package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// A long hint wraps and asks the window for no room: on one line in a
// layer as big as its widest child, it made the window thousands of
// points wide.
func TestLongTipKeepsTheWindow(t *testing.T) {
	tips := newTipLayer()
	window := test.NewTempWindow(t, tips.layer)
	window.Resize(fyne.NewSize(800, 600))
	anchor := textLine("Close to tray", 14, colText)
	tips.place(anchor, strings.Repeat("A longer explanation of what this setting does. ", 20))
	if size := tips.layer.MinSize(); size.Width > 0 || size.Height > 0 {
		t.Errorf("the hint layer asks for %v", size)
	}
	for _, line := range tips.lines {
		if width := line.MinSize().Width; width > tipWidth+1 {
			t.Errorf("a hint line is %.0f wide: %q", width, line.Text)
		}
	}
	if len(tips.lines) < 2 {
		t.Errorf("the long hint stayed on %d line", len(tips.lines))
	}
}

// A title with an (i) stays on one line: in a box, a wrapping text got its
// narrowest width and broke after every word ("Close / to / tray").
func TestTitleInfoKeepsOneLine(t *testing.T) {
	for _, title := range []string{"Close to tray", "Discord status", "Antigravity CLI on this computer"} {
		size := titleInfo(newTipLayer(), title, "A longer explanation.").MinSize()
		line := textLine(title, 14, colText).MinSize()
		if size.Width < line.Width {
			t.Errorf("%q is %v, one line of it is %v", title, size, line)
		}
	}
}
