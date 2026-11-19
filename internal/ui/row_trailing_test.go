// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// In a narrowing sidebar a project's branch is cut short, then hidden,
// never drawn over the project's name; the name is cut only once the
// branch is gone (#469).
func TestRowNameBeforeBranch(t *testing.T) {
	test.NewTempApp(t)
	row := newRow("1loop", iconFolder, styleNav, nil)
	row.trailing = "feat/discord-presence"
	row.endInset = moreButtonInset()
	renderer := test.WidgetRenderer(row).(*tapRowRenderer)
	sawCut, sawHidden := false, false
	for width := float32(320); width >= 60; width -= 2 {
		row.Resize(fyne.NewSize(width, 32))
		row.Refresh()
		label, branch := renderer.label, renderer.trailing
		if branch.Text == "" {
			sawHidden = true
			continue
		}
		if label.Text != row.text {
			t.Fatalf("at %v: the name is cut (%q) while the branch shows (%q)", width, label.Text, branch.Text)
		}
		if end := label.Position().X + label.MinSize().Width; end > branch.Position().X {
			t.Fatalf("at %v: the name ends at %v, over the branch from %v", width, end, branch.Position().X)
		}
		if branch.Position().X+branch.MinSize().Width > width {
			t.Fatalf("at %v: the branch runs past the row", width)
		}
		sawCut = sawCut || strings.HasSuffix(branch.Text, "…")
	}
	if !sawCut || !sawHidden {
		t.Errorf("the branch was cut short: %v, hidden: %v", sawCut, sawHidden)
	}
}
