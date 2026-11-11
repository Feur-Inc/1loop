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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
)

// subchatModel is a model that splits work off when told SPLIT, answers
// in a subchat, and acknowledges a subchat's report.
func subchatModel(t *testing.T) string {
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
		case strings.Contains(string(raw), "This chat is a subchat"):
			answer("Fixed in the subchat: " + content)
		case last.Role == "tool":
			answer("Started it.")
		case strings.Contains(content, "SPLIT"):
			arguments, _ := json.Marshal(map[string]any{"action": "start", "id": nil, "title": "Fix parser", "brief": "Fix parse.go", "text": nil,
				"context": nil, "isolation": "worktree", "model": nil})
			call, _ := json.Marshal(map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "subchat", "arguments": string(arguments)}})
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[%s]}}]}\n\n", call)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case strings.Contains(content, "[Subchat "):
			answer("Got the report.")
		default:
			answer("ok")
		}
	}))
	t.Cleanup(service.Close)
	return service.URL + "/v1"
}

// waitFor polls the server's state, under its lock, until ok.
func waitFor(t *testing.T, server *headlessServer, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		server.mu.Lock()
		done := ok()
		server.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			server.mu.Lock()
			var state []string
			for _, chat := range server.chats {
				state = append(state, fmt.Sprintf("%s %q running %v link %+v items %+v", chat.ID, chat.Title, chat.run != nil, chat.Subchat, chat.Items))
			}
			server.mu.Unlock()
			t.Fatalf("timed out waiting for %s:\n%s", what, strings.Join(state, "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countNotes(chat *serverChat, prefix string) int {
	n := 0
	for _, item := range chat.Items {
		if item.Kind == "note" && strings.HasPrefix(item.Text, prefix) {
			n++
		}
	}
	return n
}

// A model starts a subchat: it runs in its own worktree, shows linked to
// its parent, and reports back when its turn ends, which wakes the parent.
// A turn the user drives in the subchat reports nothing; a parent's message
// does.
func TestSubchatLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	project := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "start"}} {
		command := exec.Command("git", append([]string{"-C", project, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	project, _ = filepath.EvalSymlinks(project)

	server.mu.Lock()
	server.approvalMode = "yolo" // the start is not asked: the approval card has its own tests
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: subchatModel(t), Model: "mock", Workspace: project}}
	server.chats = []*serverChat{parent}
	server.runLocked(parent, agent.Message{Role: "user", Content: "SPLIT the parser fix off"})
	server.mu.Unlock()

	var sub *serverChat
	waitFor(t, server, "the report to reach the parent", func() bool {
		for _, chat := range server.chats {
			if chat.Subchat != nil {
				sub = chat
			}
		}
		return sub != nil && parent.run == nil && sub.run == nil && agent.LastAnswer(parent.Items) == "Got the report."
	})

	server.mu.Lock()
	if sub.Subchat.Parent != "parent" || sub.Subchat.Branch != "subchat/fix-parser" || sub.Subchat.Ended != agent.SubchatDone || sub.Subchat.Report {
		t.Fatalf("link %+v", *sub.Subchat)
	}
	if !strings.HasPrefix(sub.Config.Workspace, filepath.Join(project, ".worktrees")) {
		t.Fatalf("the subchat works in %s", sub.Config.Workspace)
	}
	if _, err := os.Stat(sub.Config.Workspace); err != nil {
		t.Fatalf("its worktree: %v", err)
	}
	if sub.Items[0].Kind != "user" || sub.Items[0].Text != "Fix parse.go" || agent.LastAnswer(sub.Items) != "Fixed in the subchat: Fix parse.go" {
		t.Fatalf("subchat items %+v", sub.Items)
	}
	if countNotes(parent, "Subchat "+sub.ID+" “Fix parser” ended its turn: done") != 1 {
		t.Fatalf("parent items %+v", parent.Items)
	}
	// Linked both ways for the clients: the list, the parent's meta, the subchat's.
	for _, summary := range server.summariesLocked() {
		if summary.ID == sub.ID && summary.Parent != "parent" {
			t.Fatalf("summary %+v", summary)
		}
	}
	meta := server.metaLocked(parent)
	if len(meta.Subchats) != 1 || meta.Subchats[0].ID != sub.ID || meta.Subchats[0].State != agent.SubchatDone || meta.Subchats[0].Branch != "subchat/fix-parser" {
		t.Fatalf("parent meta subchats %+v", meta.Subchats)
	}
	if meta := server.metaLocked(sub); meta.Parent != "parent" || meta.ParentTitle != "Main" {
		t.Fatalf("subchat meta: %q %q", meta.Parent, meta.ParentTitle)
	}

	// The user talks in the subchat: the parent is not woken.
	server.runLocked(sub, agent.Message{Role: "user", Content: "also the docs"})
	server.mu.Unlock()
	waitFor(t, server, "the user's turn", func() bool { return sub.run == nil && strings.Contains(agent.LastAnswer(sub.Items), "also the docs") })
	time.Sleep(200 * time.Millisecond)
	server.mu.Lock()
	if countNotes(parent, "Subchat ") != 1 || parent.run != nil {
		t.Fatalf("the user's turn reported: %+v", parent.Items)
	}

	// The parent's message: the subchat answers, and reports again.
	access := server.subchatAccess(parent)
	server.mu.Unlock()
	if err := access.Message(ctx, sub.ID, "check the edge cases"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, server, "the second report", func() bool {
		return parent.run == nil && sub.run == nil && countNotes(parent, "Subchat ") == 2
	})
	server.mu.Lock()
	last := sub.Items[len(sub.Items)-1].Text
	server.mu.Unlock()
	if !strings.Contains(last, "[From the parent conversation] check the edge cases") {
		t.Fatalf("the subchat's last answer: %q", last)
	}
	if list := access.List(ctx); len(list) != 1 || list[0].State != agent.SubchatDone {
		t.Fatalf("list %+v", list)
	}

	// /subchat from a subchat is refused; a subchat's tool has no start.
	server.mu.Lock()
	if err := server.subchatCommandLocked(sub, "more"); err == nil {
		t.Fatal("/subchat in a subchat")
	}
	subAccess := server.subchatAccess(sub)
	server.mu.Unlock()
	if subAccess.Start != nil || subAccess.ParentTitle != "Main" {
		t.Fatalf("subchat access: start %v, parent %q", subAccess.Start != nil, subAccess.ParentTitle)
	}

	// Stopping a subchat that is not working says so (#325); an unknown
	// model is refused before any approval (#330).
	if err := access.Stop(ctx, sub.ID); err == nil || !strings.Contains(err.Error(), "is not working (done)") {
		t.Fatalf("stop a done subchat: %v", err)
	}
	if _, err := access.Model(ctx, "no-such-model-xyz"); err == nil || !strings.Contains(err.Error(), "No model") {
		t.Fatalf("an unknown model: %v", err)
	}

	// Deleting it can remove its worktree and branch (#328).
	server.mu.Lock()
	folder := sub.Config.Workspace
	branch := ""
	for _, summary := range server.summariesLocked() {
		if summary.ID == sub.ID {
			branch = summary.Branch
		}
	}
	server.mu.Unlock()
	if branch != "subchat/fix-parser" {
		t.Fatalf("the summary's branch: %q", branch)
	}
	request := httptest.NewRequest("POST", "/api/chats/"+sub.ID+"/delete", strings.NewReader(`{"worktree":true}`))
	request.SetPathValue("id", sub.ID)
	response := httptest.NewRecorder()
	server.deleteChat(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), "note") {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	if _, err := os.Stat(folder); err == nil {
		t.Fatal("the subchat's worktree is still there")
	}
	if out, _ := exec.Command("git", "-C", project, "branch", "--list", branch).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the branch is still there: %s", out)
	}
	server.mu.Lock()
	_, kept := server.worktrees[folder]
	server.mu.Unlock()
	if kept {
		t.Fatal("the server still lists the worktree")
	}
}
