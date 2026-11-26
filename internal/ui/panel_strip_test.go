// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
)

// Tabs show by family, in the order each was opened within it (#596).
func TestStripOrderByFamily(t *testing.T) {
	tabs := []panelTab{{kind: "terminal"}, {kind: "tool"}, {kind: "files"}, {kind: "todo"}, {kind: "tool"}, {kind: "server:browser"}, {kind: "terminal"}}
	got := fmt.Sprint(stripOrder(tabs))
	if want := "[3 1 4 0 6 5 2]"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
}

// Many tabs in a narrow panel: the arrows show and scroll the strip, each
// dimmed at its end, the tab shown is scrolled into view, and the list has
// every tab.
func TestStripArrowsAndList(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	panel := window.panel
	kinds := []string{"terminal", "files", "changes", "terminal", "github", "terminal", "files", "browser", "terminal", "changes", "terminal", "terminal"}
	for i, kind := range kinds {
		panel.tabs = append(panel.tabs, panelTab{kind: kind, title: fmt.Sprintf("Tab %d", i), icon: iconFolder, content: spacer(1, 1)})
	}
	panel.choose(0)
	panel.view.Resize(fyne.NewSize(420, 600))
	panel.header.Resize(fyne.NewSize(420, 44))
	panel.header.Refresh()
	panel.syncStripArrows()
	if !panel.stripLeft.Visible() || !panel.stripRight.Visible() {
		t.Fatalf("12 tabs in 420 points: arrows shown %v %v", panel.stripLeft.Visible(), panel.stripRight.Visible())
	}
	if !panel.stripLeft.disabled || panel.stripRight.disabled {
		t.Fatalf("at the start: left disabled %v, right disabled %v", panel.stripLeft.disabled, panel.stripRight.disabled)
	}
	panel.stripRight.onTap()
	if panel.strips.Offset.X <= 0 {
		t.Fatal("the right arrow did not scroll")
	}
	if panel.stripLeft.disabled {
		t.Fatal("scrolled: the left arrow is still dimmed")
	}
	// The last tab of the strip (a terminal, the last family's last) shown:
	// it is scrolled into view.
	last := stripOrder(panel.tabs)[len(panel.tabs)-1]
	panel.choose(last)
	panel.revealActive()
	view := panel.stripViews[last]
	if x := view.Position().X + view.Size().Width - panel.strips.Offset.X; x > panel.strips.Size().Width+1 {
		t.Fatalf("the tab shown ends at %v, past the strip's %v", x, panel.strips.Size().Width)
	}
	menu := 0
	for range stripOrder(panel.tabs) {
		menu++
	}
	if menu != len(kinds) {
		t.Fatalf("the list has %d tabs, want %d", menu, len(kinds))
	}
	// Few tabs: no arrows.
	panel.tabs = panel.tabs[:1]
	panel.choose(0)
	panel.header.Resize(fyne.NewSize(700, 44))
	panel.header.Refresh()
	panel.syncStripArrows()
	if panel.stripLeft.Visible() || panel.stripRight.Visible() {
		t.Fatal("one tab fits: the arrows still show")
	}
}

// A kind opened again is numbered: Terminal, Terminal 2, Terminal 3; a
// number is not taken twice while its tab is open.
func TestNextTitle(t *testing.T) {
	panel := &toolPanel{}
	for _, want := range []string{"Terminal", "Terminal 2", "Terminal 3"} {
		got := panel.nextTitle("terminal", "Terminal")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		panel.tabs = append(panel.tabs, panelTab{kind: "terminal", title: got})
	}
	panel.tabs = panel.tabs[1:] // Terminal closed: 2 and 3 are open
	if got := panel.nextTitle("terminal", "Terminal"); got != "Terminal 4" {
		t.Fatalf("after closing the first: %q", got)
	}
}
