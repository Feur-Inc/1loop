package ui

// The agent's browser profile: where it is kept, and how it is cleared.

import (
	"fyne.io/fyne/v2"

	"1loop/internal/browser"
)

// clearBrowserData erases what the browser keeps (browser.ClearData), off
// the window's thread.
func (ui *desktopUI) clearBrowserData(done func(error)) {
	go func() {
		err := browser.ClearData(ui.browser)
		fyne.Do(func() { done(err) })
	}()
}
