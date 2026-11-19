// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"1loop/internal/palette"
)

// A markdown block drawn under one theme draws with the next one's colours
// after a switch, even when the old text and link colours were the same
// colour and so cannot be told apart by the recolouring walk (#436).
func TestFlowTextRecolouredOnSwitch(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	defer setColours(palette.Dark, palette.AccentColor(""))

	old := palette.Dark
	old.Text = color.NRGBA{0x20, 0xe0, 0x40, 0xff}
	setColours(old, color.NRGBA{0x20, 0xe0, 0x40, 0xff}) // text and accent: the same green
	flow := newFlowText(paragraphSpans(t, "plain [link](https://example.com) plain"), 14, colText)
	win := test.NewWindow(flow)
	defer win.Close()
	win.Resize(fyne.NewSize(400, 200))

	next := palette.Light
	next.Text = color.NRGBA{0x10, 0x20, 0x30, 0xff}
	blue := color.NRGBA{0x20, 0x60, 0xe0, 0xff}
	changed := recolour(setColours(next, blue))
	repaint(changed)
	flow.Refresh()

	seen := 0
	for _, object := range flow.objects {
		text, ok := object.(*canvas.Text)
		if !ok {
			continue
		}
		seen++
		want := colText
		if text.Text == "link" {
			want = colLink
		}
		if text.Color != color.Color(want) {
			t.Errorf("%q drawn %v, want %v", text.Text, text.Color, want)
		}
	}
	if seen == 0 {
		t.Fatal("nothing drawn")
	}
}
