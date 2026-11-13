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

// The subchats look like Claude Code's tree (#419): plain text with no
// chat icon, one step further in per level, a line down the left of the
// first level that bends into its last subchat and stops there, and the
// state's dot at the right of the row: working amber, done green, failed
// red, none while idle.
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
	rows := map[string]*tapRow{}
	for _, object := range ui.projectList.Objects {
		if row := firstRow(object); row != nil {
			rows[row.text] = row
		}
	}
	type want struct {
		title string
		depth int
		trunk treeTrunk
		badge fyne.Resource
	}
	for i, w := range []want{
		{"Level one", 1, trunkThrough, iconDot},                      // Beside one comes after it
		{"Level two", 2, trunkThrough, tinted(iconDot, colAdded)},    // the line runs on past it to Beside one
		{"Level three", 3, trunkThrough, tinted(iconDot, colDanger)}, // and past it
		{"Beside one", 1, trunkLast, nil},                            // the last: the line bends into it; idle
	} {
		got, row := guides[i], rows[w.title]
		if got.depth != w.depth || got.trunk != w.trunk {
			t.Errorf("%s: depth %d trunk %d, want %d %d", w.title, got.depth, got.trunk, w.depth, w.trunk)
		}
		if row == nil {
			t.Fatalf("no row %q", w.title)
		}
		if row.icon != nil || row.badge != w.badge {
			t.Errorf("%s: icon %v badge %v, want no icon and the badge %v", w.title, resourceName(row.icon), resourceName(row.badge), resourceName(w.badge))
		}
	}
	if guides[2].MinSize().Width <= guides[1].MinSize().Width || guides[1].MinSize().Width <= guides[0].MinSize().Width {
		t.Fatal("deeper subchats are not indented further")
	}

	// under the last subchat the line has ended
	ui.chats = append(ui.chats, &conversation{ID: "late", Title: "Under the last", Config: ui.chatByID("here").Config, Subchat: &agent.SubchatLink{Parent: "sibling"}})
	ui.renderChats()
	guides = guidesOf(ui)
	if len(guides) != 5 || guides[4].depth != 2 || guides[4].trunk != trunkNone {
		t.Fatalf("a subchat under the last one: %+v", guides[len(guides)-1])
	}
}

func resourceName(resource fyne.Resource) string {
	if resource == nil {
		return "none"
	}
	return resource.Name()
}

// The line is drawn for its trunk: past the row, bending into it, or not.
func TestSubchatGuideLines(t *testing.T) {
	for _, c := range []struct {
		trunk        treeTrunk
		down, bended bool
	}{{trunkThrough, true, false}, {trunkLast, true, true}, {trunkNone, false, false}} {
		guide := newSubchatGuide(1, c.trunk)
		guide.Resize(fyne.NewSize(guide.MinSize().Width, 30))
		renderer := guide.CreateRenderer().(*subchatGuideRenderer)
		renderer.Layout(guide.Size())
		if renderer.down.Visible() != c.down || renderer.across.Visible() != c.bended {
			t.Errorf("trunk %d: down %v across %v", c.trunk, renderer.down.Visible(), renderer.across.Visible())
		}
		if c.trunk == trunkThrough && renderer.down.Position2.Y <= 30 {
			t.Error("the line does not run past the row to the next one")
		}
		if c.trunk == trunkLast && (renderer.down.Position2.Y >= 15 || renderer.across.Position2.X <= renderer.down.Position1.X) {
			t.Errorf("the line does not bend into the last subchat: %v %v", renderer.down.Position2, renderer.across.Position2)
		}
	}
}
