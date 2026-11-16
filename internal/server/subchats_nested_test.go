// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"1loop/internal/agent"
)

// nestingModel is a model that starts a subchat whenever its brief says
// SPLIT, in every chat, subchats included; says what the tool answered; and
// acknowledges a subchat's report.
func nestingModel(t *testing.T) string {
	t.Helper()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		json.Unmarshal(raw, &request)
		last := request.Messages[len(request.Messages)-1]
		for i := len(request.Messages) - 1; i >= 0; i-- { // the last turn's, past 1loop's own notes
			if role := request.Messages[i].Role; (role == "user" || role == "tool") && !strings.Contains(fmt.Sprint(request.Messages[i].Content), "[1loop context") {
				last = request.Messages[i]
				break
			}
		}
		content := fmt.Sprint(last.Content)
		w.Header().Set("Content-Type", "text/event-stream")
		answer := func(text string) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", text)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
		switch {
		case last.Role == "tool":
			answer("Tool said: " + content)
		case strings.Contains(content, "[Subchat "):
			answer("Got the report.")
		case strings.Contains(content, "SPLIT"):
			arguments, _ := json.Marshal(map[string]any{"action": "start", "id": nil, "title": "Nested", "brief": "SPLIT deeper", "text": nil,
				"context": nil, "isolation": "shared", "model": nil})
			call, _ := json.Marshal(map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "subchat", "arguments": string(arguments)}})
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[%s]}}]}\n\n", call)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		default:
			answer("ok")
		}
	}))
	t.Cleanup(service.Close)
	return service.URL + "/v1"
}

// Subchats start subchats, down to 3 levels below the chat the user
// started (#398): every chat here is told to split its work off, so the
// chain grows until the level-3 subchat is refused with the limit. Each
// child's report reaches its own parent, and the reports climb to the top.
func TestNestedSubchatsChain(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()

	server.mu.Lock()
	server.approvalMode = "yolo"
	top := &serverChat{ID: "top", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: nestingModel(t), Model: "mock", Workspace: t.TempDir()}}
	server.chats = []*serverChat{top}
	server.runLocked(top, agent.Message{Role: "user", Content: "SPLIT the work"})
	server.mu.Unlock()

	// chain is the chats from the top down, each the parent of the next.
	chain := func() []*serverChat {
		out := []*serverChat{top}
		for len(out) < 10 {
			var next *serverChat
			for _, chat := range server.chats {
				if chat.Subchat != nil && chat.Subchat.Parent == out[len(out)-1].ID {
					next = chat
				}
			}
			if next == nil {
				break
			}
			out = append(out, next)
		}
		return out
	}
	waitFor(t, server, "a subchat at level 3, refused a fourth level, and every report delivered", func() bool {
		levels := chain()
		if len(levels) != 4 || len(server.chats) != 4 {
			return false
		}
		for i, chat := range levels {
			if chat.run != nil {
				return false
			}
			if i < 3 && countNotes(chat, "Subchat "+levels[i+1].ID) == 0 { // the child's report
				return false
			}
		}
		return true
	})

	server.mu.Lock()
	defer server.mu.Unlock()
	levels := chain()
	deepest := levels[3]
	if answer := agent.LastAnswer(deepest.Items); !strings.Contains(answer, "the limit of 3 levels") {
		t.Fatalf("the level-3 subchat's start: %q", answer)
	}
	for i, chat := range levels[1:3] {
		if answer := agent.LastAnswer(chat.Items); answer != "Got the report." {
			t.Fatalf("level %d's last answer: %q", i+1, answer)
		}
		if !strings.Contains(chat.Items[0].Text, "SPLIT deeper") {
			t.Fatalf("level %d's brief: %+v", i+1, chat.Items[0])
		}
	}
	// The clients see the whole tree: each summary names its own parent.
	parents := map[string]string{}
	for _, summary := range server.summariesLocked() {
		parents[summary.ID] = summary.Parent
	}
	for i := 1; i < 4; i++ {
		if parents[levels[i].ID] != levels[i-1].ID {
			t.Fatalf("summary of level %d: parent %q, want %q", i, parents[levels[i].ID], levels[i-1].ID)
		}
	}
	// /subchat from the deepest one is refused too.
	if err := server.subchatCommandLocked(deepest, "more"); err == nil {
		t.Fatal("/subchat at level 3")
	}
}

// Stopping a subchat stops the subchats under it, at any depth, and leaves
// its parent alone (#398). A subchat that is idle while one under it works
// can still be stopped.
func TestStopNestedSubchats(t *testing.T) {
	server := &headlessServer{}
	stopped := map[string]bool{}
	chat := func(id, parent string) *serverChat {
		c := &serverChat{ID: id, Title: id}
		if parent != "" {
			c.Subchat = &agent.SubchatLink{Parent: parent}
		}
		c.run = &serverRun{cancel: func() { stopped[id] = true }}
		return c
	}
	top, one, two, three, sibling := chat("top", ""), chat("one", "top"), chat("two", "one"), chat("three", "two"), chat("sibling", "top")
	server.chats = []*serverChat{three, two, one, sibling, top}

	if err := server.subchatAccess(top).Stop(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"one": true, "two": true, "three": true, "top": false, "sibling": false} {
		if stopped[id] != want {
			t.Fatalf("stopping one: %s stopped %v", id, stopped[id])
		}
	}

	// one is idle, three still works: stopping one reaches it.
	clear(stopped)
	one.run, two.run = nil, nil
	if err := server.subchatAccess(top).Stop(context.Background(), "one"); err != nil {
		t.Fatalf("stop an idle subchat with a working one under it: %v", err)
	}
	if !stopped["three"] {
		t.Fatal("the level-3 subchat kept working")
	}

	// The clients' Stop on a subchat does the same.
	clear(stopped)
	two.run = &serverRun{cancel: func() { stopped["two"] = true }}
	request := httptest.NewRequest("POST", "/api/stop", strings.NewReader(`{"chat":"one"}`))
	server.stop(httptest.NewRecorder(), request)
	if !stopped["two"] || !stopped["three"] || stopped["top"] {
		t.Fatalf("/api/stop on one: %v", stopped)
	}

	// Who may start subchats: down to level 2, not at level 3.
	for _, c := range []*serverChat{top, one, two} {
		if server.subchatAccess(c).Start == nil {
			t.Fatalf("%s cannot start subchats", c.ID)
		}
	}
	if server.subchatAccess(three).Start != nil {
		t.Fatal("the level-3 subchat can start subchats")
	}
}
