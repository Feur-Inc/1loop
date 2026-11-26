// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"1loop/internal/core"
)

// eventPing keeps phones and routers from dropping an idle stream.
const eventPing = 15 * time.Second

type event struct {
	kind string
	data []byte
	seq  uint64
}

// Events are the server-sent events of GET /api/events and the other long
// streams (a browser tab's) that end when a device is unpaired, shared by
// the desktop and a headless server. It has its own lock: an engine may
// broadcast while holding its own.
type Events struct {
	// Done ends every stream when the engine stops (nil: never).
	Done <-chan struct{}

	mu      sync.Mutex
	seq     uint64 // events sent so far: clients match snapshots against later events (#591)
	subs    map[chan event]bool
	streams map[chan struct{}]bool
}

// NewEvents is the events of an engine that stops with ctx.
func NewEvents(ctx context.Context) *Events {
	return &Events{Done: ctx.Done()}
}

// Seq is the sequence number of the last event broadcast.
func (events *Events) Seq() uint64 {
	events.mu.Lock()
	defer events.mu.Unlock()
	return events.seq
}

// Broadcast hands an event to every device; a device too slow to keep up
// is dropped and reconnects with a fresh state.
func (events *Events) Broadcast(kind string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	events.seq++
	for channel := range events.subs {
		select {
		case channel <- event{kind, data, events.seq}:
		default:
			close(channel)
			delete(events.subs, channel)
		}
	}
}

// Listening tells whether a device follows the events.
func (events *Events) Listening() bool {
	events.mu.Lock()
	defer events.mu.Unlock()
	return len(events.subs) > 0
}

// Disconnect ends every stream: an unpaired device's requests fail from
// now on, the others reconnect with their token.
func (events *Events) Disconnect() {
	events.mu.Lock()
	defer events.mu.Unlock()
	for channel := range events.subs {
		close(channel)
		delete(events.subs, channel)
	}
	for revoked := range events.streams {
		close(revoked)
		delete(events.streams, revoked)
	}
}

// Stream registers a long stream: revoked closes on Disconnect, and
// release forgets it once the stream ends.
func (events *Events) Stream() (revoked <-chan struct{}, release func()) {
	channel := make(chan struct{})
	events.mu.Lock()
	if events.streams == nil {
		events.streams = map[chan struct{}]bool{}
	}
	events.streams[channel] = true
	events.mu.Unlock()
	return channel, func() {
		events.mu.Lock()
		defer events.mu.Unlock()
		delete(events.streams, channel)
	}
}

// Serve is GET /api/events.
func (events *Events) Serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		core.Fail(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	channel := make(chan event, 256)
	events.mu.Lock()
	if events.subs == nil {
		events.subs = map[chan event]bool{}
	}
	events.subs[channel] = true
	events.mu.Unlock()
	defer func() {
		events.mu.Lock()
		defer events.mu.Unlock()
		delete(events.subs, channel)
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": 1loop\n\n")
	flusher.Flush()
	ping := time.NewTicker(eventPing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-events.Done:
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case next, open := <-channel:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", next.kind, next.seq, next.data); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}
