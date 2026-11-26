// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// Each event carries its number as its id, a read of the state or of a
// chat the number of the last one sent, and an ETag that spares the body
// when nothing changed (#591).
func TestSnapshotSeqAndETag(t *testing.T) {
	server, _, _ := testServer(t)
	token := core.Identifier()
	server.mu.Lock()
	server.devices = append(server.devices, remoteapi.PairedDevice{Name: "desk", TokenHash: remoteapi.TokenHash(token)})
	chat := &serverChat{ID: "c1", Title: "t", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	chat.Items = []agent.ChatItem{{Kind: "user", Text: "hi"}}
	server.chats = []*serverChat{chat}
	server.mu.Unlock()
	routes := server.routes()
	get := func(path, tag string) *http.Response {
		t.Helper()
		request, _ := http.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		if tag != "" {
			request.Header.Set("If-None-Match", tag)
		}
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, request)
		return recorder.Result()
	}

	first := get("/api/chats/c1", "")
	tag := first.Header.Get("ETag")
	if first.StatusCode != 200 || tag == "" || first.Header.Get("X-1loop-Seq") == "" {
		t.Fatalf("first read: %d, ETag %q, seq %q", first.StatusCode, tag, first.Header.Get("X-1loop-Seq"))
	}
	if again := get("/api/chats/c1", tag); again.StatusCode != http.StatusNotModified {
		t.Fatalf("unchanged chat: %d, want 304", again.StatusCode)
	}
	seq, _ := strconv.ParseUint(first.Header.Get("X-1loop-Seq"), 10, 64)
	server.mu.Lock()
	chat.Items = append(chat.Items, agent.ChatItem{Kind: "assistant", Text: "hello"})
	server.broadcastLocked("item", map[string]any{"chat": "c1", "index": 1})
	server.mu.Unlock()
	changed := get("/api/chats/c1", tag)
	if changed.StatusCode != 200 || changed.Header.Get("ETag") == tag {
		t.Fatalf("changed chat: %d, ETag %q", changed.StatusCode, changed.Header.Get("ETag"))
	}
	if next, _ := strconv.ParseUint(changed.Header.Get("X-1loop-Seq"), 10, 64); next != seq+1 {
		t.Fatalf("seq after one event: %d, want %d", next, seq+1)
	}
	if state := get("/api/state", ""); state.StatusCode != 200 || state.Header.Get("ETag") == "" {
		t.Fatalf("state: %d", state.StatusCode)
	}
}

// The event stream writes each event's number as its id.
func TestEventsCarrySeq(t *testing.T) {
	server, _, _ := testServer(t)
	token := core.Identifier()
	server.mu.Lock()
	server.devices = append(server.devices, remoteapi.PairedDevice{Name: "desk", TokenHash: remoteapi.TokenHash(token)})
	server.mu.Unlock()
	web := httptest.NewServer(server.routes())
	defer web.Close()
	req, _ := http.NewRequest("GET", web.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := web.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !server.hub.Listening() {
		if time.Now().After(deadline) {
			t.Fatal("event stream did not attach")
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.mu.Lock()
	server.broadcastLocked("meta", map[string]string{"chat": "c1"})
	server.broadcastLocked("meta", map[string]string{"chat": "c1"})
	server.mu.Unlock()
	scanner := bufio.NewScanner(resp.Body)
	var ids []uint64
	for len(ids) < 2 && scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "id: ") {
			seq, err := strconv.ParseUint(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatalf("bad id %q: %v", line, err)
			}
			ids = append(ids, seq)
		}
	}
	if len(ids) != 2 || ids[0] == 0 || ids[1] != ids[0]+1 {
		t.Fatalf("seqs %v", ids)
	}
}
