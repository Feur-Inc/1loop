// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"1loop/internal/core"
)

// The naming model (#425) gets only the words the user typed: neither the
// pictures nor the files attached to the first prompt, nor the lines that
// list the files, reach it.
func TestNameChatSendsOnlyTheText(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"\\\"Fix the login page crash.\\\"\\nExtra line\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	cfg := Config{Provider: "compatible", ConnectionID: "srv", Model: "namer", BaseURL: server.URL}
	files := []FileAttachment{{Name: "secret-notes.pdf", Path: "/tmp/ws/secret-notes.pdf", Workspace: "/tmp/ws"}, {Name: "shot.png", Path: "/tmp/ws/shot.png", Workspace: "/tmp/ws", Image: true}}
	images := []core.ImagePart{{Mime: "image/png", Data: "iVBORw0KGgoPICTUREBYTES"}}
	prompt := PromptMessage("The login page crashes when I submit", images, files)

	title, err := (&App{}).NameChat(context.Background(), cfg, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Fix the login page crash" {
		t.Fatalf("title %q", title)
	}
	if len(bodies) != 1 {
		t.Fatalf("%d requests", len(bodies))
	}
	body := bodies[0]
	if !strings.Contains(body, "The login page crashes when I submit") {
		t.Fatalf("the prompt's words are missing: %s", body)
	}
	for _, leak := range []string{"PICTUREBYTES", "image_url", "image/png", "secret-notes", "shot.png", "/tmp/ws", "attached these files"} {
		if strings.Contains(body, leak) {
			t.Errorf("the naming model received %q: %s", leak, body)
		}
	}
	if strings.Contains(body, `"tools"`) {
		t.Errorf("the naming model is offered tools: %s", body)
	}

	// Only pictures or files: nothing to name from, and nothing is sent.
	if _, err := (&App{}).NameChat(context.Background(), cfg, PromptMessage("  ", images, files)); !errors.Is(err, ErrNothingToName) {
		t.Fatalf("an attachment-only prompt: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatal("an attachment-only prompt reached the naming model")
	}
}

func TestNamingPrompt(t *testing.T) {
	m := PromptMessage("Hello there", []core.ImagePart{{Mime: "image/png", Data: "x"}}, []FileAttachment{{Name: "a.txt", Path: "/w/a.txt"}})
	got := NamingPrompt(m)
	if got.Content != "Hello there" || len(got.Images) != 0 || len(got.Files) != 0 || got.Role != "user" {
		t.Fatalf("%+v", got)
	}
}

func TestChatTitleText(t *testing.T) {
	long := strings.Repeat("word ", 30)
	for in, want := range map[string]string{
		"  Fix login  ":                  "Fix login",
		"\"Fix login.\"":                 "Fix login",
		"**Title:** Refactor the parser": "Refactor the parser",
		"Title: Build the APK\nmore":     "Build the APK",
		"\n\n# Docs update\n":            "Docs update",
		"« Corriger le bug »":            "Corriger le bug",
		"":                               "",
		long:                             strings.TrimSpace(strings.Repeat("word ", 12)),
	} {
		if got := ChatTitleText(in); got != want {
			t.Errorf("ChatTitleText(%q) = %q, want %q", in, got, want)
		}
	}
}

// The chats tool's rename action (#425) retitles this chat through its host.
func TestChatsRename(t *testing.T) {
	host := &fakeSubchats{}
	cfg := Config{Chats: host.chats("")}
	output, err, asked := runTool(t, "chats", cfg, false, false, map[string]any{"action": "rename", "text": "  \"Fix the parser.\" "})
	if err != nil || len(asked) != 0 || len(host.renamed) != 1 || host.renamed[0] != "Fix the parser" || !strings.Contains(output, "“Fix the parser”") {
		t.Fatalf("rename: %q %v asked %q renamed %v", output, err, asked, host.renamed)
	}
	if _, err, _ := runTool(t, "chats", cfg, true, true, map[string]any{"action": "rename", "text": "  "}); err == nil || len(host.renamed) != 1 {
		t.Fatalf("a blank title: %v %v", err, host.renamed)
	}
	if _, err, _ := runTool(t, "chats", Config{Chats: &ChatAccess{}}, true, true, map[string]any{"action": "rename", "text": "x"}); err == nil {
		t.Fatal("a host that cannot rename")
	}
	function := chatsToolDefinition()["function"].(map[string]any)
	action := function["parameters"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)
	if enum := action["enum"].([]string); enum[len(enum)-1] != "rename" || !strings.Contains(function["description"].(string), "rename") {
		t.Fatalf("rename is not offered: %v", action)
	}
}
