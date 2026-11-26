// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// Folded in Loopy, the sidebar comes back from a button in Loopy's title
// bar, as in 1loop's; switching spaces keeps it folded, with the button of
// the space shown.
func TestLoopySidebarComesBack(t *testing.T) {
	off := animationsOff
	animationsOff = true
	t.Cleanup(func() { animationsOff = off })
	window, _ := deleteTreeWindow(t)
	window.openLoopy()
	mode := window.loopy
	if mode.expand.Visible() {
		t.Fatal("the sidebar is open, yet Loopy shows the button to bring it back")
	}
	window.toggleSidebar()
	if window.root.Objects[0].Visible() || !mode.expand.Visible() {
		t.Fatalf("folded in Loopy: sidebar shown %v, button back shown %v", window.root.Objects[0].Visible(), mode.expand.Visible())
	}
	window.showSpace(false)
	if window.root.Objects[0].Visible() || !window.expand.Visible() {
		t.Fatalf("back in 1loop, folded: sidebar shown %v, button back shown %v", window.root.Objects[0].Visible(), window.expand.Visible())
	}
	window.openLoopy()
	mode.expand.Tapped(&fyne.PointEvent{})
	if !window.root.Objects[0].Visible() || mode.expand.Visible() || window.expand.Visible() {
		t.Fatalf("after the button: sidebar shown %v, buttons back shown %v, %v",
			window.root.Objects[0].Visible(), mode.expand.Visible(), window.expand.Visible())
	}
	if window.root.Objects[0] != mode.sidebar {
		t.Fatal("Loopy shows another sidebar than its own")
	}
}

// The browser Loopy uses does not open by itself: a button shows it, and
// the next session starts hidden again.
func TestLoopyBrowserHiddenUntilAsked(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	window.openLoopy()
	mode := window.loopy
	page := canvas.NewRectangle(colSurface)
	mode.showBrowser(page)
	if mode.browserPane.Visible() || !mode.browserBtn.Visible() {
		t.Fatalf("a page: pane shown %v, button shown %v", mode.browserPane.Visible(), mode.browserBtn.Visible())
	}
	mode.browserOn = true // what the button does, before it syncs the page
	mode.showBrowser(page)
	if !mode.browserPane.Visible() || !mode.browserBtn.selected {
		t.Fatalf("asked for: pane shown %v, button on %v", mode.browserPane.Visible(), mode.browserBtn.selected)
	}
	mode.showBrowser(nil)
	if mode.browserOn || mode.browserBtn.Visible() || mode.browserPane.Visible() {
		t.Fatal("no page left, yet the browser stays asked for or shown")
	}
	mode.showBrowser(page)
	if mode.browserPane.Visible() {
		t.Fatal("the next session's page opened the pane by itself")
	}
}
