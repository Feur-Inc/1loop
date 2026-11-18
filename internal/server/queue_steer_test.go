// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"1loop/internal/agent"
)

// While the agent works, a message sent with "steer" joins it at its next
// step; one sent with "send" or "queue" waits for the task to end (#433):
// every message joined the running task.
func TestSendWhileRunningQueuesOrSteers(t *testing.T) {
	server, root, call := testServer(t)
	chat := &serverChat{ID: "busy", Title: "Busy", Config: agent.Config{Workspace: root}, run: &serverRun{cancel: func() {}}}
	server.mu.Lock()
	server.chats = append(server.chats, chat)
	server.mu.Unlock()
	for _, send := range []struct{ text, mode string }{{"later", "send"}, {"now", "steer"}, {"after that", "queue"}} {
		body, _ := json.Marshal(map[string]string{"chat": "busy", "text": send.text, "mode": send.mode})
		if code, data := call("POST", "/api/send", bytes.NewReader(body)); code != 200 {
			t.Fatalf("%s: %d %s", send.mode, code, data)
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	texts := func(messages []agent.Message) (out []string) {
		for _, m := range messages {
			out = append(out, m.Content)
		}
		return out
	}
	if got := texts(chat.queue); len(got) != 2 || got[0] != "later" || got[1] != "after that" {
		t.Errorf("queued: %q", got)
	}
	if got := texts(chat.steer); len(got) != 1 || got[0] != "now" {
		t.Errorf("steered: %q", got)
	}
}
