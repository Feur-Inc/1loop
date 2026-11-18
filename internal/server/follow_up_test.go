// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"1loop/internal/agent"
)

// A follow-up to a chat joins it: its turn runs and the model gets both
// messages (#545).
func TestFollowUpMessages(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	server, root, call := testServer(t)
	connection := agent.Connection{ID: "c", Name: "Local", Provider: "compatible", BaseURL: model.URL, Enabled: true, AllModels: true}
	server.mu.Lock()
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: model.URL, Workspace: root}
	server.choices = map[string]agent.ModelChoice{"m · Local": {Connection: connection, ID: "m"}}
	server.mu.Unlock()
	send := func(body map[string]any) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		code, data := call("POST", "/api/send", bytes.NewReader(raw))
		if code != 200 {
			t.Fatalf("send %v: %d %s", body, code, data)
		}
		var reply struct{ Chat string }
		json.Unmarshal(data, &reply)
		return reply.Chat
	}
	id := send(map[string]any{"text": "first", "workspace": root})
	server.mu.Lock()
	chat := server.chatLocked(id)
	server.mu.Unlock()
	waitIdle(t, server, chat)
	send(map[string]any{"chat": id, "text": "second"})
	waitIdle(t, server, chat)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("notes"), 0o644)
	send(map[string]any{"chat": id, "text": "third", "attachments": []agent.FileAttachment{{Name: "notes.txt", Path: "notes.txt", Workspace: root}}})
	waitIdle(t, server, chat)
	server.mu.Lock()
	defer server.mu.Unlock()
	var users []string
	for _, message := range chat.History {
		if message.Role == "user" && !message.Internal {
			users = append(users, message.Content)
		}
	}
	if len(users) != 3 {
		t.Fatalf("user messages in the chat: %q", users)
	}
}
