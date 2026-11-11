// SPDX-License-Identifier: MPL-2.0

package ui

// The Appearance settings: the theme and the accent colour (theming.go
// paints them).

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/palette"
	registry "1loop/internal/settings"
)

// followTheme applies the saved theme, and again whenever the system's dark
// mode changes while the theme follows it.
func (ui *desktopUI) followTheme() {
	ui.theme.apply()
	fyne.CurrentApp().Settings().AddListener(func(fyne.Settings) {
		if ui.theme.theme == palette.ThemeSystem {
			ui.theme.apply()
		}
	})
}

// themeSetting is the theme as the settings list names it.
func (choice themeChoice) themeSetting() string {
	switch choice.theme {
	case palette.ThemeLight, palette.ThemeSystem:
		return choice.theme
	}
	return palette.ThemeDark
}

// accentSetting is the accent as the settings list names it: a preset, or
// custom for a colour of the user's own.
func (choice themeChoice) accentSetting() string {
	if choice.accent == "" {
		return palette.DefaultAccent
	}
	for _, accent := range palette.Accents {
		if accent.ID == choice.accent {
			return accent.ID
		}
	}
	return "custom"
}

func (ui *desktopUI) setTheme(theme string) {
	if theme == ui.theme.themeSetting() {
		return
	}
	ui.theme.theme = theme
	ui.themeChanged()
}

// setAccent takes a preset's id or #rrggbb. "custom" keeps the colour of
// the user's own that is in use, or the one last typed.
func (ui *desktopUI) setAccent(accent string) {
	if accent == "custom" {
		if ui.theme.accentSetting() == "custom" {
			return
		}
		accent = palette.Hex(colAmber)
	}
	if c, ok := palette.ParseHex(accent); ok {
		accent = palette.Hex(c)
	}
	if accent == ui.theme.accent {
		return
	}
	ui.theme.accent = accent
	ui.themeChanged()
}

func (ui *desktopUI) themeChanged() {
	ui.theme.apply()
	ui.save()
	// Built from the colours as they were: the page and the sidebar are
	// made again with the new ones.
	ui.renderChats()
	if view := ui.prefs; view != nil && view.at(view.page) {
		offset := view.scroll.Offset // where the user was: the rows they just used
		view.showNow(view.page)
		view.scroll.Offset = offset
		view.scroll.Refresh()
	}
}

// appearanceRows are the theme's and the accent's rows of the General page.
func (ui *desktopUI) appearanceRows(view *settingsView) []fyne.CanvasObject {
	themes, _ := registry.Lookup(registry.KeyTheme)
	themePicker := newPicker(ui.window, themes.Title, styleField, nil)
	for _, value := range themes.Values {
		themePicker.Options = append(themePicker.Options, value.Label)
	}
	themePicker.SetSelected(themes.Label(ui.theme.themeSetting()))
	themePicker.onChanged = func(label string) {
		for _, value := range themes.Values {
			if value.Label == label {
				ui.setTheme(value.ID)
			}
		}
	}
	themeRow := view.mark(registry.KeyTheme, settingRow(themes.Title,
		"Dark is 1loop's own look. System follows this computer's dark mode, and changes with it.", sized(180, themePicker.row)))

	accents, _ := registry.Lookup(registry.KeyAccent)
	current := ui.theme.accentSetting()
	swatches := container.NewHBox()
	for _, accent := range palette.Accents {
		id := accent.ID
		swatches.Add(newSwatch(accent.Color, current == id, func() { ui.setAccent(id) }))
	}
	custom := widget.NewEntry()
	custom.SetPlaceHolder("#rrggbb")
	if current == "custom" {
		custom.SetText(ui.theme.accent)
	}
	custom.OnSubmitted = func(text string) {
		if c, ok := palette.ParseHex(text); ok {
			ui.setAccent(palette.Hex(c))
		}
	}
	custom.Validator = func(text string) error {
		if _, ok := palette.ParseHex(text); !ok && strings.TrimSpace(text) != "" {
			return errBadColour
		}
		return nil
	}
	customSwatch := newSwatch(palette.AccentColor(ui.theme.accent), current == "custom", func() {
		if c, ok := palette.ParseHex(custom.Text); ok {
			ui.setAccent(palette.Hex(c))
		} else {
			ui.window.Canvas().Focus(custom)
		}
	})
	customSwatch.blank = current != "custom" // no colour of the user's own yet
	controls := container.NewHBox(swatches, padded(0, 0, 10, 0, container.NewCenter(customSwatch)), padded(0, 0, 6, 0, sized(110, custom)))
	accentRow := view.mark(registry.KeyAccent, settingStack(accents.Title,
		"Buttons, links and highlights. A colour of your own: type it as #rrggbb and press Enter. On the light theme it is darkened until it reads on white.", controls))
	return []fyne.CanvasObject{themeRow, accentRow}
}

type colourError string

func (err colourError) Error() string { return string(err) }

const errBadColour = colourError("a colour as #rrggbb")

// swatch is a round colour to choose, ringed when it is the one chosen.
type swatch struct {
	widget.BaseWidget
	colour   color.NRGBA
	blank    bool // drawn in the raised grey instead
	selected bool
	onTap    func()
}

func newSwatch(c color.NRGBA, selected bool, onTap func()) *swatch {
	s := &swatch{colour: c, selected: selected, onTap: onTap}
	s.ExtendBaseWidget(s)
	return s
}

func (s *swatch) Tapped(*fyne.PointEvent) { s.onTap() }

func (s *swatch) CreateRenderer() fyne.WidgetRenderer {
	ring, dot := canvas.NewCircle(color.Transparent), canvas.NewCircle(s.colour)
	r := &swatchRenderer{swatch: s, ring: ring, dot: dot}
	r.Refresh()
	return r
}

type swatchRenderer struct {
	swatch    *swatch
	ring, dot *canvas.Circle
}

const swatchSize = 30

func (r *swatchRenderer) Layout(size fyne.Size) {
	r.ring.Resize(fyne.NewSquareSize(swatchSize))
	r.ring.Move(fyne.NewPos((size.Width-swatchSize)/2, (size.Height-swatchSize)/2))
	r.dot.Resize(fyne.NewSquareSize(swatchSize - 10))
	r.dot.Move(fyne.NewPos((size.Width-swatchSize)/2+5, (size.Height-swatchSize)/2+5))
}

func (r *swatchRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(swatchSize + 4) }

func (r *swatchRenderer) Refresh() {
	r.dot.FillColor = r.swatch.colour
	if r.swatch.blank {
		r.dot.FillColor = colRaised
	}
	r.ring.StrokeWidth, r.ring.StrokeColor = 2, color.Transparent
	if r.swatch.selected {
		r.ring.StrokeColor = colText
	}
	r.ring.Refresh()
	r.dot.Refresh()
}

func (r *swatchRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.ring, r.dot} }
func (r *swatchRenderer) Destroy()                     {}
