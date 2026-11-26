// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/projectsync"
)

// moveModel writes a.txt, then b.txt, then answers, whoever asks.
func moveModel(t *testing.T) (string, chan string) {
	t.Helper()
	prompts := make(chan string, 64)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Messages []struct{ Role, Content string }
		}
		json.Unmarshal(body, &request)
		results := 0
		for _, message := range request.Messages {
			if message.Role == "tool" {
				results++
			}
		}
		prompts <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		call := func(name, text string) {
			args, _ := json.Marshal(map[string]string{"path": name, "old_text": "", "new_text": text})
			quoted, _ := json.Marshal(string(args))
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c%d\",\"type\":\"function\",\"function\":{\"name\":\"edit_file\",\"arguments\":%s}}]}}]}\n\n", results, quoted)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		}
		switch results {
		case 0:
			time.Sleep(300 * time.Millisecond) // the pause is asked during this step
			call("a.txt", "from the first device\n")
		case 1:
			call("b.txt", "from the second device\n")
		default:
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Both files are written.\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(model.Close)
	return model.URL + "/v1", prompts
}

func movingServer(t *testing.T, name, url string) (*headlessServer, string) {
	t.Helper()
	server, root, _ := testServer(t)
	server.name, server.fingerprint = name, name
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: name, Model: "m", BaseURL: url, Workspace: root}
	server.approvalMode = "yolo"
	return server, root
}

// A task at work goes on on the other device (#18): the first step here, the
// second there, with what waited in the queue.
func TestMoveRunningChat(t *testing.T) {
	url, _ := moveModel(t)
	first, root := movingServer(t, "first", url)
	second, _ := movingServer(t, "second", url)
	first.mu.Lock()
	chat := &serverChat{ID: "chat1", Title: "Two files", Config: first.cfg, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	first.chats = append(first.chats, chat)
	first.runLocked(chat, agent.Message{Role: "user", Content: "write both files"})
	chat.queue = append(chat.queue, agent.Message{Role: "user", Content: "and then this"})
	first.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	here, there := first.syncEngine(), second.syncEngine()
	bases := &projectsync.BaseStore{Path: filepath.Join(t.TempDir(), "bases.json")}
	report, err := projectsync.Move(ctx, here, "first", root, there, "second", "moved-project", "chat1", bases, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	second.mu.Lock()
	moved := second.chatLocked("chat1")
	second.mu.Unlock()
	if moved == nil {
		t.Fatal("the chat did not arrive")
	}
	waitIdle(t, second, moved)
	for name, want := range map[string]string{"a.txt": "from the first device\n", "b.txt": "from the second device\n"} {
		if data, _ := os.ReadFile(filepath.Join(report.Workspace, name)); string(data) != want {
			t.Errorf("%s there: %q", name, data)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err == nil {
		t.Error("the second step ran on the first device")
	}
	second.mu.Lock()
	defer second.mu.Unlock()
	text := ""
	for _, message := range moved.History {
		text += message.Role + ": " + message.Content + "\n"
	}
	for _, want := range []string{"[Task moved here from first]", "Both files are written.", "and then this"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is missing there", want)
		}
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if chat.run != nil || chat.moving || len(chat.queue) > 0 {
		t.Errorf("here: running %v, moving %v, %d queued", chat.run != nil, chat.moving, len(chat.queue))
	}
	if last := chat.Items[len(chat.Items)-1]; last.Kind != "note" || !strings.HasPrefix(last.Text, "Moved to second") {
		t.Errorf("last item here %+v", last)
	}
	for _, item := range chat.Items {
		if item.Kind == "error" {
			t.Errorf("an error here: %s", item.Text)
		}
	}
}

// A device that cannot take the task leaves it at work here.
func TestMoveGoesBack(t *testing.T) {
	url, _ := moveModel(t)
	first, root := movingServer(t, "first", url)
	second, _ := movingServer(t, "second", url)
	second.cfg.Model = "" // nothing to go on with there
	first.mu.Lock()
	chat := &serverChat{ID: "chat1", Title: "Two files", Config: first.cfg, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	first.chats = append(first.chats, chat)
	first.runLocked(chat, agent.Message{Role: "user", Content: "write both files"})
	first.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bases := &projectsync.BaseStore{Path: filepath.Join(t.TempDir(), "bases.json")}
	_, err := projectsync.Move(ctx, first.syncEngine(), "first", root, second.syncEngine(), "second", "moved-project", "chat1", bases, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Configure a provider") {
		t.Fatalf("moved: %v", err)
	}
	waitIdle(t, first, chat)
	if data, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(data) != "from the second device\n" {
		t.Errorf("the task did not go on here: b.txt %q", data)
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if chat.moving {
		t.Error("still moving")
	}
}
