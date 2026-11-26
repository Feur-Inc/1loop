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
