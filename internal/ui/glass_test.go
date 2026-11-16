// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// The fade above the composer shows while more of the conversation lies
// below, and not at its end, in a short chat or on the welcome screen.
func TestFooterFadeFollowsTheEnd(t *testing.T) {
	test.NewTempApp(t)
	tall := canvas.NewRectangle(nil)
	tall.SetMinSize(fyne.NewSize(300, 2000))
	ui := &desktopUI{desktopApp: &desktopApp{}, footerGlass: newFooterGlass()}
	ui.scroll = newKineticVScroll(container.NewVBox(tall))
	window := test.NewTempWindow(t, ui.scroll)
	window.Resize(fyne.NewSize(300, 500))
	ui.scroll.Resize(fyne.NewSize(300, 500))
	fade := ui.footerGlass.fade
	if fade.Visible() {
		t.Fatal("the fade shows before any conversation does")
	}

	ui.scroll.ScrollToOffset(fyne.NewPos(0, 600))
	ui.syncFooterFade()
	if !fade.Visible() {
		t.Fatal("no fade with 900 points of conversation below")
	}
	// The strips go from clear to the background: the last one hides the text.
	first := fade.Objects[0].(*canvas.Rectangle).FillColor.(color.NRGBA)
	last := fade.Objects[len(fade.Objects)-1].(*canvas.Rectangle).FillColor.(color.NRGBA)
	if first.A > 0x10 || last.A < 0xf0 || last.R != colBackground.R {
		t.Fatalf("the fade goes from alpha %#x to %#x, want clear to opaque", first.A, last.A)
	}

	ui.scroll.ScrollToBottom()
	ui.syncFooterFade()
	if fade.Visible() {
		t.Fatal("the fade stays at the conversation's end")
	}

	ui.scroll.ScrollToOffset(fyne.NewPos(0, ui.scroll.Offset.Y-1)) // within followTail's tolerance
	ui.syncFooterFade()
	if fade.Visible() {
		t.Fatal("the fade shows one point from the end")
	}

	ui.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	ui.syncFooterFade()
	if !fade.Visible() {
		t.Fatal("no fade back at the top")
	}
	ui.scroll.Hide() // the welcome screen
	ui.syncFooterFade()
	if fade.Visible() {
		t.Fatal("the fade shows over the welcome screen")
	}
	ui.scroll.Show()

	tall.SetMinSize(fyne.NewSize(300, 200)) // a chat shorter than the window
	ui.scroll.Content.Resize(fyne.NewSize(300, 500))
	ui.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	ui.syncFooterFade()
	if fade.Visible() {
		t.Fatal("the fade shows under a chat shorter than the window")
	}
}
