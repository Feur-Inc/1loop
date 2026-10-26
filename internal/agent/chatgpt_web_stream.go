package agent

// The answer of a ChatGPT (Web) turn is read from the page's own response
// stream (POST /backend-api/f/conversation, server-sent events), not from
// what the page displays: the stream holds the model's exact Markdown, says
// which messages are the model's text, its tool calls or ChatGPT's own
// summaries, and ends with message_stream_complete.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"1loop/internal/browser"
	"1loop/internal/platform"
)

// webStreamSink receives one turn's stream: text the model writes (with the
// id of its message), each tool call it starts, and the end.
type webStreamSink struct {
	started  func()
	text     func(message, text string, rewritten bool) // complete text, including revisions
	final    func(message, text string, rewritten bool)
	toolCall func()
	// done ends the response: err when it was cut before ChatGPT marked
	// it complete (the network, the page closing).
	done func(conversation string, err error)
}

// webStream decodes the "v1" delta encoding: each event patches the message
// being written; an event without "p" or "o" reuses the previous ones.
type webStream struct {
	sink         webStreamSink
	pending      []byte
	path, op     string
	message      webMessage
	object       int
	objects      map[int]webMessage
	texts        map[string]string // what each message's text was so far
	conversation string
	finished     bool
}

type webMessage struct {
	id, role, kind string
	visible, final bool
}

func (stream *webStream) write(data []byte) {
	stream.pending = append(stream.pending, data...)
	// Lines may end with CRLF or CR as well (the SSE format); a CR at the
	// end may be the first half of a CRLF split across two writes.
	if bytes.IndexByte(stream.pending, '\r') >= 0 {
		tail := bytes.HasSuffix(stream.pending, []byte("\r"))
		if tail {
			stream.pending = stream.pending[:len(stream.pending)-1]
		}
		stream.pending = bytes.ReplaceAll(stream.pending, []byte("\r\n"), []byte("\n"))
		stream.pending = bytes.ReplaceAll(stream.pending, []byte("\r"), []byte("\n"))
		if tail {
			stream.pending = append(stream.pending, '\r')
		}
	}
	for {
		end := bytes.Index(stream.pending, []byte("\n\n"))
		if end < 0 {
			return
		}
		block := string(stream.pending[:end])
		stream.pending = stream.pending[end+2:]
		var payload []string
		for _, line := range strings.Split(block, "\n") {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				payload = append(payload, strings.TrimPrefix(value, " "))
			}
		}
		if len(payload) > 0 {
			stream.event(strings.Join(payload, "\n"))
		}
	}
}

func (stream *webStream) event(data string) {
	if stream.finished {
		return
	}
	if data == "[DONE]" {
		stream.finish(nil)
		return
	}
	var event struct {
		Type           string          `json:"type"`
		Object         *int            `json:"c"`
		ConversationID string          `json:"conversation_id"`
		Path           *string         `json:"p"`
		Op             *string         `json:"o"`
		Value          json.RawMessage `json:"v"`
	}
	if json.Unmarshal([]byte(data), &event) != nil { // "v1", the encoding's name
		return
	}
	if event.ConversationID != "" {
		stream.conversation = event.ConversationID
	}
	if event.Type != "" {
		if event.Type == "message_stream_complete" {
			stream.finish(nil)
		}
		return
	}
	if event.Path != nil {
		stream.path = *event.Path
	}
	if event.Op != nil {
		stream.op = *event.Op
	}
	if event.Object != nil {
		stream.object = *event.Object
		stream.message = stream.objects[stream.object]
	}
	stream.apply(stream.path, stream.op, event.Value)
}

func (stream *webStream) apply(path, op string, value json.RawMessage) {
	switch {
	case op == "add" && path == "":
		var added struct {
			Message struct {
				ID        string `json:"id"`
				Channel   string `json:"channel"`
				Recipient string `json:"recipient"`
				Author    struct {
					Role string `json:"role"`
				} `json:"author"`
				Content struct {
					Kind  string            `json:"content_type"`
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
				Metadata struct {
					Hidden bool `json:"is_visually_hidden_from_conversation"`
				} `json:"metadata"`
			} `json:"message"`
			ConversationID string `json:"conversation_id"`
		}
		if json.Unmarshal(value, &added) != nil || added.Message.ID == "" {
			return
		}
		if added.ConversationID != "" {
			stream.conversation = added.ConversationID
		}
		m := added.Message
		stream.message = webMessage{id: m.ID, role: m.Author.Role, kind: m.Content.Kind,
			visible: m.Author.Role == "assistant" && m.Content.Kind == "text" && !m.Metadata.Hidden && (m.Recipient == "" || m.Recipient == "all"),
			final:   m.Channel == "final"}
		if stream.objects == nil {
			stream.objects = make(map[int]webMessage)
		}
		stream.objects[stream.object] = stream.message
		if m.Author.Role == "assistant" && m.Content.Kind == "code" {
			stream.sink.toolCall()
		}
		if stream.message.visible && len(m.Content.Parts) > 0 {
			var text string
			if json.Unmarshal(m.Content.Parts[0], &text) == nil {
				stream.setText(m.ID, text)
			}
		}
	case op == "append" && path == "/message/content/parts/0":
		var text string
		if stream.message.visible && json.Unmarshal(value, &text) == nil && text != "" {
			stream.setText(stream.message.id, stream.texts[stream.message.id]+text)
		}
	case op == "patch":
		var operations []struct {
			Path  string          `json:"p"`
			Op    string          `json:"o"`
			Value json.RawMessage `json:"v"`
		}
		if json.Unmarshal(value, &operations) == nil {
			for _, operation := range operations {
				stream.apply(operation.Path, operation.Op, operation.Value)
			}
		}
	}
}

// setText records a message's whole text: ChatGPT also sends a message
// again in full, which must not repeat what was already shown.
func (stream *webStream) setText(id, text string) {
	if stream.texts == nil {
		stream.texts = map[string]string{}
	}
	before := stream.texts[id]
	if before == text {
		return
	}
	if stream.message.final {
		stream.sink.final(id, text, !strings.HasPrefix(text, before))
	} else {
		stream.sink.text(id, text, !strings.HasPrefix(text, before))
	}
	stream.texts[id] = text
}

func (stream *webStream) finish(err error) {
	if !stream.finished {
		stream.finished = true
		stream.sink.done(stream.conversation, err)
	}
}

// webStreams follows one page's conversation streams and hands the one
// started during a turn to that turn.
type webStreams struct {
	page     *browser.Page
	mu       sync.Mutex
	sink     *webStreamSink
	watching map[network.RequestID]*webWatch
	// following is the response the turn reads: the page can receive the
	// same stream twice (its service worker relays it), so another one
	// counts only once this one ended (after an approval, the next part).
	following network.RequestID
}

// webWatch is one response: data arriving while its earlier part is being
// fetched waits, so the decoder gets it in order.
type webWatch struct {
	stream   *webStream
	ready    bool
	queued   [][]byte
	finished bool
	failure  error // the response failed (EventLoadingFailed)
}

func (bridge *ChatGPTWeb) streams(ctx context.Context, tab *browser.Page) (*webStreams, error) {
	if err := tab.Run(ctx); err != nil { // the page has started
		return nil, err
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.pageStreams == nil {
		bridge.pageStreams = map[*browser.Page]*webStreams{}
	}
	if streams := bridge.pageStreams[tab]; streams != nil {
		return streams, nil
	}
	streams := &webStreams{page: tab, watching: map[network.RequestID]*webWatch{}}
	bridge.pageStreams[tab] = streams
	chromedp.ListenTarget(tab.Ctx, streams.event)
	return streams, nil
}

// attach gives the next conversation stream to sink until the returned
// function is called.
func (streams *webStreams) attach(sink webStreamSink) func() {
	streams.mu.Lock()
	streams.sink = &sink
	streams.mu.Unlock()
	return func() {
		streams.mu.Lock()
		if streams.sink == &sink {
			streams.sink = nil
		}
		streams.mu.Unlock()
	}
}

func (streams *webStreams) event(ev any) {
	switch e := ev.(type) {
	case *network.EventResponseReceived:
		if !strings.HasSuffix(e.Response.URL, "/backend-api/f/conversation") || !strings.Contains(e.Response.MimeType, "event-stream") {
			return
		}
		streams.mu.Lock()
		if streams.sink == nil {
			streams.mu.Unlock()
			return
		}
		if previous := streams.watching[streams.following]; previous != nil && !previous.stream.finished {
			streams.mu.Unlock()
			platform.Devf("chatgpt-web", "response %s ignored: %s is still followed", e.RequestID, streams.following)
			return
		}
		delete(streams.watching, streams.following) // a completed SSE response may keep its connection open
		watch := &webWatch{stream: &webStream{sink: *streams.sink}}
		streams.watching[e.RequestID] = watch
		streams.following = e.RequestID
		streams.sink.started()
		streams.mu.Unlock()
		id := e.RequestID
		go func() {
			var buffered []byte
			err := chromedp.Run(streams.page.Ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				data, err := network.StreamResourceContent(id).Do(ctx)
				buffered = data
				return err
			}))
			streams.mu.Lock()
			defer streams.mu.Unlock()
			if err != nil {
				watch.stream.finish(fmt.Errorf("Unable to read ChatGPT's answer stream: %w", err))
				delete(streams.watching, id)
				return
			}
			watch.stream.write(buffered)
			for _, data := range watch.queued {
				watch.stream.write(data)
			}
			watch.ready, watch.queued = true, nil
			if watch.finished {
				watch.stream.write([]byte("\n\n"))
				watch.stream.finish(watch.failure)
				delete(streams.watching, id)
			}
		}()
	case *network.EventDataReceived:
		if e.Data == "" {
			return
		}
		streams.mu.Lock()
		defer streams.mu.Unlock()
		if watch := streams.watching[e.RequestID]; watch != nil {
			data, err := base64.StdEncoding.DecodeString(e.Data)
			if err != nil {
				return
			}
			if watch.ready {
				watch.stream.write(data)
			} else {
				watch.queued = append(watch.queued, data)
			}
		}
	case *network.EventLoadingFinished:
		streams.end(e.RequestID, nil)
	case *network.EventLoadingFailed:
		streams.end(e.RequestID, fmt.Errorf("ChatGPT's answer was interrupted (%s): send the message again", e.ErrorText))
	}
}

// end closes a response. One that finished loading without ChatGPT's
// completion mark still ends its turn with what it carried (ChatGPT also
// ends a response this way when it asks to allow a call); one that failed
// ends it with that error.
func (streams *webStreams) end(id network.RequestID, failure error) {
	streams.mu.Lock()
	defer streams.mu.Unlock()
	watch := streams.watching[id]
	if watch == nil {
		return
	}
	if !watch.ready {
		watch.finished, watch.failure = true, failure
		return
	}
	watch.stream.write([]byte("\n\n"))
	watch.stream.finish(failure)
	delete(streams.watching, id)
}
