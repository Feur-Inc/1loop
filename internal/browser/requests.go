// SPDX-License-Identifier: MPL-2.0

package browser

// What a page asked the network for, kept for the pages that record
// (Loopy's): the request inspector. Only the latest are.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const maxRequests = 400

// Request is a request of a page.
type Request struct {
	ID        string            `json:"id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Type      string            `json:"type"`
	Status    int64             `json:"status,omitempty"`
	Mime      string            `json:"mime,omitempty"`
	Size      int64             `json:"size,omitempty"`
	Failed    string            `json:"failed,omitempty"`
	Done      bool              `json:"done"`
	Began     time.Time         `json:"began"`
	Took      int64             `json:"ms,omitempty"`
	Sent      map[string]string `json:"-"`
	Received  map[string]string `json:"-"`
	Body      string            `json:"-"` // what was sent, cut
	requestID network.RequestID
}

type requestLog struct {
	mu   sync.Mutex
	next int
	list []*Request
	open int
	last time.Time // something started or ended
}

// Record keeps the page's requests from now on; before Start, all of them.
func (browser *Page) Record() {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	if browser.requests == nil {
		browser.requests = &requestLog{last: time.Now()}
	}
}

func (browser *Page) requestLog() *requestLog {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	return browser.requests
}

func headersOf(headers network.Headers) map[string]string {
	out := map[string]string{}
	for name, value := range headers {
		out[name] = fmt.Sprint(value)
	}
	return out
}

func (log *requestLog) find(id network.RequestID) *Request {
	for i := len(log.list) - 1; i >= 0; i-- {
		if log.list[i].requestID == id {
			return log.list[i]
		}
	}
	return nil
}

func (browser *Page) record(event any) {
	log := browser.requestLog()
	if log == nil {
		return
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	end := func(id network.RequestID, change func(*Request)) {
		if r := log.find(id); r != nil && !r.Done {
			r.Done, r.Took = true, time.Since(r.Began).Milliseconds()
			change(r)
			log.open--
			log.last = time.Now()
		}
	}
	switch e := event.(type) {
	case *network.EventRequestWillBeSent:
		if strings.HasPrefix(e.Request.URL, "data:") || strings.HasPrefix(e.Request.URL, "blob:") {
			return
		}
		if e.RedirectResponse != nil { // the same id goes on elsewhere
			end(e.RequestID, func(r *Request) {
				r.Status, r.Received = e.RedirectResponse.Status, headersOf(e.RedirectResponse.Headers)
			})
			if r := log.find(e.RequestID); r != nil {
				r.requestID += "#" // its body is gone: the id is the next one's
			}
		}
		log.next++
		r := &Request{ID: fmt.Sprint(log.next), Method: e.Request.Method, URL: e.Request.URL, Type: strings.ToLower(e.Type.String()), Began: time.Now(), Sent: headersOf(e.Request.Headers), requestID: e.RequestID}
		for _, part := range e.Request.PostDataEntries {
			if data, err := base64.StdEncoding.DecodeString(part.Bytes); err == nil && len(r.Body) < 64<<10 {
				r.Body += string(data)
			}
		}
		r.Body = r.Body[:min(len(r.Body), 64<<10)]
		log.list = append(log.list, r)
		if len(log.list) > maxRequests {
			if !log.list[0].Done {
				log.open--
			}
			log.list = append([]*Request(nil), log.list[len(log.list)-maxRequests:]...)
		}
		log.open++
		log.last = time.Now()
	case *network.EventRequestWillBeSentExtraInfo: // with the cookies
		if r := log.find(e.RequestID); r != nil {
			for name, value := range headersOf(e.Headers) {
				r.Sent[name] = value
			}
		}
	case *network.EventResponseReceived:
		if r := log.find(e.RequestID); r != nil {
			r.Status, r.Mime, r.Received = e.Response.Status, e.Response.MimeType, headersOf(e.Response.Headers)
			if strings.HasPrefix(e.Response.MimeType, "text/event-stream") { // never ends
				end(e.RequestID, func(*Request) {})
			}
		}
	case *network.EventLoadingFinished:
		end(e.RequestID, func(r *Request) { r.Size = int64(e.EncodedDataLength) })
	case *network.EventLoadingFailed:
		end(e.RequestID, func(r *Request) {
			if r.Failed = e.ErrorText; e.Canceled {
				r.Failed = "cancelled"
			}
		})
	}
}

// Requests are those the page made, the latest last, whose address holds
// match and whose type is kind ("" for all; "xhr" is fetch too).
func (browser *Page) Requests(match, kind string) []Request {
	log := browser.requestLog()
	if log == nil {
		return nil
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	var out []Request
	for _, r := range log.list {
		if strings.Contains(strings.ToLower(r.URL), strings.ToLower(match)) && (kind == "" || r.Type == kind || kind == "xhr" && r.Type == "fetch") {
			out = append(out, *r)
		}
	}
	return out
}

// Response is a request and the body that came back, cut at limit bytes.
func (browser *Page) Response(ctx context.Context, id string, limit int) (Request, string, error) {
	var found *Request
	for _, r := range browser.Requests("", "") {
		if r.ID == id {
			found = &r
		}
	}
	if found == nil {
		return Request{}, "", errors.New("no such request: only the latest are kept")
	}
	if !found.Done || strings.HasSuffix(string(found.requestID), "#") {
		return *found, "", nil
	}
	var body []byte
	err := browser.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) (err error) {
		body, err = network.GetResponseBody(found.requestID).Do(ctx)
		return err
	}))
	if err != nil {
		if strings.Contains(err.Error(), "No resource") || strings.Contains(err.Error(), "No data") {
			return *found, "", nil // the browser no longer has it
		}
		return *found, "", err
	}
	return *found, string(body[:min(len(body), limit)]), nil
}

// Idle waits until the page has had no request under way for quiet.
func (browser *Page) Idle(ctx context.Context, quiet time.Duration) error {
	log := browser.requestLog()
	if log == nil {
		return errors.New("this page does not record its requests")
	}
	if err := browser.Run(ctx); err != nil { // started
		return err
	}
	for {
		log.mu.Lock()
		still := log.open <= 0 && time.Since(log.last) >= quiet
		log.mu.Unlock()
		if still {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-browser.Life.Done():
			return errors.New("the page was closed")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Used is when the user last acted in the page, and whether it is on show
// somewhere.
func (browser *Page) Used() (last time.Time, shown bool) {
	browser.inputMu.Lock()
	last = browser.used
	browser.inputMu.Unlock()
	return last, browser.isVisible() || browser.phonesWatching()
}
