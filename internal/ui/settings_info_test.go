package ui

import "testing"

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
