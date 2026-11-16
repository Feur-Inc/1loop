// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"strings"
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
	ui.subchatsShown = map[string]bool{"here": true} // four subchats: unfolded by hand (#426)
	ui.renderChats()
	guides := guidesOf(ui)[1:] // after the summary row's
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
	guides = guidesOf(ui)[1:]
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

// A chat with more than three subchats, all levels counted, lists one muted
// summary row under it in place of its tree, with the failed ones counted
// and a warning at its right; clicking it unfolds the tree and clicking
// again folds it (#426). The chat keeps the branch mark with the count.
func TestSidebarFoldsManySubchats(t *testing.T) {
	ui := sidebarUI(t)
	ui.chats = nestedChats()
	ui.chatByID("three").Subchat.Ended = agent.SubchatFailed
	ui.renderChats()
	rows := func() []*tapRow {
		var out []*tapRow
		for _, object := range ui.projectList.Objects {
			if row := firstRow(object); row != nil {
				out = append(out, row)
			}
		}
		return out
	}
	find := func(text string) (*tapRow, int) {
		for i, row := range rows() {
			if row.text == text {
				return row, i
			}
		}
		return nil, -1
	}
	parent, at := find("Fix the parser")
	if parent == nil || parent.mark != iconBranch || parent.markText != "4" {
		t.Fatalf("the parent row's branch mark: %+v", parent)
	}
	summary, summaryAt := find("4 nested sessions · 1 failed")
	if summary == nil || summaryAt != at+1 {
		t.Fatalf("no summary row under the parent: %q", sidebarTexts(ui))
	}
	if summary.ink != colMuted || summary.badge != iconWarning {
		t.Errorf("summary: ink %v badge %s, want muted and the warning", summary.ink, resourceName(summary.badge))
	}
	for _, title := range []string{"Level one", "Level two", "Level three", "Beside one"} {
		if row, _ := find(title); row != nil {
			t.Errorf("%q is listed while folded", title)
		}
	}
	if guides := guidesOf(ui); len(guides) != 1 || guides[0].depth != 1 || guides[0].trunk != trunkLast {
		t.Fatalf("the line does not bend into the summary: %+v", guides)
	}

	summary.onTap() // unfolds
	if listed := sidebarTexts(ui); !slices.Contains(listed, "Level three") || !slices.Contains(listed, "4 nested sessions · 1 failed") {
		t.Fatalf("unfolded: %q", listed)
	}
	if guides := guidesOf(ui); len(guides) != 5 || guides[0].trunk != trunkThrough {
		t.Fatalf("unfolded guides: %+v", guides)
	}
	summary, _ = find("4 nested sessions · 1 failed")
	summary.onTap() // folds again
	if row, _ := find("Level one"); row != nil {
		t.Fatal("clicking again does not fold the tree")
	}

	// none failed: no failed count, no warning
	ui.chatByID("three").Subchat.Ended = agent.SubchatDone
	ui.renderChats()
	if summary, _ = find("4 nested sessions"); summary == nil || summary.badge != nil {
		t.Fatalf("with none failed: %q", sidebarTexts(ui))
	}

	// three or fewer: the tree, no summary
	ui.chats = slices.DeleteFunc(ui.chats, func(chat *conversation) bool { return chat.ID == "sibling" })
	ui.renderChats()
	if listed := sidebarTexts(ui); !slices.Contains(listed, "Level three") || slices.ContainsFunc(listed, func(text string) bool { return strings.Contains(text, "nested sessions") }) {
		t.Fatalf("three subchats: %q", listed)
	}
	if parent, _ = find("Fix the parser"); parent.markText != "3" {
		t.Errorf("parent count %q", parent.markText)
	}

	// an open subchat unfolds the tree until the user folds it
	ui.chats = nestedChats()
	ui.subchatsShown = nil
	ui.current = ui.chatByID("two")
	ui.renderChats()
	if row, _ := find("Level two"); row == nil {
		t.Fatalf("the open subchat is hidden: %q", sidebarTexts(ui))
	}
}

// The count goes down every level and counts the failed ones anywhere.
func TestNestedCount(t *testing.T) {
	failed := func(id string, subs ...sideChat) sideChat {
		return sideChat{id: id, ended: agent.SubchatFailed, subchats: subs}
	}
	tree := []sideChat{
		{id: "a", subchats: []sideChat{failed("b", failed("c")), {id: "d", running: true, ended: agent.SubchatFailed}}},
		{id: "e", ended: agent.SubchatDone},
	}
	if total, bad := nestedCount(tree); total != 5 || bad != 2 {
		t.Fatalf("nestedCount = %d, %d, want 5, 2", total, bad)
	}
	if got := nestedSummary(5, 0); got != "5 nested sessions" {
		t.Fatal(got)
	}
	if got := nestedSummary(5, 2); got != "5 nested sessions · 2 failed" {
		t.Fatal(got)
	}
}
