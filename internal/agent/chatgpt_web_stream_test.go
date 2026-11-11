// SPDX-License-Identifier: MPL-2.0

package agent

import "testing"

// The stream arrives in pieces cut anywhere, CRLF included; the
// conversation id and the end are read whatever the cuts.
func TestWebStreamPieces(t *testing.T) {
	const body = "event: delta_encoding\r\ndata: \"v1\"\r\n\r\n" +
		"data: {\"conversation_id\":\"c-1\"}\r\n\r\n" +
		"data: {\"type\":\"message_stream_complete\"}\r\n\r\n"
	for cut := 0; cut <= len(body); cut++ {
		var conversation string
		done := 0
		stream := &webStream{sink: webStreamSink{done: func(id string, err error) {
			conversation = id
			done++
			if err != nil {
				t.Errorf("cut %d: %v", cut, err)
			}
		}}}
		stream.write([]byte(body[:cut]))
		stream.write([]byte(body[cut:]))
		stream.flush()
		if done != 1 || conversation != "c-1" {
			t.Fatalf("cut %d: done %d times, conversation %q", cut, done, conversation)
		}
	}
}

// A stream that stops without its last blank line still delivers the
// last event.
func TestWebStreamFlush(t *testing.T) {
	done := 0
	stream := &webStream{sink: webStreamSink{done: func(string, error) { done++ }}}
	stream.write([]byte(`data: {"type":"message_stream_complete"}`))
	if done != 0 {
		t.Fatal("ended before the event was complete")
	}
	stream.flush()
	if done != 1 {
		t.Fatalf("done %d times", done)
	}
}
