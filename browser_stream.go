package main

import (
	"encoding/binary"
	"net/http"
	"time"
)

func serveBrowserStream(writer http.ResponseWriter, req *http.Request, browser *browserPage, revoked <-chan struct{}) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		fail(writer, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	watcher, stop := browser.watch(req.URL.Query().Get("format") == "png")
	defer stop()
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	send := func(kind byte, payload []byte) error {
		var head [5]byte
		head[0] = kind
		binary.BigEndian.PutUint32(head[1:], uint32(len(payload)))
		if _, err := writer.Write(head[:]); err != nil {
			return err
		}
		_, err := writer.Write(payload)
		return err
	}
	ping := time.NewTicker(15 * time.Second) // the state again: keeps the stream alive
	defer ping.Stop()
	ctx := req.Context()
	for {
		select {
		case <-revoked:
			return
		case <-ctx.Done():
			return
		case <-browser.life.Done():
			return
		case <-ping.C:
			watcher.put(nil, browser.phoneStateJSON())
			continue
		case <-watcher.wake:
		}
		frame, state := watcher.take()
		if state != nil && send('S', state) != nil {
			return
		}
		if frame != nil && send('F', frame) != nil {
			return
		}
		flusher.Flush()
	}
}
