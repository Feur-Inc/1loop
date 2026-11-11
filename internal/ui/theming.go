// SPDX-License-Identifier: MPL-2.0

package ui

// Themes (issue #58): dark, light or the system's, with an accent colour.
// The colours live in the col… variables, which drawing code reads as it
// draws; a change of theme sets them all and then repaints what is already
// on screen, mapping every old colour found in the window to its new one.

import (
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	fynedriver "fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/palette"
	"1loop/internal/platform"
)

// themeTokens are the tokens in use, for what the col… variables do not hold.
var themeTokens = palette.Dark

// colourVars are the variables a theme sets, each with its value in tokens
// and accent.
func colourVars() []struct {
	v   *color.NRGBA
	get func(palette.Tokens, palette.Accented) color.NRGBA
} {
	type entry = struct {
		v   *color.NRGBA
		get func(palette.Tokens, palette.Accented) color.NRGBA
	}
	token := func(v *color.NRGBA, get func(palette.Tokens) color.NRGBA) entry {
		return entry{v, func(t palette.Tokens, _ palette.Accented) color.NRGBA { return get(t) }}
	}
	accent := func(v *color.NRGBA, get func(palette.Accented) color.NRGBA) entry {
		return entry{v, func(_ palette.Tokens, a palette.Accented) color.NRGBA { return get(a) }}
	}
	return []entry{
		token(&colBackground, func(t palette.Tokens) color.NRGBA { return t.Background }),
		token(&colSidebar, func(t palette.Tokens) color.NRGBA { return t.Sidebar }),
		token(&colSurface, func(t palette.Tokens) color.NRGBA { return t.Surface }),
		token(&colPopover, func(t palette.Tokens) color.NRGBA { return t.Popover }),
		token(&colRaised, func(t palette.Tokens) color.NRGBA { return t.Raised }),
		token(&colField, func(t palette.Tokens) color.NRGBA { return t.Field }),
		token(&colCode, func(t palette.Tokens) color.NRGBA { return t.Code }),
		token(&colBorder, func(t palette.Tokens) color.NRGBA { return t.Border }),
		token(&colBorderSoft, func(t palette.Tokens) color.NRGBA { return t.BorderSoft }),
		token(&colBorderHot, func(t palette.Tokens) color.NRGBA { return t.BorderHot }),
		token(&colBorderHover, func(t palette.Tokens) color.NRGBA { return t.BorderHover }),
		token(&colPopBorder, func(t palette.Tokens) color.NRGBA { return t.PopBorder }),
		token(&colText, func(t palette.Tokens) color.NRGBA { return t.Text }),
		token(&colTextSoft, func(t palette.Tokens) color.NRGBA { return t.TextSoft }),
		token(&colMuted, func(t palette.Tokens) color.NRGBA { return t.Muted }),
		token(&colPlaceholder, func(t palette.Tokens) color.NRGBA { return t.Placeholder }),
		token(&colFaint, func(t palette.Tokens) color.NRGBA { return t.Faint }),
		token(&colTrack, func(t palette.Tokens) color.NRGBA { return t.Track }),
		token(&colKnobOff, func(t palette.Tokens) color.NRGBA { return t.KnobOff }),
		token(&colDanger, func(t palette.Tokens) color.NRGBA { return t.Danger }),
		token(&colClose, func(t palette.Tokens) color.NRGBA { return t.Close }),
		token(&colCloseHot, func(t palette.Tokens) color.NRGBA { return t.CloseHot }),
		token(&colAdded, func(t palette.Tokens) color.NRGBA { return t.Added }),
		token(&colDiffAdded, func(t palette.Tokens) color.NRGBA { return t.DiffAdded }),
		token(&colDiffRemoved, func(t palette.Tokens) color.NRGBA { return t.DiffRemoved }),
		token(&colCodeInk, func(t palette.Tokens) color.NRGBA { return t.CodeInk }),
		token(&colOverlay, func(t palette.Tokens) color.NRGBA { return t.Overlay }),
		token(&colHover, func(t palette.Tokens) color.NRGBA { return t.Hover }),
		token(&colSelected, func(t palette.Tokens) color.NRGBA { return t.Selected }),
		token(&colMenuHover, func(t palette.Tokens) color.NRGBA { return t.MenuHover }),
		token(&colPressed, func(t palette.Tokens) color.NRGBA { return t.Pressed }),
		token(&colScrollBar, func(t palette.Tokens) color.NRGBA { return t.ScrollBar }),
		token(&colCodeChip, func(t palette.Tokens) color.NRGBA { return t.CodeChip }),
		token(&colCodeEdge, func(t palette.Tokens) color.NRGBA { return t.CodeEdge }),
		token(&glassEdge, func(t palette.Tokens) color.NRGBA { return t.GlassEdge }),
		accent(&colAmber, func(a palette.Accented) color.NRGBA { return a.Accent }),
		accent(&colAmberHot, func(a palette.Accented) color.NRGBA { return a.AccentHot }),
		accent(&colInk, func(a palette.Accented) color.NRGBA { return a.OnAccent }),
		accent(&colLink, func(a palette.Accented) color.NRGBA { return a.Link }),
		accent(&colFocus, func(a palette.Accented) color.NRGBA { return a.Focus }),
		accent(&colSelection, func(a palette.Accented) color.NRGBA { return a.Selection }),
		accent(&colRing, func(a palette.Accented) color.NRGBA { return a.Ring }),
	}
}

// overlay is the theme's overlay colour at an alpha: light on dark, shade
// on light.
func overlay(alpha uint8) color.NRGBA {
	c := colOverlay
	c.A = alpha
	return c
}

// setColours sets the col… variables for a theme and an accent, and gives
// what each colour that changed became.
func setColours(tokens palette.Tokens, accent color.NRGBA) map[color.NRGBA]color.NRGBA {
	accented := palette.Derive(tokens, accent)
	changed := map[color.NRGBA]color.NRGBA{}
	for _, entry := range colourVars() {
		next := entry.get(tokens, accented)
		if *entry.v != next {
			if _, taken := changed[*entry.v]; !taken {
				changed[*entry.v] = next
			}
		}
		*entry.v = next
	}
	themeTokens = tokens
	return changed
}

// recolour maps a colour of the old theme to the new one: exactly when it
// is a token, by its hue when it is a token made translucent (a wash of
// the accent, a fade of the background). Other colours stay.
type recolour map[color.NRGBA]color.NRGBA

func (changed recolour) of(c color.NRGBA) color.NRGBA {
	if next, ok := changed[c]; ok {
		return next
	}
	if c.A == 0 {
		return c
	}
	opaque := c
	opaque.A = 0xff
	if next, ok := changed[opaque]; ok {
		next.A = c.A
		return next
	}
	return c
}

func (changed recolour) any(c color.Color) color.Color {
	if n, ok := c.(color.NRGBA); ok {
		return changed.of(n)
	}
	return c
}

// repaintable is a widget that keeps colours of its own, outside what it
// draws: it maps them to the new theme's.
type repaintable interface{ repaint(recolour) }

// tintedFrom remembers what a recoloured line icon was made from, so it can
// be made again in another colour.
var tintedFrom = map[fyne.Resource]struct {
	icon fyne.Resource
	ink  color.NRGBA
}{}

// repaint maps the colours of everything in the app's windows to the new
// theme's, then has Fyne draw it all again.
func repaint(changed recolour) {
	for name, c := range tintColors {
		next := changed.of(c)
		tintColors[name] = next
		delete(tintCache, c)
		tintCache[next] = name
	}
	for _, hook := range onRepaint {
		hook()
	}
	for _, glow := range glows {
		glow.tint = changed.of(glow.tint)
	}
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	paint := func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *canvas.Rectangle:
			o.FillColor, o.StrokeColor = changed.any(o.FillColor), changed.any(o.StrokeColor)
		case *canvas.Text:
			o.Color = changed.any(o.Color)
		case *canvas.Circle:
			o.FillColor, o.StrokeColor = changed.any(o.FillColor), changed.any(o.StrokeColor)
		case *canvas.Line:
			o.StrokeColor = changed.any(o.StrokeColor)
		case *canvas.Arc:
			o.FillColor, o.StrokeColor = changed.any(o.FillColor), changed.any(o.StrokeColor)
		case *canvas.LinearGradient:
			o.StartColor, o.EndColor = changed.any(o.StartColor), changed.any(o.EndColor)
		case *canvas.Image:
			if from, ok := tintedFrom[o.Resource]; ok {
				if next := changed.of(from.ink); next != from.ink {
					o.Resource = tinted(from.icon, next)
				}
			}
		case repaintable:
			o.repaint(changed)
		}
	}
	for _, window := range app.Driver().AllWindows() {
		c := window.Canvas()
		objects := []fyne.CanvasObject{c.Content()}
		objects = append(objects, c.Overlays().List()...)
		for _, root := range objects {
			fynedriver.WalkObjects(root, paint)
		}
	}
	// Fyne's own widgets read the theme: setting it again draws every
	// window anew.
	app.Settings().SetTheme(loopTheme{theme.DefaultTheme()})
}

// themeChoice is the theme setting with the accent, as saved.
type themeChoice struct {
	theme  string // palette.Theme…
	accent string // a preset's id or #rrggbb
}

// applyTheme sets the colours for the choice, the system's dark mode read
// when it follows it, and repaints the windows when anything changed.
func (choice themeChoice) apply() {
	tokens := palette.For(choice.theme, choice.theme == palette.ThemeSystem && systemDark())
	changed := setColours(tokens, palette.AccentColor(choice.accent))
	platform.Devf("ui", "theme %s (dark %v), accent %s", choice.theme, tokens.Dark, choice.accent)
	if len(changed) > 0 {
		repaint(changed)
	}
}

// systemDark says whether the computer asks for dark windows. Fyne reads
// the XDG portal's color-scheme (GNOME, KDE and the others behind it) and
// Windows' app mode, and follows their changes; when the portal has no
// preference, GNOME's and KDE's own settings are read.
func systemDark() bool {
	app := fyne.CurrentApp()
	if app == nil {
		return true
	}
	if app.Settings().ThemeVariant() == theme.VariantDark {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	return desktopPrefersDark()
}

// desktopPrefersDark looks at the desktop's own theme: Fyne takes "no
// preference" for light, yet GNOME with a dark GTK theme, or KDE with a
// dark colour scheme, may answer that.
var desktopPrefersDark = func() bool {
	if out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output(); err == nil &&
		strings.Contains(string(out), "prefer-dark") {
		return true
	}
	if out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "gtk-theme").Output(); err == nil &&
		strings.Contains(strings.ToLower(string(out)), "dark") {
		return true
	}
	return kdeSchemeDark()
}

// kdeSchemeDark reads the window background of KDE's colour scheme.
func kdeSchemeDark() bool {
	dir, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "kdeglobals"))
	if err != nil {
		return false
	}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Colors:Window]" || !strings.HasPrefix(line, "BackgroundNormal=") {
			continue
		}
		var r, g, b uint8
		if _, err := fmt.Sscanf(strings.TrimPrefix(line, "BackgroundNormal="), "%d,%d,%d", &r, &g, &b); err == nil {
			return palette.Luminance(color.NRGBA{R: r, G: g, B: b, A: 0xff}) < 0.2
		}
	}
	return false
}

// syntaxStyle is the Chroma style code is coloured with in diffs and files.
func syntaxStyle() string {
	if themeTokens.Dark {
		return "monokai"
	}
	return "github"
}

func (row *tapRow) repaint(changed recolour) {
	row.fill, row.ink, row.hoverFill = changed.of(row.fill), changed.of(row.ink), changed.of(row.hoverFill)
}

func (flow *flowText) repaint(changed recolour) {
	flow.ink = changed.any(flow.ink)
	flow.cut = nil // its atoms keep their colours: measured again
}

func (label *fitLabel) repaint(changed recolour) { label.color = changed.any(label.color) }

func (view *outputView) repaint(changed recolour) {
	for i := range view.rows {
		view.rows[i].ink = changed.any(view.rows[i].ink)
	}
}

// onRepaint are what draws itself again on a change of theme, beyond its
// colours: artwork recoloured for the theme.
var onRepaint []func()

// glows are the accent's halos (newGlow): their tint is kept to set
// their opacity, outside the window's tree.
var glows []*shadow
