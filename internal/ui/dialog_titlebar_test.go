// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Escape closes the dialog on top even while one of its fields has the
// focus, as New project's name field does (the key went to the field).
func TestEscapeClosesDialogFromItsField(t *testing.T) {
	ui := addProjectUI(t)
	ui.addProject()
	top, ok := ui.window.Canvas().Overlays().Top().(*sheet)
	if !ok {
		t.Fatal("Add project opened no dialog")
	}
	field := widget.NewEntry()
	top.content.(*fyne.Container).Add(field)
	ui.window.Canvas().Focus(field)
	if !escapeOverlay(ui.window.Canvas()) || top.shown() {
		t.Fatal("Escape left the dialog open")
	}
	if escapeOverlay(ui.window.Canvas()) {
		t.Fatal("Escape with no pop-up open claimed the key")
	}
}

// A dialog leaves the custom title bar alone, down to below its window
// buttons: the pointer reaches it (to move, maximize or close the window)
// and nothing is frosted or dimmed over it. Only the title bar on screen
// counts: Loopy's, built later but not shown, is not where the buttons are
// (its place was the logo's, cut sharp over the frosted window).
func TestDialogLeavesTheTitleBar(t *testing.T) {
	saved := titleBars
	t.Cleanup(func() { titleBars = saved })
	ui := addProjectUI(t)
	shown := windowControls(ui.window, func() {}, func() bool { return true })
	hidden := windowControls(ui.window, func() {}, func() bool { return false })
	bar := padded(8, 4, 20, 8, container.NewBorder(nil, nil, nil, shown))
	ui.window.SetContent(container.NewBorder(bar, nil, nil, nil, widget.NewLabel("chat")))
	ui.window.Resize(fyne.NewSize(900, 700))
	if got := titleBarControls(ui.window.Canvas()); got != shown {
		t.Fatalf("the title bar's buttons are %p, want the one on screen %p (not %p)", got, shown, hidden)
	}
	ui.addProject()
	top := ui.window.Canvas().Overlays().Top().(*sheet)
	bottom := shown.Position().Y + 8 + shown.Size().Height
	if top.strip < bottom || top.strip > bottom+12 {
		t.Fatalf("the dialog leaves %v of the top, the buttons end at %v", top.strip, bottom)
	}
	if !top.PassesThrough(fyne.NewPos(880, 10)) || top.PassesThrough(fyne.NewPos(880, top.strip+4)) {
		t.Fatal("the pointer does not reach the title bar alone")
	}
	if y := top.dim.Position().Y; y != top.strip {
		t.Fatalf("the dim starts at y=%v, over the title bar ending at %v", y, top.strip)
	}
	menu := showMenu(ui.window, nil, []menuAction{{label: "One", run: func() {}}})
	if menu.strip != 0 || menu.PassesThrough(fyne.NewPos(880, 10)) {
		t.Fatal("a menu leaves the title bar: a tap there should close it")
	}
}
