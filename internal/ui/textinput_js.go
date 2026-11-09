// SPDX-License-Identifier: MPL-2.0

package ui

// Text input in the browser: glfw-js types through a hidden textarea while
// a text field has the keyboard (third_party/glfw-js/textinput_wasm.go), so
// that dead keys, input methods and phone keyboards work. Fyne has no
// focus callback, so the focus is read ten times a second; the entries ever
// focused are where a tap opens a phone's keyboard at once.

import (
	"syscall/js"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	glfw "github.com/fyne-io/glfw-js"
)

// textField is a widget.Entry, or a widget that embeds one.
type textField interface {
	fyne.Widget
	fyne.Focusable
	CursorPosition() fyne.Position
	SelectedText() string
	Disabled() bool
}

// watchTextInput keeps glfw-js's textarea on the focused entry.
func (ui *desktopUI) watchTextInput() {
	touch := js.Global().Get("navigator").Get("maxTouchPoints").Int() > 0
	fields := map[textField]bool{}
	if ui.prompt != nil {
		fields[ui.prompt] = true
	}
	focusedText := ""
	js.Global().Set("loopFocusedText", js.FuncOf(func(js.Value, []js.Value) any { return focusedText }))
	go func() {
		for range time.Tick(100 * time.Millisecond) {
			fyne.Do(func() {
				c := ui.window.Canvas()
				driver := fyne.CurrentApp().Driver()
				scale := float64(c.Scale())
				rect := func(pos fyne.Position, size fyne.Size) glfw.TextRect {
					return glfw.TextRect{X: float64(pos.X) * scale, Y: float64(pos.Y) * scale,
						Width: float64(size.Width) * scale, Height: float64(size.Height) * scale}
				}
				field, active := c.Focused().(textField)
				active = active && !field.Disabled()
				focusedText = ""
				var caret glfw.TextRect
				if active {
					if touch {
						fields[field] = true
					}
					focusedText = entryText(field)
					pos := driver.AbsolutePositionForObject(field)
					size := field.Size()
					line := fyne.MeasureText("Mg", theme.TextSize(), fyne.TextStyle{}).Height
					cursor := field.CursorPosition()
					// A long text scrolls: the caret stays in the field.
					cursor.X = min(max(cursor.X, 0), size.Width)
					cursor.Y = min(max(cursor.Y, 0), max(size.Height-line, 0))
					caret = rect(pos.Add(cursor), fyne.NewSize(1, line))
				}
				glfw.SetTextInput(active, caret)
				if !touch {
					return
				}
				var targets []glfw.TextRect
				for f := range fields {
					pos := driver.AbsolutePositionForObject(f)
					if !f.Visible() || f.Disabled() || pos.IsZero() {
						if f != textField(ui.prompt) {
							delete(fields, f)
						}
						continue
					}
					targets = append(targets, rect(pos, f.Size()))
				}
				glfw.SetTextInputTargets(targets)
			})
		}
	}()
}

// entryText is the text of an entry, for the smoke run's checks
// (window.loopFocusedText).
func entryText(f textField) string {
	switch e := f.(type) {
	case *chatEntry:
		return e.Text
	case *widget.Entry:
		return e.Text
	}
	return ""
}
