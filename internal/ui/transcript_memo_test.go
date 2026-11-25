// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// minSizePass runs do the way the driver lays a frame out: inside the pass
// that remembers minimum sizes (EnsureMinSize), after every object shown
// was asked for its own, children first.
func minSizePass(root fyne.CanvasObject, do func()) {
	fyne.BeginMinSizeMemo()
	defer fyne.EndMinSizeMemo()
	var ask func(fyne.CanvasObject)
	ask = func(object fyne.CanvasObject) {
		if !object.Visible() {
			return
		}
		if box, ok := object.(*fyne.Container); ok {
			for _, child := range box.Objects {
				ask(child)
			}
		}
		object.MinSize()
	}
	ask(root)
	fyne.ForgetMinSizes() // as the driver does before it resizes or lays out
	do()
}

// #497/#440: a row wrapped again at another width during a frame's pass
// measures the height of its text at the new width. The pass remembered
// the sizes from before: the row kept its old height, about 70 px too high
// at 712 px (the gaps) or too low at 400 px (rows drawn over each other).
func TestMeasureRowInAMinSizePass(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	long := strings.Repeat("words that wrap across the column ", 12)
	for _, widths := range [][2]float32{{400, 712}, {712, 400}} {
		view := newMarkdownView("A paragraph: " + long)
		row := container.NewVBox(container.NewStack(view.box), container.NewHBox(newRow("Copy", iconCopy, styleChip, nil)))
		measureRow(row, widths[0])
		var got fyne.Size
		minSizePass(row, func() { got = measureRow(row, widths[1]) })
		if need := row.MinSize().Height; abs32(got.Height-need) > 0.5 {
			t.Errorf("%.0f → %.0f px: measured %.1f high in a frame's pass, its content needs %.1f", widths[0], widths[1], got.Height, need)
		}
	}
}

// checkSlots fails when a row shown is not laid out at the height its
// content needs, or not right below the one before: what the frame that
// follows a layout draws, before anything measures it again.
func checkSlots(t *testing.T, ui *desktopUI, when string) {
	t.Helper()
	layout := ui.items
	shown := 0
	for i, row := range layout.rows {
		if !row.object.Visible() {
			continue
		}
		shown++
		if need := row.object.MinSize().Height; abs32(need-row.size.Height) > 0.5 || abs32(need-row.object.Size().Height) > 0.5 {
			t.Fatalf("%s: row %d (%s) has a slot of %.1f, is drawn %.1f high, needs %.1f", when, i, describeRow(row.object), row.size.Height, row.object.Size().Height, need)
		}
		if i > 0 && layout.rows[i-1].object.Visible() {
			above := layout.rows[i-1].object
			if want := above.Position().Y + above.Size().Height + transcriptGap; abs32(row.object.Position().Y-want) > 0.5 {
				t.Fatalf("%s: row %d at %.1f, expected at %.1f (a gap or an overlap)", when, i, row.object.Position().Y, want)
			}
		}
		checkStack(t, row.object, when)
	}
	if shown == 0 {
		t.Fatalf("%s: nothing on screen", when)
	}
}

// #497: the window resized (or the side panel opened) and the chat
// scrolled, each laid out inside a frame's pass as the driver does: every
// row on screen is as high as its text at the new width, with no gap.
func TestTranscriptResizedInAMinSizePass(t *testing.T) {
	ui, _ := perfWindow(t, 150)
	root := ui.window.Content()
	for _, width := range []float32{700, 1400, 900, 600, 1180} {
		minSizePass(root, func() { ui.window.Resize(fyne.NewSize(width, 760)) })
		checkSlots(t, ui, fmt.Sprintf("resized to %.0f", width))
	}
	// Rows out of sight kept the size of another width: scrolled to, they
	// are measured again inside the pass.
	ui.followTail = false
	bottom := ui.scroll.Content.Size().Height - ui.scroll.Size().Height
	minSizePass(root, func() { ui.window.Resize(fyne.NewSize(700, 760)) })
	for _, at := range []float32{0.5, 0, 0.8} {
		minSizePass(root, func() {
			ui.scroll.ScrollToOffset(fyne.NewPos(0, at*bottom))
			relayout(ui.transcript)
		})
		checkSlots(t, ui, fmt.Sprintf("scrolled to %.0f%% at 700", at*100))
	}
}
