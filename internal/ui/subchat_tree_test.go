// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

// guidesOf are the tree guides drawn in the sidebar, top to bottom.
func guidesOf(ui *desktopUI) []*subchatGuide {
	var out []*subchatGuide
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch object := object.(type) {
		case *subchatGuide:
			out = append(out, object)
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		}
	}
	for _, object := range ui.projectList.Objects {
		walk(object)
	}
	return out
}

// Each subchat row has its connector at its own depth and its state's dot
// (#398): working amber, done green, failed red, idle grey.
func TestSidebarSubchatGuides(t *testing.T) {
	ui := sidebarUI(t)
	ui.chats = nestedChats()
	ui.chatByID("one").run = &chatRun{cancel: func() {}}
	ui.chatByID("two").Subchat.Ended = agent.SubchatDone
	ui.chatByID("three").Subchat.Ended = agent.SubchatFailed
	ui.renderChats()
	guides := guidesOf(ui)
	if len(guides) != 4 {
		t.Fatalf("%d guides", len(guides))
	}
	type want struct {
		levels []bool
		state  string
		ink    any
	}
	for i, w := range []want{
		{[]bool{true}, agent.SubchatWorking, colAmber},               // one: Beside one comes after it
		{[]bool{true, false}, agent.SubchatDone, colAdded},           // two: one's line goes on past it
		{[]bool{true, false, false}, agent.SubchatFailed, colDanger}, // three
		{[]bool{false}, "", colFaint},                                // sibling: the last, idle
	} {
		got := guides[i]
		if len(got.levels) != len(w.levels) || got.state != w.state || got.dotColor() != w.ink {
			t.Fatalf("guide %d: levels %v state %q, want %v %q", i, got.levels, got.state, w.levels, w.state)
		}
		for j := range w.levels {
			if got.levels[j] != w.levels[j] {
				t.Fatalf("guide %d: levels %v, want %v", i, got.levels, w.levels)
			}
		}
	}
	if guides[2].MinSize().Width <= guides[1].MinSize().Width || guides[1].MinSize().Width <= guides[0].MinSize().Width {
		t.Fatal("deeper subchats are not indented further")
	}
}
