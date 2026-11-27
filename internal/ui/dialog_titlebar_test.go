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

// A dialog frosts the whole window, title bar included, so nothing of the
// bar answers (the logo's switch, the sidebar's button), but draws the
// window's own buttons sharp in their place, and a drag area across the
// bar's height: the window still moves, maximizes and closes. Only the
// title bar on screen counts: Loopy's, built later but not shown, is not
// where the buttons are (its place was the logo's, cut sharp before).
func TestDialogKeepsTheWindowButtons(t *testing.T) {
	saved := titleBars
	t.Cleanup(func() { titleBars = saved })
	ui := addProjectUI(t)
	shown := windowControls(ui.window, func() {}, func() bool { return true })
	hidden := windowControls(ui.window, func() {}, func() bool { return false })
	bar := padded(8, 4, 20, 8, container.NewBorder(nil, nil, nil, shown))
	ui.window.SetContent(container.NewBorder(bar, nil, nil, nil, widget.NewLabel("chat")))
	ui.window.Resize(fyne.NewSize(900, 700))
	if got := titleBarOf(ui.window.Canvas()); got == nil || got.controls != shown {
		t.Fatalf("the title bar found is not the one on screen (hidden one: %p)", hidden)
	}
	ui.addProject()
	top := ui.window.Canvas().Overlays().Top().(*sheet)
	if top.chrome == nil {
		t.Fatal("the dialog keeps no window buttons")
	}
	buttons := top.chrome.Objects[1]
	want := fyne.CurrentApp().Driver().AbsolutePositionForObject(shown)
	if buttons.Position() != want || buttons.Size() != shown.Size() {
		t.Fatalf("the dialog's buttons at %v %v, the title bar's at %v %v", buttons.Position(), buttons.Size(), want, shown.Size())
	}
	if drag := top.chrome.Objects[0]; drag.Size().Width != 900 || drag.Size().Height < want.Y+shown.Size().Height {
		t.Fatalf("the drag area is %v, not the title bar's width and height", drag.Size())
	}
	if top.dim.Position().Y != 0 || top.dim.Size().Height != 700 {
		t.Fatal("the dim does not cover the whole window")
	}
	menu := showMenu(ui.window, nil, []menuAction{{label: "One", run: func() {}}})
	if menu.chrome != nil {
		t.Fatal("a menu keeps window buttons: a tap there should close it")
	}
}
