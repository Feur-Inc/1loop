// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"slices"
	"testing"

	"1loop/internal/agent"
)

// nestedChats are a chat with subchats three levels down, and a second
// subchat beside the first: here › one › two › three, here › sibling.
func nestedChats() []*conversation {
	sub := func(id, title, parent string) *conversation {
		return &conversation{ID: id, Title: title, Config: agent.Config{Workspace: "/home/u/app"}, Subchat: &agent.SubchatLink{Parent: parent}}
	}
	return []*conversation{
		{ID: "here", Title: "Fix the parser", Config: agent.Config{Workspace: "/home/u/app"}},
		sub("one", "Level one", "here"),
		sub("two", "Level two", "one"),
		sub("three", "Level three", "two"),
		sub("sibling", "Beside one", "here"),
	}
}

// The sidebar shows subchats of subchats under their parent, at depth 2
// and 3 (#398), each right after its parent.
func TestSidebarShowsNestedSubchats(t *testing.T) {
	ui := sidebarUI(t)
	ui.chats = nestedChats()
	ui.subchatsShown = map[string]bool{"here": true} // four subchats: unfolded by hand (#426)
	ui.renderChats()
	listed := sidebarTexts(ui)
	at := slices.Index(listed, "Fix the parser")
	want := []string{"Fix the parser", "4 nested sessions", "Level one", "Level two", "Level three", "Beside one"}
	if at < 0 || len(listed) < at+len(want) || !slices.Equal(listed[at:at+len(want)], want) {
		t.Fatalf("sidebar rows %q, want the tree %q", listed, want)
	}

	top := nestSubchats([]sideChat{{id: "here"}, {id: "one", parent: "here"}, {id: "two", parent: "one"}, {id: "three", parent: "two"}, {id: "sibling", parent: "here"}})
	if len(top) != 1 || len(top[0].subchats) != 2 || len(top[0].subchats[0].subchats) != 1 || len(top[0].subchats[0].subchats[0].subchats) != 1 ||
		top[0].subchats[0].subchats[0].subchats[0].id != "three" {
		t.Fatalf("tree %+v", top)
	}
	// A loop of links is still listed, not lost.
	if loop := nestSubchats([]sideChat{{id: "a", parent: "b"}, {id: "b", parent: "a"}}); len(loop) == 0 {
		t.Fatal("a loop of links vanished from the sidebar")
	}
}

// Stopping a subchat on this computer stops the ones under it, not its
// parent; a subchat below level 3 is refused a start (#398).
func TestStopNestedSubchatsOnDesktop(t *testing.T) {
	ui := &desktopUI{ctx: context.Background()}
	ui.chats = nestedChats()
	stopped := map[string]bool{}
	for _, chat := range ui.chats {
		chat.run = &chatRun{cancel: func() { stopped[chat.ID] = true }}
	}
	ui.stopSubchat(ui.chatByID("one"))
	for id, want := range map[string]bool{"one": true, "two": true, "three": true, "here": false, "sibling": false} {
		if stopped[id] != want {
			t.Fatalf("stopping one: %s stopped %v", id, stopped[id])
		}
	}

	clear(stopped)
	ui.current = ui.chatByID("two") // the Stop button in the subchat itself
	ui.stopRun()
	if !stopped["two"] || !stopped["three"] || stopped["one"] {
		t.Fatalf("Stop in two: %v", stopped)
	}

	for _, id := range []string{"here", "one", "two"} {
		if ui.subchatAccess(ui.chatByID(id)).Start == nil {
			t.Fatalf("%s cannot start subchats", id)
		}
	}
	if ui.subchatAccess(ui.chatByID("three")).Start != nil {
		t.Fatal("the level-3 subchat can start subchats")
	}
	if problem := ui.splitOff(ui.chatByID("three"), "more"); problem == "" {
		t.Fatal("/subchat at level 3")
	}
}
