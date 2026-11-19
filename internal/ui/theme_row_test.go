// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// findFlow finds a text block by its first words, and where it is.
func findFlow(object fyne.CanvasObject, at fyne.Position, prefix string) (*flowText, fyne.Position) {
	at = at.Add(object.Position())
	switch object := object.(type) {
	case *flowText:
		if len(object.spans) > 0 && len(object.spans[0].text) >= len(prefix) && object.spans[0].text[:len(prefix)] == prefix {
			return object, at
		}
	case *fyne.Container:
		for _, child := range object.Objects {
			if found, where := findFlow(child, at, prefix); found != nil {
				return found, where
			}
		}
	}
	return nil, fyne.Position{}
}

func findButton(object fyne.CanvasObject, at fyne.Position, text string) (*tapRow, fyne.Position) {
	at = at.Add(object.Position())
	switch object := object.(type) {
	case *tapRow:
		if object.text == text {
			return object, at
		}
	case *fyne.Container:
		for _, child := range object.Objects {
			if found, where := findButton(child, at, text); found != nil {
				return found, where
			}
		}
	}
	return nil, fyne.Position{}
}

// The theme files' note and buttons belong to the Theme row and start
// where its title does; the card's divider follows them, before Accent
// colour (#508). They were a row of their own against the card's edge.
func TestThemeFileControlsLineUp(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui := sidebarUI(t)
	ui.window = test.NewWindow(nil)
	t.Cleanup(ui.window.Close)
	view := &settingsView{ui: ui, anchors: map[string]*settingAnchor{}}
	rows := ui.appearanceRows(view)
	page := card(rows...)
	page.Resize(fyne.NewSize(720, page.MinSize().Height))

	if button, _ := findButton(rows[0], fyne.Position{}, "New theme"); button == nil {
		t.Fatal("New theme is not in the Theme row")
	}
	_, title := findFlow(page, fyne.Position{}, "Theme")
	_, where := findFlow(page, fyne.Position{}, "Theme files are read from")
	_, button := findButton(page, fyne.Position{}, "New theme")
	if title.X == 0 || where.X != title.X || button.X != title.X {
		t.Errorf("title at x %v, note at %v, New theme at %v", title.X, where.X, button.X)
	}
}
