// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"1loop/internal/agent"
)

// The chat naming model (#425): once chosen, it names a new chat from the
// words of its first prompt, never its pictures, and every client hears of
// the new title. Unchosen, a chat keeps the start of its prompt.
func TestChatNamingModel(t *testing.T) {
	var mu sync.Mutex
	var naming []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		answer := "ok"
		if strings.Contains(string(data), "You name chats") {
			mu.Lock()
			naming = append(naming, string(data))
			mu.Unlock()
			answer = "Login crash fix"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + answer + `"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	server, root, call := testServer(t)
	connection := agent.Connection{ID: "c", Name: "Local", Provider: "compatible", BaseURL: model.URL, Enabled: true, AllModels: true}
	server.mu.Lock()
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: model.URL, Workspace: root}
	server.choices = map[string]agent.ModelChoice{"m · Local": {Connection: connection, ID: "m"}, "namer · Local": {Connection: connection, ID: "namer"}}
	server.mu.Unlock()
	post := func(path string, body any) (int, string) {
		raw, _ := json.Marshal(body)
		code, data := call("POST", path, bytes.NewReader(raw))
		return code, string(data)
	}
	send := func(text string) *serverChat {
		t.Helper()
		if code, data := post("/api/send", map[string]any{"text": text, "images": []map[string]string{{"mime": "image/png", "data": "iVBORw0KGgoPICTUREBYTES"}}}); code != 200 {
			t.Fatalf("send: %d %s", code, data)
		}
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.chats[0]
	}
	title := func(chat *serverChat) string {
		server.mu.Lock()
		defer server.mu.Unlock()
		return chat.Title
	}

	// No naming model: as before.
	plain := send("The login page crashes when I submit")
	waitIdle(t, server, plain)
	if got := title(plain); got != "The login page crashes when I submit" {
		t.Fatalf("without a naming model: %q", got)
	}

	if code, _ := post("/api/naming-model", map[string]any{"model": map[string]string{"connectionId": "c", "model": "gone"}}); code != 400 {
		t.Fatalf("an unknown model is refused: %d", code)
	}
	if code, data := post("/api/naming-model", map[string]any{"model": map[string]string{"connectionId": "c", "model": "namer"}}); code != 200 {
		t.Fatalf("choose: %d %s", code, data)
	}
	server.mu.Lock()
	settings := server.settingsLocked()
	server.mu.Unlock()
	if chosen, _ := settings["namingModel"].(*agent.NamingModel); !chosen.Set() || chosen.Model != "namer" {
		t.Fatalf("the clients' settings: %+v", settings["namingModel"])
	}

	named := send("The login page crashes when I submit")
	waitFor(t, server, "the naming model's title", func() bool { return named.Title == "Login crash fix" })
	waitIdle(t, server, named)
	mu.Lock()
	if len(naming) != 1 || !strings.Contains(naming[0], "The login page crashes when I submit") || strings.Contains(naming[0], "PICTUREBYTES") || strings.Contains(naming[0], "image_url") {
		t.Fatalf("what the naming model got: %q", naming)
	}
	mu.Unlock()
	// The chat's own model still saw the picture.
	server.mu.Lock()
	images := len(named.History[0].Images)
	server.mu.Unlock()
	if images != 1 {
		t.Fatal("the chat's model lost the picture")
	}

	// The agent's chats tool renames its own chat.
	if err := server.chatAccess(named).Rename(context.Background(), "Parser refactor"); err != nil || title(named) != "Parser refactor" {
		t.Fatalf("rename: %v %q", err, title(named))
	}

	// Off again.
	if code, _ := post("/api/naming-model", map[string]any{"model": nil}); code != 200 {
		t.Fatalf("turn off: %d", code)
	}
	server.mu.Lock()
	off := server.namingModel == nil
	server.mu.Unlock()
	if !off {
		t.Fatal("the naming model stays chosen")
	}
}
