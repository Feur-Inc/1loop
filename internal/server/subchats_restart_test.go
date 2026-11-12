// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"testing"

	"1loop/internal/agent"
)

// A subchat is still its parent's after the server restarts: the link is
// saved with the chats and read back, for the clients' lists and the
// parent's Tasks tab (#356).
func TestSubchatLinkSurvivesRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, dir := t.TempDir(), t.TempDir()
	server, err := newHeadlessServer(ctx, root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	chat := func(id, title string, link *agent.SubchatLink) *serverChat {
		return &serverChat{ID: id, Title: title, Subchat: link, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}, Config: agent.Config{Workspace: root}}
	}
	cut := chat("cut", "Docs", &agent.SubchatLink{Parent: "parent"})
	cut.ReportTo = []string{"parent"} // its turn was working for the parent when the server stopped
	server.mu.Lock()
	server.chats = []*serverChat{
		cut,
		chat("done", "Fix parser", &agent.SubchatLink{Parent: "parent", Branch: "subchat/fix-parser", Ended: agent.SubchatDone}),
		chat("parent", "Main", nil),
	}
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
	if link := again.chatLocked("done").Subchat; link == nil || *link != (agent.SubchatLink{Parent: "parent", Branch: "subchat/fix-parser", Ended: agent.SubchatDone}) {
		t.Fatalf("link %+v", link)
	}
	if cut := again.chatLocked("cut"); cut.Subchat == nil || cut.Subchat.Ended != agent.SubchatStopped || cut.ReportTo != nil {
		t.Fatalf("a turn cut off by the restart: %+v, reports to %v", cut.Subchat, cut.ReportTo)
	}
	for _, summary := range again.summariesLocked() {
		want := map[string]string{"cut": "parent", "done": "parent"}[summary.ID]
		if summary.Parent != want {
			t.Fatalf("summary %s: parent %q", summary.ID, summary.Parent)
		}
	}
	meta := again.metaLocked(again.chatLocked("parent"))
	if len(meta.Subchats) != 2 {
		t.Fatalf("the parent's subchats: %+v", meta.Subchats)
	}
	if meta := again.metaLocked(again.chatLocked("done")); meta.Parent != "parent" || meta.ParentTitle != "Main" {
		t.Fatalf("the subchat's meta: %q %q", meta.Parent, meta.ParentTitle)
	}
}
