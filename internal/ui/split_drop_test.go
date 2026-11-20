// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

// A pane's drop zones: its sides where the pointer is within dropEdge of
// them, a corner going to the side it is deeper in, the middle otherwise.
func TestDropZoneAt(t *testing.T) {
	size := fyne.NewSize(1000, 800)
	for _, c := range []struct {
		x, y float32
		want dropZone
	}{
		{50, 400, dropLeft}, {950, 400, dropRight}, {500, 40, dropTop}, {500, 780, dropBottom},
		{500, 400, dropHere}, {320, 400, dropHere}, {290, 400, dropLeft},
		{100, 50, dropTop},    // 10 % from the left, 6 % from the top
		{50, 100, dropLeft},   // 5 % from the left, 12 % from the top
		{990, 790, dropRight}, // 1 % from the right, 1.25 % from the bottom
		{-1, 400, dropNone}, {500, 800, dropNone},
	} {
		if got := dropZoneAt(size, fyne.NewPos(c.x, c.y)); got != c.want {
			t.Errorf("at %v,%v: %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if got := dropZoneAt(fyne.NewSize(0, 0), fyne.NewPos(0, 0)); got != dropNone {
		t.Errorf("an empty pane has zone %v", got)
	}
}

// A side without room for two panes of minPaneSize offers no split: the
// drop opens the chat in the pane instead. The room counts, not the size:
// a pane moved off a side leaves its place to the pane beside it.
func TestDropTargetFits(t *testing.T) {
	narrow := fyne.NewSize(500, 800) // 500 < 2×320+6 across; 770 high under the header
	if got := dropTarget(narrow, narrow, fyne.NewPos(20, 400)); got != dropHere {
		t.Errorf("a narrow pane's left side: %v, want the middle", got)
	}
	if got := dropTarget(narrow, narrow, fyne.NewPos(250, 20)); got != dropTop {
		t.Errorf("a narrow but tall pane's top: %v, want top", got)
	}
	if got := dropTarget(narrow, fyne.NewSize(1006, 800), fyne.NewPos(20, 400)); got != dropLeft {
		t.Errorf("a narrow pane that will have the room: %v, want left", got)
	}
	short := fyne.NewSize(1000, 600) // 570 under the header
	if got := dropTarget(short, short, fyne.NewPos(500, 590)); got != dropHere {
		t.Errorf("a short pane's bottom: %v, want the middle", got)
	}
	if dropFits(short, dropNone) || !dropFits(short, dropHere) {
		t.Error("dropFits is wrong for the middle or for none")
	}
}

// The preview covers the half the new pane will take, as splitCut cuts it,
// inset from the pane's edge.
func TestDropRect(t *testing.T) {
	size := fyne.NewSize(1000, 800)
	cutX, cutY := splitCut(0.5, 1000), splitCut(0.5, 800)
	for _, c := range []struct {
		zone dropZone
		want rect
	}{
		{dropHere, rect{fyne.NewPos(6, 6), fyne.NewPos(988, 788)}},
		{dropLeft, rect{fyne.NewPos(6, 6), fyne.NewPos(cutX-12, 788)}},
		{dropRight, rect{fyne.NewPos(cutX+dividerSize+6, 6), fyne.NewPos(1000-cutX-dividerSize-12, 788)}},
		{dropTop, rect{fyne.NewPos(6, 6), fyne.NewPos(988, cutY-12)}},
		{dropBottom, rect{fyne.NewPos(6, cutY+dividerSize+6), fyne.NewPos(988, 800-cutY-dividerSize-12)}},
		{dropNone, rect{}},
	} {
		if got := dropRect(size, c.zone); got != c.want {
			t.Errorf("zone %v: %+v, want %+v", c.zone, got, c.want)
		}
	}
	if got := insetRect(rect{size: fyne.NewPos(8, 8)}, 6); got.size.X != 0 || got.size.Y != 0 {
		t.Errorf("a rectangle smaller than its inset: %+v", got)
	}
}

// Each side splits its own way, the new half where the side is.
func TestSplitSide(t *testing.T) {
	for zone, want := range map[dropZone][2]bool{
		dropLeft: {false, true}, dropRight: {false, false}, dropTop: {true, true}, dropBottom: {true, false},
	} {
		if vertical, before := splitSide(zone); vertical != want[0] || before != want[1] {
			t.Errorf("zone %v: vertical %v before %v", zone, vertical, before)
		}
	}
	if dropLabel(dropRight, false) != "Split right" || dropLabel(dropHere, false) != "Open here" ||
		dropLabel(dropTop, true) != "Move up" || dropLabel(dropHere, true) != "Swap panes" || dropLabel(dropNone, false) != "" {
		t.Error("dropLabel is wrong")
	}
}

func TestOutsideCanvas(t *testing.T) {
	size := fyne.NewSize(800, 600)
	for at, want := range map[fyne.Position]bool{
		fyne.NewPos(0, 0): false, fyne.NewPos(799, 599): false, fyne.NewPos(-1, 10): true,
		fyne.NewPos(10, -3): true, fyne.NewPos(800, 10): true, fyne.NewPos(10, 600): true,
	} {
		if got := outsideCanvas(size, at); got != want {
			t.Errorf("%v: %v", at, got)
		}
	}
}

// Moving a pane in the tree: beside another on a side, the halves even, or
// in its place (a swap). A drop where the pane is already changes nothing.
func TestMovePaneInTree(t *testing.T) {
	window, right := splitWindow(t)
	below := &desktopUI{desktopApp: window.desktopApp, owner: window}
	window.tree.find(right).splitWith(below, true, 0.3)
	// window | (right / below): below goes left of the window's pane.
	if !window.tree.move(below, window, dropLeft) {
		t.Fatal("the pane did not move")
	}
	if panes := window.panesOf(); len(panes) != 3 || panes[0] != below || panes[1] != window || panes[2] != right {
		t.Fatalf("panes = %v", panes)
	}
	if leaf := window.tree.find(below); leaf.parent.vertical || leaf.parent.ratio != 0.5 || leaf.parent.first != leaf {
		t.Fatal("the moved pane is not the left half of an even split")
	}
	// A swap keeps the shape.
	window.tree.move(right, below, dropHere)
	if panes := window.panesOf(); panes[0] != right || panes[2] != below {
		t.Fatalf("after a swap, panes = %v", panes)
	}
	// Moving the pane beside it to the side it is on already changes nothing.
	two, other := splitWindow(t)
	if !two.moveKeeps(other, two, dropRight) || two.moveKeeps(other, two, dropLeft) || two.moveKeeps(other, two, dropBottom) {
		t.Fatal("moveKeeps is wrong")
	}
	if two.tree.move(other, other, dropLeft) {
		t.Fatal("a pane moved onto itself")
	}
}

// The real thing, in a built window: a chat dragged from the sidebar onto
// the right part of the pane splits it, the preview showing on the way; the
// new pane is then dragged by its header to the left; Escape cancels a drag
// with nothing done; and the layout is saved with the chat in its pane.
func TestDragChatToSplit(t *testing.T) {
	test.NewTempApp(t)
	off := animationsOff
	animationsOff = true
	t.Cleanup(func() { animationsOff = off })
	window, _ := twoWindows(t)
	window.windows = window.windows[:1]
	window.window = test.NewWindow(nil)
	window.window.SetContent(window.build())
	window.window.Resize(fyne.NewSize(1400, 820))
	left := &conversation{ID: "l", Title: "Left"}
	right := &conversation{ID: "r", Title: "Right"}
	window.chats = []*conversation{left, right}
	window.openLocalChat(left)

	box := paneRect(window)
	at := box.pos.AddXY(box.size.X-40, box.size.Y/2)
	window.carryTo(right, nil, right.Title, at)
	if drag := window.carry; drag == nil || drag.target != window || drag.zone != dropRight {
		t.Fatalf("the drag over the pane's right part: %+v", window.carry)
	}
	if !window.drops.box.Visible() || window.drops.label.Text != "Split right" || !window.drops.ghost.Visible() {
		t.Fatal("no preview over the pane's right half")
	}
	window.dropCarried()
	if window.drops.layer.Visible() {
		t.Fatal("the preview stayed on screen after the drop")
	}
	panes := window.panesOf()
	if len(panes) != 2 || panes[0] != window || panes[1].current != right || window.current != left {
		t.Fatalf("the window did not split with the chat on the right: %v", panes)
	}
	pane := panes[1]

	// Escape cancels: nothing moves, no preview stays.
	window.carryTo(nil, pane, right.Title, paneRect(window).pos.AddXY(20, 300))
	if !window.cancelDrag() {
		t.Fatal("Escape found no drag to cancel")
	}
	window.carryTo(nil, pane, right.Title, paneRect(window).pos.AddXY(20, 300)) // the gesture goes on
	window.dropCarried()
	if panes := window.panesOf(); panes[0] != window || window.drops.layer.Visible() || window.carry != nil {
		t.Fatal("a cancelled drag did something")
	}

	// The right pane dragged by its header onto the window's left part: the
	// window's pane is narrow but has the right pane's room once it leaves.
	window.carryTo(nil, pane, right.Title, paneRect(window).pos.AddXY(20, 300))
	if drag := window.carry; drag.zone != dropLeft || window.drops.label.Text != "Move left" || !window.drops.veil.Visible() {
		t.Fatalf("the pane dragged left: zone %v, label %q", drag.zone, window.drops.label.Text)
	}
	window.dropCarried()
	if panes := window.panesOf(); len(panes) != 2 || panes[0] != pane || panes[1] != window || window.focused() != pane {
		t.Fatalf("the pane did not move left: %v", panes)
	}
	saved := window.savedLayout()
	if saved == nil || saved.Vertical || saved.First.Chat != "r" || !saved.Second.Window {
		t.Fatalf("saved layout: %+v", saved)
	}

	// The row of a chat already in a pane moves that pane: on its own pane,
	// nothing shows.
	window.carryTo(right, nil, right.Title, paneRect(pane).pos.AddXY(100, 300))
	if window.carry.target != nil || window.drops.box.Visible() {
		t.Fatal("a chat over its own pane shows a drop")
	}
	window.endCarry()
}

// A chat row dragged in the sidebar of a split window, whose sidebar the
// pane with the focus draws: it reorders over the sidebar, is carried to the
// panes once out of it (no reorder line, the row back in place), comes back,
// and a drop over the panes splits without reordering.
func TestSidebarRowToPanes(t *testing.T) {
	test.NewTempApp(t)
	off := animationsOff
	animationsOff = true
	t.Cleanup(func() { animationsOff = off })
	window, _ := twoWindows(t)
	window.windows = window.windows[:1]
	window.window = test.NewWindow(nil)
	window.window.SetContent(window.build())
	window.window.Resize(fyne.NewSize(1600, 900))
	project := t.TempDir()
	window.cfg.Workspace = project
	window.projects = []string{project}
	chats := []*conversation{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}
	for _, chat := range chats {
		chat.Config.Workspace = project
	}
	window.chats = chats
	window.openLocalChat(chats[0])
	pane := window.splitPane(chats[1], false)
	if pane == nil || window.focused() != pane {
		t.Fatal("the window did not split with the new pane focused")
	}
	window.renderChats()
	key := chatKey("c")
	kept := pane.sideRows[key]
	if kept == nil {
		t.Fatalf("the focused pane's sidebar has no row for C: %v", pane.sideRows)
	}
	row := kept.row
	home := fyne.CurrentApp().Driver().AbsolutePositionForObject(row)
	drag := func(to fyne.Position) {
		row.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: to}, Dragged: fyne.NewDelta(0, to.Y-home.Y)})
		home = to
	}
	drag(home.AddXY(0, -20)) // over the sidebar: a reorder
	if pane.sideDrag == nil || pane.sideDrag.away || !pane.sideMarker.Visible() || window.carry != nil {
		t.Fatal("a drag over the sidebar is not a reorder")
	}
	box := paneRect(window)
	drag(box.pos.AddXY(box.size.X/2, box.size.Y/2)) // the middle of the left pane
	if !pane.sideDrag.away || pane.sideMarker.Visible() || window.carry == nil || window.carry.zone != dropHere {
		t.Fatalf("the row carried to the panes: away %v, carry %+v", pane.sideDrag.away, window.carry)
	}
	// A chat's status changes: the sidebar is drawn again under the drag.
	chats[1].Title = "B renamed"
	window.renderChats()
	if again := pane.sideRows[key]; again == nil || again.object != kept.object || !slices.Contains(window.projectList.Objects, kept.object) {
		t.Fatal("the row carried was not kept through a rebuild of the sidebar")
	}
	drag(box.pos.AddXY(box.size.X/2+5, box.size.Y/2))
	if window.carry == nil || window.carry.zone != dropHere {
		t.Fatal("the drag did not go on after the rebuild")
	}
	drag(fyne.CurrentApp().Driver().AbsolutePositionForObject(row).AddXY(10, 5)) // back over the sidebar
	if pane.sideDrag.away || window.carry != nil {
		t.Fatal("the row back in the sidebar is still carried")
	}
	box = paneRect(window)
	drag(box.pos.AddXY(20, box.size.Y/2)) // the left pane's left part
	rank := len(window.sideRank)
	row.DragEnd()
	if pane.sideDrag != nil || window.carry != nil || window.drops.layer.Visible() {
		t.Fatal("the drag left something behind")
	}
	if len(window.sideRank) != rank {
		t.Fatal("a drop over the panes reordered the sidebar")
	}
	panes := window.panesOf()
	if len(panes) != 3 || panes[0].current != chats[2] || panes[1] != window {
		t.Fatalf("C did not open left of the window's pane: %v", panes)
	}
}

// While a chat or a pane is dragged, the pointer passing over a pane does
// not give it the focus.
func TestDragKeepsTheFocus(t *testing.T) {
	window, right := splitWindow(t)
	window.focus = right
	if window.dragging() {
		t.Fatal("dragging with no drag")
	}
	window.carry = &paneDrag{window: window, pane: right}
	if !right.dragging() {
		t.Fatal("a pane's drag is not seen from the window's other pane")
	}
	layer := &selectLayer{ui: window}
	layer.ExtendBaseWidget(layer)
	layer.MouseIn(&desktop.MouseEvent{})
	if window.focused() != right {
		t.Fatal("the pane under a drag took the focus")
	}
	window.carry = nil
	window.sideDrag = &sideDrag{key: "chat:x"}
	if !right.dragging() {
		t.Fatal("a sidebar row's drag is not seen")
	}
}
