// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"1loop/internal/agent"
)

// A message sent while the agent works waits for the end of the task
// unless the client asks to steer it in (#433).
func TestSendWhileRunningQueuesUnlessSteered(t *testing.T) {
	server, root, call := testServer(t)
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: "http://127.0.0.1:1", Workspace: root}
	body, _ := json.Marshal(map[string]any{"text": "hello"})
	if code, data := call("POST", "/api/send", bytes.NewReader(body)); code != 200 {
		t.Fatalf("send: %d %s", code, data)
	}
	server.mu.Lock()
	chat := server.chats[0]
	server.mu.Unlock()
	waitIdle(t, server, chat)

	server.mu.Lock()
	chat.run = &serverRun{calls: map[string]int{}, states: map[int]string{}, index: -1}
	server.mu.Unlock()
	for _, mode := range []string{"send", "queue", "steer"} {
		raw, _ := json.Marshal(map[string]any{"chat": chat.ID, "text": mode + " message", "mode": mode})
		if code, data := call("POST", "/api/send", bytes.NewReader(raw)); code != 200 {
			t.Fatalf("send %s: %d %s", mode, code, data)
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	chat.run = nil
	if len(chat.queue) != 2 || chat.queue[0].Content != "send message" || chat.queue[1].Content != "queue message" {
		t.Fatalf("queue = %+v, want the send and queue messages", chat.queue)
	}
	if len(chat.steer) != 1 || chat.steer[0].Content != "steer message" {
		t.Fatalf("steer = %+v, want the steer message", chat.steer)
	}
}

func TestEditQueueEndpoint(t *testing.T) {
	server, root, call := testServer(t)
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: "http://127.0.0.1:1", Workspace: root}
	body, _ := json.Marshal(map[string]any{"text": "hello"})
	if code, data := call("POST", "/api/send", bytes.NewReader(body)); code != 200 {
		t.Fatalf("send: %d %s", code, data)
	}
	server.mu.Lock()
	chat := server.chats[0]
	server.mu.Unlock()
	waitIdle(t, server, chat)

	server.mu.Lock()
	chat.run = &serverRun{calls: map[string]int{}, states: map[int]string{}, index: -1}
	chat.queue = []agent.Message{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
	}
	chat.steer = []agent.Message{
		{Role: "user", Content: "steer1"},
	}
	server.mu.Unlock()

	post := func(payload map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		if code, data := call("POST", "/api/chats/"+chat.ID+"/queue", bytes.NewReader(raw)); code != 200 {
			t.Fatalf("queue %v: %d %s", payload, code, data)
		}
	}

	post(map[string]any{"action": "move", "index": 1, "delta": -1})
	post(map[string]any{"action": "edit", "index": 0, "text": "second edited"})
	post(map[string]any{"action": "send_now", "index": 1})
	post(map[string]any{"action": "remove", "steer": true, "index": 0})

	server.mu.Lock()
	if len(chat.queue) != 1 || chat.queue[0].Content != "second edited" {
		t.Fatalf("queue = %+v, want [second edited]", chat.queue)
	}
	if len(chat.steer) != 1 || chat.steer[0].Content != "first" {
		t.Fatalf("steer = %+v, want [first]", chat.steer)
	}
	server.mu.Unlock()

	post(map[string]any{"action": "clear"})
	server.mu.Lock()
	defer server.mu.Unlock()
	chat.run = nil
	if len(chat.queue) != 0 || len(chat.steer) != 0 {
		t.Fatalf("after clear: queue=%+v steer=%+v", chat.queue, chat.steer)
	}
}
