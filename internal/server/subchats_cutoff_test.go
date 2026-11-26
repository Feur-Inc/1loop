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
	"time"

	"1loop/internal/agent"
)

// A restart cuts turns off (#584): each shows so at once; a subchat whose
// harness kept its session is resumed in it, once; the chats it was to
// report to (its parent always) are told, with its last answer. Cut off
// again after that resume, it waits for a message; a turn the user stopped
// is no cut-off turn.
func TestCutOffTurnsAfterRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, dir := t.TempDir(), t.TempDir()
	server, err := newHeadlessServer(ctx, root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	chat := func(id, title string, link *agent.SubchatLink, cfg agent.Config) *serverChat {
		cfg.Workspace = root
		return &serverChat{ID: id, Title: title, Subchat: link, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}, Config: cfg,
			Items: []agent.ChatItem{{Kind: "user", Text: "work"}, {Kind: "assistant", Text: "Building " + title + " now."}}}
	}
	claude := agent.Config{Provider: "claudecode", Session: "session-1"}
	if agent.HarnessSession(claude) == "" {
		t.Fatal("the test's Claude Code configuration keeps no session")
	}
	resumable := chat("cut", "Docs", &agent.SubchatLink{Parent: "parent"}, claude)
	resumable.Working, resumable.ReportTo = true, []string{"parent"} // working for the parent when the server stopped
	user := chat("user", "Notes", &agent.SubchatLink{Parent: "parent"}, agent.Config{Provider: "compatible"})
	user.Working = true // the user's own turn, in a harness that keeps no session
	stopped := chat("stopped", "Lint", &agent.SubchatLink{Parent: "parent", Ended: agent.SubchatStopped}, claude)
	parent := chat("parent", "Main", nil, agent.Config{Provider: "compatible"})
	parent.Working = true
	server.mu.Lock()
	server.chats = []*serverChat{resumable, user, stopped, parent}
	err = server.saveLocked()
	server.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	server.close()

	again, err := newHeadlessServer(ctx, root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer again.close()
	again.mu.Lock()
	defer again.mu.Unlock()
	last := func(id string) agent.ChatItem { items := again.chatLocked(id).Items; return items[len(items)-1] }
	for id, want := range map[string]string{
		"cut":    "Interrupted: the server restarted while this chat worked. Resumed in its session.",
		"user":   "Interrupted: the server restarted while this chat worked. Send a message to continue.",
		"parent": "Interrupted: the server restarted while this chat worked. Send a message to continue.",
	} {
		if item := last(id); item.Kind != "error" || item.Text != want {
			t.Fatalf("%s shows %+v", id, item)
		}
		if c := again.chatLocked(id); c.Working || c.ReportTo != nil {
			t.Fatalf("%s: working %v, reports to %v before it goes on", id, c.Working, c.ReportTo)
		}
	}
	if item := last("stopped"); item.Kind != "assistant" || len(again.cutOffs) != 3 {
		t.Fatalf("the turn the user stopped: %+v; cut off: %d", item, len(again.cutOffs))
	}
	if link := again.chatLocked("cut").Subchat; link.Ended != agent.SubchatStopped || link.Reported != agent.SubchatInterrupted {
		t.Fatalf("the cut subchat's link %+v", link)
	}

	// The models are loaded: it goes on. The parent works meanwhile (its
	// own turn, say), so what it is told joins that turn.
	main := again.chatLocked("parent")
	main.run = &serverRun{cancel: func() {}}
	var started []string
	start := func(c *serverChat, m agent.Message) {
		started = append(started, c.ID)
		if !m.Internal || !strings.HasPrefix(m.Content, "[Resumed: the server restarted while you worked]") {
			t.Fatalf("the resume message %+v", m)
		}
	}
	again.resumeCutOffLocked(start)
	again.resumeCutOffLocked(start) // a second call resumes nothing
	if fmt.Sprint(started) != "[cut]" {
		t.Fatalf("resumed %v", started)
	}
	if cut := again.chatLocked("cut"); !cut.Resumed || fmt.Sprint(cut.ReportTo) != "[parent]" {
		t.Fatalf("the resumed subchat: resumed %v, reports to %v", cut.Resumed, cut.ReportTo)
	}
	if len(main.steer) != 2 {
		t.Fatalf("the parent was told %d times: %+v", len(main.steer), main.steer)
	}
	told := main.steer[0].Content + "\n---\n" + main.steer[1].Content
	for _, want := range []string{
		"[Subchat cut “Docs” ended its turn: interrupted]\nBuilding Docs now.",
		"1loop resumed it in its session: its next report follows.",
		"[Subchat user “Notes” ended its turn: interrupted]\nBuilding Notes now.",
		"message it (chats, id user) to continue it.",
		"Your own last turn was cut off too",
	} {
		if !strings.Contains(told, want) {
			t.Fatalf("the parent was told %q, without %q", told, want)
		}
	}

	// Cut off again before the resumed turn ended: not resumed twice.
	main.run = nil
	if err := again.saveLocked(); err != nil {
		t.Fatal(err)
	}
	third, err := newHeadlessServer(ctx, root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer third.close()
	third.mu.Lock()
	defer third.mu.Unlock()
	items := third.chatLocked("cut").Items
	if item := items[len(items)-1]; !strings.HasPrefix(item.Text, "Interrupted again: the server restarted") {
		t.Fatalf("cut off again: %+v", item)
	}
	third.chatLocked("parent").run = &serverRun{cancel: func() {}}
	started = nil
	third.resumeCutOffLocked(start)
	if len(started) != 0 {
		t.Fatalf("resumed a second time: %v", started)
	}
}

// reportModel answers "ok", fails (400) a message with FAIL, holds one with
// HOLD until release is closed and then fails it, and acknowledges a
// subchat's report.
func reportModel(t *testing.T, release chan struct{}) string {
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
		content := ""
		for i := len(request.Messages) - 1; i >= 0; i-- { // the turn's own, past 1loop's notes
			if text := fmt.Sprint(request.Messages[i].Content); request.Messages[i].Role == "user" && !strings.Contains(text, "[1loop context") {
				content = text
				break
			}
		}
		fail := func() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
		}
		answer := func(text string) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", text)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
		switch {
		case strings.Contains(content, "[Subchat "):
			answer("Got the report.")
		case strings.Contains(content, "HOLD"):
			<-release
			fail()
		case strings.Contains(content, "FAIL"):
			fail()
		default:
			answer("Done: " + content)
		}
	}))
	t.Cleanup(service.Close)
	return service.URL + "/v1"
}

// The end of every subchat turn the parent is owed reaches it once (#584):
// after a failed turn, the user's own turn in the subchat reports too, so
// that failure is not the parent's last word; a report steered into a
// parent's turn that then fails still starts a turn of the parent.
func TestSubchatReportsAreNotDropped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cfg := agent.Config{Provider: "compatible", BaseURL: reportModel(t, release), Model: "mock", Workspace: t.TempDir()}
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}, Config: cfg}
	sub := &serverChat{ID: "sub", Title: "Fix parser", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}, Config: cfg,
		Subchat: &agent.SubchatLink{Parent: "parent"}, ReportTo: []string{"parent"}}
	reports := func(state string) int { return countNotes(parent, "Subchat sub “Fix parser” ended its turn: "+state) }
	idle := func() bool { return parent.run == nil && sub.run == nil }

	server.mu.Lock()
	server.chats = []*serverChat{sub, parent}
	server.runLocked(sub, agent.Message{Role: "user", Content: "FAIL at first"})
	server.mu.Unlock()
	waitFor(t, server, "the failed turn's report", func() bool { return idle() && reports("failed") == 1 })

	// The user goes on in the subchat: its end is the parent's last word.
	server.mu.Lock()
	server.runLocked(sub, agent.Message{Role: "user", Content: "try again"})
	server.mu.Unlock()
	waitFor(t, server, "the done turn's report", func() bool { return idle() && reports("done") == 1 })
	server.mu.Lock()
	if sub.Subchat.Reported != agent.SubchatDone || reports("failed") != 1 {
		t.Fatalf("reported %q, failed reports %d", sub.Subchat.Reported, reports("failed"))
	}
	// Done heard, the user's next turn is theirs alone again.
	server.runLocked(sub, agent.Message{Role: "user", Content: "one more thing"})
	server.mu.Unlock()
	waitFor(t, server, "the user's turn", func() bool { return idle() && strings.Contains(agent.LastAnswer(sub.Items), "one more thing") })
	time.Sleep(200 * time.Millisecond)
	server.mu.Lock()
	if reports("done") != 1 || parent.run != nil {
		t.Fatalf("the user's turn reported: %+v", parent.Items)
	}

	// The parent works on a turn that will fail; the subchat ends a turn it
	// caused meanwhile: the report waits in the parent's steer.
	server.runLocked(parent, agent.Message{Role: "user", Content: "HOLD this turn"})
	sub.ReportTo = []string{"parent"}
	server.turnEndedLocked(sub, agent.SubchatDone)
	if len(parent.steer) != 1 {
		t.Fatalf("the report was not steered into the parent's turn: %+v", parent.steer)
	}
	server.mu.Unlock()
	close(release)
	waitFor(t, server, "the parent to hear the report after its failed turn", func() bool {
		return idle() && agent.LastAnswer(parent.Items) == "Got the report." && len(parent.steer) == 0
	})
	server.mu.Lock()
	defer server.mu.Unlock()
	if n := reports("done"); n != 2 {
		t.Fatalf("%d done reports: %+v", n, parent.Items)
	}
}
