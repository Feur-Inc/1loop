package app

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
)

// ApplyThemeTo ensures that the specified canvasobject and all widgets and themeable objects will
// be updated for the current theme.
func ApplyThemeTo(content fyne.CanvasObject, canv fyne.Canvas) {
	if content == nil {
		return
	}

	switch o := content.(type) {
	case fyne.Widget:
		renderer := cache.Renderer(o)
		for _, co := range renderer.Objects() {
			ApplyThemeTo(co, canv)
		}
		renderer.Layout(content.Size()) // theme can cause sizing changes
	case *fyne.Container:
		for _, co := range o.Objects {
			ApplyThemeTo(co, canv)
		}
		if l := o.Layout; l != nil {
			l.Layout(o.Objects, o.Size()) // theme can cause sizing changes
		}
	}
	content.Refresh()
}

// ApplySettings ensures that all widgets and themeable objects in an application will be updated for the current theme.
// It also checks that scale changes are reflected if required
func ApplySettings(set fyne.Settings, app fyne.App) {
	ApplySettingsWithCallback(set, app, nil)
}

// ApplySettingsWithCallback ensures that all widgets and themeable objects in an application will be updated for the current theme.
// It also checks that scale changes are reflected if required. Also it will call `onEveryWindow` on every window
// interaction
func ApplySettingsWithCallback(set fyne.Settings, app fyne.App, onEveryWindow func(w fyne.Window)) {
	for _, window := range app.Driver().AllWindows() {
		ApplyThemeTo(window.Content(), window.Canvas())
		for _, overlay := range window.Canvas().Overlays().List() {
			ApplyThemeTo(overlay, window.Canvas())
		}
		if onEveryWindow != nil {
			onEveryWindow(window)
		}
	}
}

// VariantOnly is set while the settings announce a change of the system's
// light or dark variant alone, with the theme unchanged (1loop). The
// application's theme does not depend on Fyne's variant: when it follows
// it, it sets a new theme, which clears this; otherwise the windows have
// nothing to redraw. UI thread only.
var VariantOnly bool
