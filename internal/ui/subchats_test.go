// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
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
		Subchat: &agent.SubchatLink{Parent: "parent"}, ReportTo: []string{"parent"}} // working for the parent when 1loop closed
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
	if cut := after.chatByID("cut"); cut.Subchat.Ended != agent.SubchatStopped || cut.ReportTo != nil {
		t.Fatalf("a turn cut off by the restart: %+v, reports to %v", *cut.Subchat, cut.ReportTo)
	}
	if after.parentOf(after.chatByID("done")) != loaded || parentTitle(after, after.chatByID("done")) != "Main" {
		t.Fatal("the subchat does not find its parent")
	}
	if after.chatByID("parent").Subchat != nil {
		t.Fatal("the parent became a subchat")
	}
}

// The chats tool on the desktop (#357): it lists the other chats with how
// they stand to the asking one, finds parent, and says why a chat cannot be
// reached: unknown, itself, or on the 1loop server the window shows.
// Past MaxChatHops nothing is sent.
func TestChatAccessFinds(t *testing.T) {
	ui := &desktopUI{ctx: context.Background(), server: &remoteDesktop{name: "build-box", chats: []remoteapi.Chat{{ID: "far", Title: "On the server"}}}}
	parent := &conversation{ID: "parent", Title: "Main", Config: agent.Config{Workspace: "/src/app"}}
	sub := &conversation{ID: "sub", Title: "Fix parser", Config: agent.Config{Workspace: "/src/app"}, Subchat: &agent.SubchatLink{Parent: "parent"}}
	other := &conversation{ID: "other", Title: "Docs\nmore", Config: agent.Config{Workspace: "/src/docs"}}
	ui.chats = []*conversation{sub, other, parent}

	got, err := ui.findChat(sub, "parent")
	if err != nil || got != parent {
		t.Fatalf("parent: %v %v", got, err)
	}
	if info := ui.chatInfo(parent, sub); info.Relation != agent.RelationSubchat || info.State != agent.ChatIdle || info.Project != "app" {
		t.Fatalf("the subchat as its parent sees it: %+v", info)
	}
	if info := ui.chatInfo(sub, other); info.Relation != "" || info.Title != "Docs more" || info.Project != "docs" {
		t.Fatalf("another chat: %+v", info)
	}
	for id, want := range map[string]string{
		"nope":   "No chat nope on this computer",
		"sub":    "That is this chat",
		"far":    "is on the 1loop server build-box",
		"parent": "", // found
	} {
		_, err := ui.findChat(sub, id)
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Fatalf("find %s: %v", id, err)
		}
	}
	if _, err := ui.findChat(parent, "parent"); err == nil {
		t.Fatal("a chat that is no subchat found a parent")
	}
	sub.hops = agent.MaxChatHops
	if err := ui.sendToChat(sub, "other", "hi"); err == nil || !strings.Contains(err.Error(), "Not sent") {
		t.Fatalf("past the hops: %v", err)
	}
}
