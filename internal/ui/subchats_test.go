// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"path/filepath"
	"testing"

	"1loop/internal/agent"
)

// A subchat is still its parent's after 1loop restarts: the link is saved
// with the chats and read back (#356). A turn that was to report when 1loop
// closed shows stopped, and reports nothing later.
func TestSubchatLinkSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	fresh := func() *desktopUI {
		return &desktopUI{ctx: context.Background(), statePath: path, foldedProjects: map[string]bool{}, shortcutKeys: map[string]string{}}
	}
	before := fresh()
	parent := &conversation{ID: "parent", Title: "Main", Config: agent.Config{Workspace: "/src/app"}}
	done := &conversation{ID: "done", Title: "Fix parser", Config: agent.Config{Workspace: "/src/app/.worktrees/fix"},
		Subchat: &agent.SubchatLink{Parent: "parent", Branch: "subchat/fix-parser", Ended: agent.SubchatDone}}
	cut := &conversation{ID: "cut", Title: "Docs", Config: agent.Config{Workspace: "/src/app"},
		Subchat: &agent.SubchatLink{Parent: "parent", Report: true}}
	before.chats = []*conversation{cut, done, parent}
	before.persist(true)
	if before.saveError != "" {
		t.Fatal(before.saveError)
	}

	after := fresh()
	after.load()
	if len(after.chats) != 3 {
		t.Fatalf("%d chats loaded", len(after.chats))
	}
	loaded := after.chatByID("parent")
	subs := after.subchatsOf(loaded)
	if len(subs) != 2 || subs[0].ID != "cut" || subs[1].ID != "done" {
		t.Fatalf("subchats of the parent after a restart: %d", len(subs))
	}
	if link := *after.chatByID("done").Subchat; link != (agent.SubchatLink{Parent: "parent", Branch: "subchat/fix-parser", Ended: agent.SubchatDone}) {
		t.Fatalf("link %+v", link)
	}
	if link := *after.chatByID("cut").Subchat; link.Report || link.Ended != agent.SubchatStopped {
		t.Fatalf("a turn cut off by the restart: %+v", link)
	}
	if after.parentOf(after.chatByID("done")) != loaded || parentTitle(after, after.chatByID("done")) != "Main" {
		t.Fatal("the subchat does not find its parent")
	}
	if after.chatByID("parent").Subchat != nil {
		t.Fatal("the parent became a subchat")
	}
}
