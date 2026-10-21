package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

func testServer(t *testing.T) (*headlessServer, string, func(method, path string, body io.Reader) (int, []byte)) {
	t.Helper()
	dir, workspace := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, workspace, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	token := core.Identifier()
	server.devices = append(server.devices, remoteapi.PairedDevice{Name: "test", TokenHash: remoteapi.TokenHash(token)})
	web := httptest.NewServer(server.routes())
	t.Cleanup(web.Close)
	call := func(method, path string, body io.Reader) (int, []byte) {
		req, _ := http.NewRequest(method, web.URL+path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data
	}
	return server, server.root, call
}

func TestFileRoutes(t *testing.T) {
	_, root, call := testServer(t)
	os.MkdirAll(filepath.Join(root, "src"), 0755)
	os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0644)
	os.MkdirAll(filepath.Join(root, ".git"), 0755)

	code, data := call("GET", "/api/files?path=", nil)
	if code != 200 || !strings.Contains(string(data), `"name":"src"`) || strings.Contains(string(data), ".git") {
		t.Fatalf("list: %d %s", code, data)
	}
	code, data = call("GET", "/api/files/content?path=src/main.go", nil)
	var preview remoteapi.FilePreview
	json.Unmarshal(data, &preview)
	if code != 200 || preview.Text != "package main\n" || preview.Binary {
		t.Fatalf("content: %d %s", code, data)
	}
	if code, _ := call("GET", "/api/files/content?path=../outside", nil); code == 200 {
		t.Fatal("a path out of the project is refused")
	}
	code, data = call("POST", "/api/files/upload?path=src&name=main.go", bytes.NewReader([]byte("second")))
	if code != 200 || !strings.Contains(string(data), "main (2).go") {
		t.Fatalf("an existing name gets a number: %d %s", code, data)
	}
	code, data = call("GET", "/api/files/download?path=src/main%20(2).go", nil)
	if code != 200 || string(data) != "second" {
		t.Fatalf("download: %d %s", code, data)
	}
	// A file of the project can be attached to a message.
	if _, err := remoteapi.AttachedMessage(root, "look", []agent.FileAttachment{{Name: "main.go", Path: "src/main.go", Workspace: root}}); err != nil {
		t.Fatalf("project file attachment: %v", err)
	}
}

func TestSendAsksForHandoff(t *testing.T) {
	server, root, call := testServer(t)
	previous := agent.RunIdentity{Provider: "vllm", ConnectionID: "local", Model: "qwen"}
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "other", Model: "m", BaseURL: "http://127.0.0.1:1", Workspace: root}
	cfg := server.cfg
	cfg.RanWith = &previous
	server.chats = []*serverChat{{ID: "c1", Title: "t", Config: cfg, History: []agent.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}}

	body, _ := json.Marshal(map[string]any{"chat": "c1", "text": "next"})
	code, data := call("POST", "/api/send", bytes.NewReader(body))
	var reply struct {
		Error   string
		Handoff *agent.HandoffPlan
	}
	json.Unmarshal(data, &reply)
	if code != 409 || reply.Handoff == nil || reply.Handoff.Recommended.Mode != "as_is" {
		t.Fatalf("a model change first asks how to hand off: %d %s", code, data)
	}
	if len(server.chats[0].History) != 2 {
		t.Fatal("nothing is sent before the answer")
	}
	body, _ = json.Marshal(map[string]any{"chat": "c1", "text": "next", "handoff": map[string]string{"mode": "summary", "summarizer": "next", "level": "everything"}})
	if code, data := call("POST", "/api/send", bytes.NewReader(body)); code != 200 {
		t.Fatalf("with the choice the message goes: %d %s", code, data)
	}
}
