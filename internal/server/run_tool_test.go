package server

import (
	"context"
	"testing"

	"1loop/internal/agent"
)

// The phone's status badge lets touches through while a phone tool runs, so
// the metadata names the executing tool even when an approval replaced the
// label, and only until its result.
func TestMetaToolFollowsExecution(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	run := &serverRun{calls: map[string]int{}, states: map[int]string{}, index: -1}
	chat := &serverChat{ID: "c", Title: "t", run: run}
	tool := func() string {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.metaLocked(chat).Tool
	}
	call := agent.ToolOutcome{ID: "1", Name: "phone_use"}
	server.emit(chat, run, "tool", call)
	server.emit(chat, run, "approval", map[string]any{"id": "a", "title": "Tap", "preview": ""})
	if got := tool(); got != "" {
		t.Fatalf("before execution: tool %q", got)
	}
	server.emit(chat, run, "tool_exec", call)
	if got := tool(); got != "phone_use" {
		t.Fatalf("executing after an approval: tool %q, want phone_use (label %q)", got, run.label)
	}
	server.emit(chat, run, "tool_result", call)
	if got := tool(); got != "" {
		t.Fatalf("after the result: tool %q", got)
	}
}
