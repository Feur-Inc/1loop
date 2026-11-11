// SPDX-License-Identifier: MPL-2.0

// Package palette holds 1loop's colours: one set of tokens per theme, dark
// and light, and the accent colour laid over them. The desktop paints with
// them, and the phone gets a copy (PaletteTokens.kt, kept in step by a
// test), so a token means the same on both.
package palette

import (
	"fmt"
	"image/color"
	"math"
	"strings"
)

// Tokens are a theme's colours by what they are for.
type Tokens struct {
	Background  color.NRGBA // the conversation, the window
	Sidebar     color.NRGBA // the sidebar and the title bar
	Surface     color.NRGBA // cards, dialogs, panels
	Popover     color.NRGBA // menus
	Raised      color.NRGBA // buttons, tooltips, headers in a card
	Field       color.NRGBA // text fields and select boxes
	Code        color.NRGBA // code blocks, terminals and diffs
	Border      color.NRGBA
	BorderSoft  color.NRGBA // separators
	BorderHot   color.NRGBA // an outlined control under the pointer
	BorderHover color.NRGBA // a field under the pointer
	PopBorder   color.NRGBA // a menu's or tooltip's edge
	Text        color.NRGBA
	TextSoft    color.NRGBA // a quieter text, still read at a glance
	Muted       color.NRGBA // secondary text
	Placeholder color.NRGBA
	Faint       color.NRGBA // hints, line numbers, disabled
	Track       color.NRGBA // a slider's or meter's empty track
	KnobOff     color.NRGBA // a disabled slider's knob
	Danger      color.NRGBA // errors, removed lines, destructive actions
	Close       color.NRGBA // a destructive button's fill, the window's close
	CloseHot    color.NRGBA
	Added       color.NRGBA // added lines, success
	DiffAdded   color.NRGBA // an added line's background
	DiffRemoved color.NRGBA // a removed line's background
	CodeInk     color.NRGBA // inline code's text
	// Overlay is laid translucent over surfaces to light them up (white on
	// dark) or shade them (black on light): hover, selection, presses.
	Overlay   color.NRGBA
	Hover     color.NRGBA
	Selected  color.NRGBA
	MenuHover color.NRGBA
	Pressed   color.NRGBA
	ScrollBar color.NRGBA
	CodeChip  color.NRGBA // inline code's chip
	CodeEdge  color.NRGBA // its outline
	GlassEdge color.NRGBA // the hairline around a pane of frosted glass
	Dark      bool
}

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

func over(c color.NRGBA, alpha uint8) color.NRGBA { c.A = alpha; return c }

// Dark is 1loop's own look, near-black greys.
var Dark = Tokens{
	Background: rgb(0x121212), Sidebar: rgb(0x0c0c0c), Surface: rgb(0x1a1a1a), Popover: rgb(0x212121),
	Raised: rgb(0x242424), Field: rgb(0x141414), Code: rgb(0x0e0e0e),
	Border: rgb(0x2a2a2a), BorderSoft: rgb(0x1e1e1e), BorderHot: rgb(0x555555), BorderHover: rgb(0x3c3c3c), PopBorder: rgb(0x343434),
	Text: rgb(0xf4f4f4), TextSoft: rgb(0xb4b4b4), Muted: rgb(0x8c8c8c), Placeholder: rgb(0x6c6c6c), Faint: rgb(0x5c5c5c),
	Track: rgb(0x333333), KnobOff: rgb(0x777777),
	Danger: rgb(0xe86a5f), Close: rgb(0xc42b1c), CloseHot: rgb(0xd93b2b), Added: rgb(0x7fc88f),
	DiffAdded: rgb(0x1c3323), DiffRemoved: rgb(0x40211d), CodeInk: rgb(0xeaeaea),
	Overlay: rgb(0xffffff), Hover: over(rgb(0xffffff), 0x0c), Selected: over(rgb(0xffffff), 0x12), MenuHover: over(rgb(0xffffff), 0x10),
	Pressed: over(rgb(0xffffff), 0x1a), ScrollBar: over(rgb(0xffffff), 0x30),
	CodeChip: over(rgb(0xffffff), 0x14), CodeEdge: over(rgb(0xffffff), 0x12), GlassEdge: over(rgb(0xffffff), 0x2a),
	Dark: true,
}

// Light is the same layout on white: shades of black instead of light,
// and darker status colours that keep their contrast on white.
var Light = Tokens{
	Background: rgb(0xffffff), Sidebar: rgb(0xf3f3f4), Surface: rgb(0xf7f7f8), Popover: rgb(0xffffff),
	Raised: rgb(0xededef), Field: rgb(0xffffff), Code: rgb(0xf5f5f6),
	Border: rgb(0xdcdce0), BorderSoft: rgb(0xe8e8eb), BorderHot: rgb(0xa2a2a8), BorderHover: rgb(0xc4c4c9), PopBorder: rgb(0xd6d6db),
	Text: rgb(0x1c1c1e), TextSoft: rgb(0x48484c), Muted: rgb(0x6b6b70), Placeholder: rgb(0x8e8e93), Faint: rgb(0x9e9ea3),
	Track: rgb(0xd8d8dc), KnobOff: rgb(0xb4b4b8),
	Danger: rgb(0xc8312a), Close: rgb(0xc42b1c), CloseHot: rgb(0xd93b2b), Added: rgb(0x1f7a3a),
	DiffAdded: rgb(0xe3f4e7), DiffRemoved: rgb(0xfbe6e3), CodeInk: rgb(0x24292f),
	Overlay: rgb(0x000000), Hover: over(rgb(0x000000), 0x0a), Selected: over(rgb(0x000000), 0x0f), MenuHover: over(rgb(0x000000), 0x0c),
	Pressed: over(rgb(0x000000), 0x16), ScrollBar: over(rgb(0x000000), 0x38),
	CodeChip: over(rgb(0x000000), 0x0d), CodeEdge: over(rgb(0x000000), 0x12), GlassEdge: over(rgb(0x000000), 0x1f),
}

// Theme settings.
const (
	ThemeSystem = "system" // follows the computer's or phone's dark mode
	ThemeDark   = "dark"
	ThemeLight  = "light"
)

// For gives the tokens of a theme setting, dark meaning what the system
// asks for when the setting follows it.
func For(theme string, systemDark bool) Tokens {
	switch theme {
	case ThemeLight:
		return Light
	case ThemeSystem:
		if !systemDark {
			return Light
		}
	}
	return Dark
}

// Accent is a preset accent colour.
type Accent struct {
	ID    string
	Label string
	Color color.NRGBA
}

// DefaultAccent is 1loop's amber.
const DefaultAccent = "amber"

// Accents are the presets, amber first.
var Accents = []Accent{
	{"amber", "Amber", rgb(0xffb000)},
	{"orange", "Orange", rgb(0xff7a1a)},
	{"pink", "Pink", rgb(0xff5fa2)},
	{"purple", "Purple", rgb(0xa87bff)},
	{"blue", "Blue", rgb(0x4c8dff)},
	{"teal", "Teal", rgb(0x2fc2a4)},
	{"green", "Green", rgb(0x5bc66b)},
}

// AccentColor reads an accent setting: a preset's id or a colour as
// #rrggbb. Anything else is amber.
func AccentColor(setting string) color.NRGBA {
	for _, accent := range Accents {
		if accent.ID == setting {
			return accent.Color
		}
	}
	if c, ok := ParseHex(setting); ok {
		return c
	}
	return Accents[0].Color
}

// ParseHex reads #rrggbb (the # and case optional).
func ParseHex(text string) (color.NRGBA, bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "#")
	var r, g, b uint8
	if len(text) != 6 {
		return color.NRGBA{}, false
	}
	if _, err := fmt.Sscanf(strings.ToLower(text), "%02x%02x%02x", &r, &g, &b); err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}, true
}

// Hex writes a colour as #rrggbb.
func Hex(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// Accented are the colours made from the accent for a theme.
type Accented struct {
	Accent    color.NRGBA // buttons, marks, highlighted text
	AccentHot color.NRGBA // the accent under the pointer
	OnAccent  color.NRGBA // text and icons on the accent
	Link      color.NRGBA // links in running text
	Focus     color.NRGBA // a focused field's ring, translucent
	Selection color.NRGBA // selected text, translucent
	Ring      color.NRGBA // an open select box's outline
}

// minContrast is what the accent keeps against the background, as text:
// WCAG's level for large text and the parts of controls.
const minContrast = 3.2

// Derive fits an accent to a theme: lightened on dark or darkened on light
// until it reads against the background, with its hover, ink and
// translucent forms.
func Derive(tokens Tokens, accent color.NRGBA) Accented {
	accent.A = 0xff
	toward := rgb(0x000000)
	if tokens.Dark {
		toward = rgb(0xffffff)
	}
	for step := 1; Contrast(accent, tokens.Background) < minContrast && step <= 20; step++ {
		accent = Mix(accent, toward, 0.08)
	}
	derived := Accented{Accent: accent}
	if tokens.Dark {
		derived.AccentHot = Mix(accent, rgb(0xffffff), 0.24)
		derived.Link = derived.AccentHot
	} else {
		derived.AccentHot = Mix(accent, rgb(0x000000), 0.12)
		derived.Link = accent
	}
	derived.OnAccent = rgb(0xffffff)
	if Contrast(accent, rgb(0x111111)) >= Contrast(accent, rgb(0xffffff)) {
		derived.OnAccent = rgb(0x111111)
	}
	derived.Focus, derived.Selection, derived.Ring = over(accent, 0x2a), over(accent, 0x40), over(accent, 0x99)
	return derived
}

// Mix goes from a to b by f, from 0 to 1, alpha included.
func Mix(a, b color.NRGBA, f float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f + 0.5) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

// Luminance is a colour's relative luminance, as WCAG defines it.
func Luminance(c color.NRGBA) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// Contrast is WCAG's contrast ratio of two colours, from 1 to 21.
func Contrast(a, b color.NRGBA) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
