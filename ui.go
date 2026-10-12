package main

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// One neutral palette shared by every surface, popup and dialog.
var (
	colBackground = color.NRGBA{R: 0x12, G: 0x12, B: 0x12, A: 0xff}
	colSidebar    = color.NRGBA{R: 0x0c, G: 0x0c, B: 0x0c, A: 0xff}
	colSurface    = color.NRGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff}
	colPopover    = color.NRGBA{R: 0x21, G: 0x21, B: 0x21, A: 0xff}
	colRaised     = color.NRGBA{R: 0x24, G: 0x24, B: 0x24, A: 0xff}
	colField      = color.NRGBA{R: 0x14, G: 0x14, B: 0x14, A: 0xff}
	colCode       = color.NRGBA{R: 0x0e, G: 0x0e, B: 0x0e, A: 0xff}
	colBorder     = color.NRGBA{R: 0x2a, G: 0x2a, B: 0x2a, A: 0xff}
	colBorderSoft = color.NRGBA{R: 0x1e, G: 0x1e, B: 0x1e, A: 0xff}
	colBorderHot  = color.NRGBA{R: 0x55, G: 0x55, B: 0x55, A: 0xff}
	colPopBorder  = color.NRGBA{R: 0x34, G: 0x34, B: 0x34, A: 0xff}
	colText       = color.NRGBA{R: 0xf4, G: 0xf4, B: 0xf4, A: 0xff}
	colMuted      = color.NRGBA{R: 0x8c, G: 0x8c, B: 0x8c, A: 0xff}
	colFaint      = color.NRGBA{R: 0x5c, G: 0x5c, B: 0x5c, A: 0xff}
	colAmber      = color.NRGBA{R: 0xff, G: 0xb0, B: 0x00, A: 0xff}
	colAmberHot   = color.NRGBA{R: 0xff, G: 0xc4, B: 0x3d, A: 0xff}
	colInk        = color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff}
	colHover      = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0c}
	colSelected   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}
	colMenuHover  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x10}
	colDanger     = color.NRGBA{R: 0xe8, G: 0x6a, B: 0x5f, A: 0xff}
	colClose      = color.NRGBA{R: 0xc4, G: 0x2b, B: 0x1c, A: 0xff}
	colAdded      = color.NRGBA{R: 0x7f, G: 0xc8, B: 0x8f, A: 0xff}
)

// Sizes are CSS-like pixels: the window scale follows the desktop (Xft.dpi),
// so 13 here renders like 13px text in a browser on the same screen.
type loopTheme struct{ fyne.Theme }

func (t loopTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	if c, ok := tintColors[name]; ok {
		return c
	}
	switch name {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNameButton:
		return colRaised
	case theme.ColorNameDisabledButton:
		return colSurface
	case theme.ColorNameDisabled:
		return colFaint
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x6c, G: 0x6c, B: 0x6c, A: 0xff}
	case theme.ColorNameInputBackground:
		return colField
	case theme.ColorNameInputBorder:
		return colBorder
	case theme.ColorNameMenuBackground:
		return colPopover
	case theme.ColorNameOverlayBackground:
		return colSurface
	case theme.ColorNameHeaderBackground:
		return colSidebar
	case theme.ColorNamePrimary, theme.ColorNameHyperlink, theme.ColorNameWarning:
		return colAmber
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnWarning,
		theme.ColorNameForegroundOnError, theme.ColorNameForegroundOnSuccess:
		return colInk
	case theme.ColorNameError:
		return colDanger
	case theme.ColorNameSuccess:
		return colAdded
	case theme.ColorNameHover:
		return colHover
	case theme.ColorNamePressed:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x1a}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 0xff, G: 0xb0, B: 0x00, A: 0x2a}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0xff, G: 0xb0, B: 0x00, A: 0x40}
	case theme.ColorNameSeparator:
		return colBorderSoft
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x30}
	case theme.ColorNameScrollBarBackground:
		return color.Transparent
	case theme.ColorNameShadow:
		return color.NRGBA{A: 0x30} // scroll edge fades; dialog dims are drawn by our sheets
	}
	return t.Theme.Color(name, theme.VariantDark)
}

// Inter is the face Codex and GNOME use; it is read from the system when
// installed (Debian: fonts-inter) and Fyne's bundled font is kept otherwise.
var interFaces = func() map[string]fyne.Resource {
	faces := map[string]fyne.Resource{}
	for key, file := range map[string]string{"regular": "Inter-Regular.otf", "bold": "Inter-SemiBold.otf",
		"italic": "Inter-Italic.otf", "boldItalic": "Inter-SemiBoldItalic.otf"} {
		for _, dir := range []string{"/usr/share/fonts/opentype/inter/", "/usr/share/fonts/inter/", "/usr/local/share/fonts/"} {
			if data, err := os.ReadFile(dir + file); err == nil {
				faces[key] = fyne.NewStaticResource(file, data)
				break
			}
		}
	}
	return faces
}()

func (t loopTheme) Font(style fyne.TextStyle) fyne.Resource {
	key := "regular"
	switch {
	case style.Monospace, style.Symbol:
		return t.Theme.Font(style)
	case style.Bold && style.Italic:
		key = "boldItalic"
	case style.Bold:
		key = "bold"
	case style.Italic:
		key = "italic"
	}
	if face, ok := interFaces[key]; ok {
		return face
	}
	return t.Theme.Font(style)
}

func (t loopTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNameHeadingText:
		return 18
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameLineSpacing:
		return 4
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius:
		return 8
	case theme.SizeNameSelectionRadius:
		return 3
	case theme.SizeNameScrollBar:
		return 6
	case theme.SizeNameScrollBarSmall:
		return 3
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return t.Theme.Size(name)
}

// The composer edits text directly on its surface, without a nested field box.
type bareInputTheme struct{ fyne.Theme }

func (t bareInputTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameInputBackground || name == theme.ColorNameInputBorder {
		return color.Transparent
	}
	return t.Theme.Color(name, v)
}

func mix(a, b color.NRGBA, f float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*f + 0.5) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

func textLine(text string, size float32, c color.Color) *canvas.Text {
	t := canvas.NewText(text, c)
	t.TextSize = size
	return t
}

func spacer(width, height float32) *canvas.Rectangle {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(width, height))
	return r
}

func padded(top, bottom, left, right float32, object fyne.CanvasObject) *fyne.Container {
	return container.New(layout.NewCustomPaddedLayout(top, bottom, left, right), object)
}

// relayout places a container's objects again without redrawing them.
// Container.Refresh refreshes every descendant, which on the conversation
// means parsing and measuring all its text: far too slow for an animation.
func relayout(c *fyne.Container) {
	c.Layout.Layout(c.Objects, c.Size())
}

// surface draws a rounded, finely bordered card behind content.
func surface(content fyne.CanvasObject, fill, stroke color.Color, radius, pad float32) *fyne.Container {
	background := canvas.NewRectangle(fill)
	background.CornerRadius = radius
	if stroke != nil {
		background.StrokeColor = stroke
		background.StrokeWidth = 1
	}
	return container.NewStack(background, padded(pad, pad, pad, pad, content))
}

func fitText(text string, size float32, style fyne.TextStyle, width float32) string {
	if width <= 0 || (len(text) <= 600 && fyne.MeasureText(text, size, style).Width <= width) {
		return text
	}
	runes := []rune(text)
	if len(runes) > 600 { // no row is that wide: never measure a whole one-line script
		runes = runes[:600]
	}
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if fyne.MeasureText(string(runes[:mid])+"…", size, style).Width <= width {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low]) + "…"
}

// ---------------------------------------------------------------------------
// Thin line icons (UI glyphs, not brand artwork). currentColor is replaced by
// the exact ink of the row that shows them.

func lineIcon(name, body string) fyne.Resource {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`
	return fyne.NewStaticResource("1loop-"+name+".svg", []byte(svg))
}

var (
	iconChevron      = lineIcon("chevron", `<path d="M4.5 6.5 8 10l3.5-3.5"/>`)
	iconChevronLeft  = lineIcon("chevron-left", `<path d="M9.5 4.5 6 8l3.5 3.5"/>`)
	iconSliders      = lineIcon("sliders", `<path d="M2.5 5h6M11.5 5h2M2.5 11h2M7.5 11h6"/><circle cx="10" cy="5" r="1.5"/><circle cx="6" cy="11" r="1.5"/>`)
	iconPlug         = lineIcon("plug", `<path d="M6 2.5v3M10 2.5v3M4.5 5.5h7v2.2a3.5 3.5 0 0 1-7 0zM8 11.2v2.3"/>`)
	iconPhone        = lineIcon("phone", `<rect x="4.5" y="2" width="7" height="12" rx="1.6"/><path d="M7.2 11.8h1.6"/>`)
	iconChevronRight = lineIcon("chevron-right", `<path d="M6.5 4.5 10 8l-3.5 3.5"/>`)
	iconChevronUp    = lineIcon("chevron-up", `<path d="M4.5 9.5 8 6l3.5 3.5"/>`)
	iconCheck        = lineIcon("check", `<path d="M3.5 8.4 6.6 11.4 12.5 4.8"/>`)
	iconMinimize     = lineIcon("minimize", `<path d="M4 8h8"/>`)
	iconMaximize     = lineIcon("maximize", `<rect x="4" y="4" width="8" height="8" rx="1.2"/>`)
	iconClose        = lineIcon("close", `<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>`)
	iconSidebar      = lineIcon("sidebar", `<rect x="2.5" y="3" width="11" height="10" rx="2"/><path d="M6.5 3v10"/>`)
	iconMore         = lineIcon("more", `<path d="M3.2 8h.01M8 8h.01M12.8 8h.01" stroke-width="3.1"/>`)
	iconPlus         = lineIcon("plus", `<path d="M8 3v10M3 8h10" stroke-width="1.8"/>`)
	iconCopy         = lineIcon("copy", `<rect x="5.5" y="5.5" width="7.5" height="7.5" rx="1.6"/><path d="M10.5 5.5V4.2c0-.7-.5-1.2-1.2-1.2H4.2C3.5 3 3 3.5 3 4.2v5.1c0 .7.5 1.2 1.2 1.2h1.3"/>`)
	iconTrash        = lineIcon("trash", `<path d="M3 4.5h10M6.5 4.5V3.2h3v1.3M4.5 4.5l.6 8.3h5.8l.6-8.3M7 7v3.6M9 7v3.6"/>`)
	iconRename       = lineIcon("rename", `<path d="M9.8 3.4l2.8 2.8-6.8 6.8H3v-2.8z"/>`)
	iconFolder       = lineIcon("folder", `<path d="M2.5 5c0-.8.6-1.5 1.4-1.5h2.7l1.3 1.4h4.2c.8 0 1.4.6 1.4 1.4v4.9c0 .8-.6 1.3-1.4 1.3H3.9c-.8 0-1.4-.5-1.4-1.3z"/>`)
	iconSend         = lineIcon("send", `<path d="M8 13.2V2.9M3.5 7.4 8 2.9l4.5 4.5" stroke-width="2"/>`)
	iconStop         = fyne.NewStaticResource("1loop-stop.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect x="4.5" y="4.5" width="7" height="7" rx="1.4" fill="currentColor"/></svg>`))
	iconShield       = lineIcon("shield", `<path d="M8 2.2l4.6 1.8v3.5c0 3-2 5.1-4.6 6.3-2.6-1.2-4.6-3.3-4.6-6.3V4z"/>`)
	iconBolt         = lineIcon("bolt", `<path d="M8.9 1.8 3.7 9h4.1l-.7 5.2L12.3 7H8.2z"/>`)
	iconShieldCheck  = lineIcon("shield-check", `<path d="M8 2.2l4.6 1.8v3.5c0 3-2 5.1-4.6 6.3-2.6-1.2-4.6-3.3-4.6-6.3V4z"/><path d="M5.9 7.8 7.4 9.3l2.8-3"/>`)
	iconPanel        = lineIcon("panel", `<rect x="2.5" y="3" width="11" height="10" rx="2"/><path d="M9.5 3v10"/>`)
	iconTerminal     = lineIcon("terminal", `<path d="M3.5 4.5 6.5 7.5 3.5 10.5M8 11.5h4.5"/>`)
	iconDocument     = lineIcon("document", `<path d="M4 2.8h5l3 3v7.4H4z"/><path d="M9 2.8v3h3M6 8.6h4M6 10.9h4"/>`)
	iconPencil       = lineIcon("pencil", `<path d="M3.2 12.8l.7-3 6.6-6.6 2.3 2.3-6.6 6.6zM9.2 4.5l2.3 2.3"/>`)
	iconArrowTurn    = lineIcon("arrow-turn", `<path d="M4 3.5v3.7c0 1.1.9 2 2 2h6M9.6 6.8 12 9.2l-2.4 2.4"/>`)
	iconDot          = lineIcon("dot", `<circle cx="8" cy="8" r="3.2" fill="currentColor" stroke="none"/>`)
	iconCompact      = lineIcon("compact", `<path d="M8 2.5v4M5.8 4.6 8 6.6l2.2-2M8 13.5v-4M5.8 11.4 8 9.4l2.2 2M3 8h10"/>`)
)

// tinted recolours an icon to an exact colour: line icons by substitution,
// theme icons through a private theme colour name.
var (
	tintCache  = map[color.NRGBA]fyne.ThemeColorName{}
	tintColors = map[fyne.ThemeColorName]color.NRGBA{}
	lineCache  = map[string]fyne.Resource{}
)

func tinted(icon fyne.Resource, c color.NRGBA) fyne.Resource {
	if strings.HasPrefix(icon.Name(), "1loop-") {
		hex := fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
		key := icon.Name() + hex
		if res, ok := lineCache[key]; ok {
			return res
		}
		body := strings.ReplaceAll(string(icon.Content()), "currentColor", hex)
		res := fyne.NewStaticResource(key+".svg", []byte(body))
		lineCache[key] = res
		return res
	}
	name, ok := tintCache[c]
	if !ok {
		name = fyne.ThemeColorName(fmt.Sprintf("1loop-tint-%d", len(tintCache)))
		tintCache[c] = name
		tintColors[name] = c
	}
	return theme.NewColoredResource(icon, name)
}

// ---------------------------------------------------------------------------
// tapRow: the one interactive element used for navigation, chips and buttons.

type rowStyle int

const (
	styleNav     rowStyle = iota // sidebar entry, flat until hovered
	styleChip                    // muted inline control, brightens on hover
	styleOutline                 // pill outlined like the DA demo buttons
	stylePrimary                 // amber call to action
	styleIcon                    // round icon-only control
	styleMenu                    // item inside a dropdown menu
	styleField                   // select box shaped like an input
	styleWindow                  // title bar window control
	styleModel                   // rows in the three-column model selector
)

type tapRow struct {
	widget.BaseWidget
	text      string
	detail    string  // optional second line (menu items)
	endInset  float32 // space reserved for an overlaid row action
	trailing  string
	icon      fyne.Resource
	style     rowStyle
	onTap     func()
	selected  bool
	disabled  bool
	accent    bool
	center    bool
	iconAfter bool
	size      float32
	fill      color.NRGBA
	ink       color.NRGBA
	hoverFill color.NRGBA
	danger    bool          // destructive action: red ink or red fill
	onHover   func(bool)    // optional: reports pointer enter and leave
	onMenu    func()        // optional: right click opens a context menu
	iconPx    float32       // optional icon size (styleIcon), 0 = style default
	padPx     float32       // optional padding around the icon (styleIcon), 0 = style default
	badge     fyne.Resource // optional mark on the far edge (styleMenu: the active choice)
	truncate  bool          // fills its column: truncates instead of forcing it wider
	hover     float32
	alpha     float32 // whole-row opacity, used by menu fades
	anim      *fyne.Animation
}

func newRow(text string, icon fyne.Resource, style rowStyle, onTap func()) *tapRow {
	r := &tapRow{text: text, icon: icon, style: style, onTap: onTap, size: 14, alpha: 1}
	switch style {
	case styleOutline, stylePrimary:
		r.center = true
	case styleChip:
		r.size = 13
	case styleField, styleMenu:
		r.iconAfter = true
	}
	r.ExtendBaseWidget(r)
	return r
}

func newIconButton(icon fyne.Resource, fill, ink color.NRGBA, onTap func()) *tapRow {
	r := newRow("", icon, styleIcon, onTap)
	r.fill, r.ink = fill, ink
	return r
}

func (r *tapRow) SetText(text string)       { r.text = text; r.Refresh() }
func (r *tapRow) SetTrailing(text string)   { r.trailing = text; r.Refresh() }
func (r *tapRow) SetSelected(selected bool) { r.selected = selected; r.Refresh() }
func (r *tapRow) Disable()                  { r.disabled = true; r.setHover(0, false); r.Refresh() }
func (r *tapRow) Enable()                   { r.disabled = false; r.Refresh() }

func (r *tapRow) Tapped(*fyne.PointEvent) {
	if !r.disabled && r.onTap != nil {
		r.onTap()
	}
}

// Rows are mouse targets of their own, so a title bar behind them never
// mistakes a click on a control for the start of a window move.
func (r *tapRow) MouseDown(*desktop.MouseEvent) {}
func (r *tapRow) MouseUp(*desktop.MouseEvent)   {}

func (r *tapRow) Cursor() desktop.Cursor {
	if r.disabled || r.onTap == nil {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (r *tapRow) MouseIn(*desktop.MouseEvent) {
	if !r.disabled && r.onTap != nil {
		r.setHover(1, true)
	}
	if r.onHover != nil {
		r.onHover(true)
	}
}
func (r *tapRow) MouseMoved(*desktop.MouseEvent) {}
func (r *tapRow) MouseOut() {
	r.setHover(0, true)
	if r.onHover != nil {
		r.onHover(false)
	}
}

func (r *tapRow) TappedSecondary(*fyne.PointEvent) {
	if !r.disabled && r.onMenu != nil {
		r.onMenu()
	}
}

// setHover eases the hover factor so colours fade like CSS transitions.
func (r *tapRow) setHover(target float32, animate bool) {
	if r.anim != nil {
		r.anim.Stop()
		r.anim = nil
	}
	if !animate {
		r.hover = target
		return
	}
	from := r.hover
	r.anim = fyne.NewAnimation(130*time.Millisecond, func(p float32) {
		r.hover = from + (target-from)*p
		r.Refresh()
	})
	r.anim.Curve = fyne.AnimationEaseOut
	r.anim.Start()
}

func (r *tapRow) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	label := canvas.NewText(r.text, colText)
	label.TextSize = r.size
	trailing := canvas.NewText("", colFaint)
	trailing.TextSize = r.size - 1
	icon := canvas.NewImageFromResource(nil)
	icon.FillMode = canvas.ImageFillContain
	detail := canvas.NewText("", colMuted)
	detail.TextSize = 12
	badge := canvas.NewImageFromResource(nil)
	badge.FillMode = canvas.ImageFillContain
	rr := &tapRowRenderer{row: r, bg: bg, label: label, trailing: trailing, icon: icon, detail: detail, badge: badge}
	rr.Refresh()
	return rr
}

type tapRowRenderer struct {
	row      *tapRow
	bg       *canvas.Rectangle
	label    *canvas.Text
	trailing *canvas.Text
	detail   *canvas.Text
	icon     *canvas.Image
	badge    *canvas.Image
	tint     color.NRGBA
	source   fyne.Resource
	marked   fyne.Resource
}

func (rr *tapRowRenderer) metrics() (padX, padY, iconSize, gap float32) {
	switch rr.row.style {
	case styleNav:
		return 10, 7, 18, 10
	case styleChip:
		return 7, 4, 16, 7
	case styleIcon:
		icon, pad := float32(18), float32(7)
		if rr.row.iconPx > 0 {
			icon = rr.row.iconPx
		}
		if rr.row.padPx > 0 {
			pad = rr.row.padPx
		}
		return pad, pad, icon, 0
	case styleMenu:
		return 10, 8, 18, 11
	case styleModel:
		return 9, 7, 26, 10
	case styleField:
		return 10, 7, 16, 8
	case styleWindow:
		return 8, 6, 18, 0
	}
	return 16, 7, 16, 8
}

func (rr *tapRowRenderer) MinSize() fyne.Size {
	padX, padY, iconSize, gap := rr.metrics()
	textHeight := fyne.MeasureText("Ag", rr.row.size, fyne.TextStyle{}).Height
	if rr.row.style == styleIcon || rr.row.style == styleWindow {
		return fyne.NewSize(iconSize+2*padX, iconSize+2*padY)
	}
	height := fyne.Max(textHeight, iconSize) + 2*padY
	if rr.row.detail != "" {
		height += fyne.MeasureText("Ag", 12, fyne.TextStyle{}).Height + 1
	}
	width := 2 * padX
	if rr.row.icon != nil {
		width += iconSize
		if rr.row.text != "" {
			width += gap
		}
	}
	if rr.row.style == styleNav || rr.row.style == styleField || rr.row.truncate {
		// Rows that fill a column truncate instead of forcing it wider.
		return fyne.NewSize(width+40, height)
	}
	width += fyne.Max(fyne.MeasureText(rr.row.text, rr.row.size, fyne.TextStyle{}).Width,
		fyne.MeasureText(rr.row.detail, 12, fyne.TextStyle{}).Width) + 2
	if rr.row.trailing != "" {
		width += gap + fyne.MeasureText(rr.row.trailing, rr.row.size-1, fyne.TextStyle{}).Width
	}
	if rr.row.badge != nil {
		width += gap + iconSize
	}
	return fyne.NewSize(width, height)
}

func (rr *tapRowRenderer) Layout(size fyne.Size) {
	padX, _, iconSize, gap := rr.metrics()
	rr.bg.Resize(size)
	rr.bg.Move(fyne.NewPos(0, 0))
	switch rr.row.style {
	case styleOutline, styleIcon:
		rr.bg.CornerRadius = size.Height / 2
	case styleWindow:
		rr.bg.CornerRadius = 6
	case stylePrimary, styleField:
		rr.bg.CornerRadius = 8
	case styleModel:
		rr.bg.CornerRadius = 9
	default:
		rr.bg.CornerRadius = 6
	}
	if rr.row.style == styleIcon || rr.row.style == styleWindow {
		rr.icon.Resize(fyne.NewSquareSize(iconSize))
		rr.icon.Move(fyne.NewPos((size.Width-iconSize)/2, (size.Height-iconSize)/2))
		return
	}
	trailingWidth := float32(0)
	if rr.row.badge != nil {
		rr.badge.Resize(fyne.NewSquareSize(iconSize))
		rr.badge.Move(fyne.NewPos(size.Width-padX-iconSize-rr.row.endInset, (size.Height-iconSize)/2))
		trailingWidth = iconSize + gap
	}
	if rr.row.trailing != "" {
		width := rr.trailing.MinSize().Width
		rr.trailing.Move(fyne.NewPos(size.Width-padX-trailingWidth-width, (size.Height-rr.trailing.MinSize().Height)/2))
		trailingWidth += width + gap
	}
	iconRoom := float32(0)
	if rr.row.icon != nil {
		iconRoom = iconSize
		if rr.row.text != "" {
			iconRoom += gap
		}
	}
	room := size.Width - 2*padX - iconRoom - trailingWidth - rr.row.endInset
	rr.label.Text = fitText(rr.row.text, rr.row.size, fyne.TextStyle{}, room)
	textWidth := rr.label.MinSize().Width
	textY := (size.Height - rr.label.MinSize().Height) / 2
	if rr.row.detail != "" {
		detailHeight := rr.detail.MinSize().Height + 1
		textY = (size.Height - rr.label.MinSize().Height - detailHeight) / 2
		rr.detail.Text = fitText(rr.row.detail, 12, fyne.TextStyle{}, room)
	}
	iconY := (size.Height - iconSize) / 2
	x := padX
	if rr.row.center {
		x = (size.Width - iconRoom - textWidth) / 2
	}
	if rr.row.icon != nil {
		rr.icon.Resize(fyne.NewSquareSize(iconSize))
		switch {
		case !rr.row.iconAfter:
			rr.icon.Move(fyne.NewPos(x, iconY))
			x += iconRoom
		case rr.row.style == styleField || rr.row.style == styleMenu:
			rr.icon.Move(fyne.NewPos(size.Width-padX-iconSize, iconY)) // chevron and check sit on the far edge
		default:
			rr.icon.Move(fyne.NewPos(x+textWidth+gap, iconY))
		}
	}
	rr.label.Move(fyne.NewPos(x, textY))
	// The second line starts under the label, past any leading icon.
	rr.detail.Move(fyne.NewPos(x, textY+rr.label.MinSize().Height+1))
}

func (rr *tapRowRenderer) colorsAt(h float32) (fill, stroke, ink color.NRGBA) {
	r := rr.row
	clear := color.NRGBA{}
	switch r.style {
	case styleNav:
		fill = mix(clear, colHover, h)
		if r.selected {
			fill = mix(colSelected, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}, h)
		}
		ink = colText
		if !r.selected && r.accent {
			ink = mix(color.NRGBA{R: 0xb4, G: 0xb4, B: 0xb4, A: 0xff}, colText, h)
		}
	case styleChip:
		fill = mix(clear, colHover, h)
		ink = mix(colMuted, colText, h)
		if r.selected {
			ink = colText
		}
		if r.accent {
			ink = colAmber
		}
		if r.danger {
			ink = colDanger
		}
	case styleOutline:
		stroke = mix(colBorder, colBorderHot, h)
		ink = mix(colMuted, colText, h)
	case stylePrimary:
		fill = mix(colAmber, colAmberHot, h)
		ink = colInk
		if r.danger {
			fill = mix(colClose, color.NRGBA{R: 0xd9, G: 0x3b, B: 0x2b, A: 0xff}, h)
			ink = colText
		}
	case styleIcon:
		hot := mix(r.fill, colText, 0.22)
		if r.fill.A < 0xff { // a translucent wash only deepens
			hot = r.fill
			hot.A = uint8(fyne.Min(255, float32(r.fill.A)*2+10))
		}
		fill = mix(r.fill, hot, h)
		if r.fill.A == 0 {
			fill = mix(clear, colHover, h)
		}
		// Controls without an ink of their own brighten on hover like the
		// chips beside them; the others keep their fixed ink.
		ink = r.ink
		if r.ink == (color.NRGBA{}) {
			ink = mix(colMuted, colText, h)
		}
	case styleMenu:
		base := clear
		if r.selected {
			base = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x09}
		}
		fill = mix(base, colMenuHover, h)
		ink = colText
		if r.danger {
			ink = colDanger
		}
	case styleModel:
		fill = mix(clear, colRaised, h)
		ink = mix(colMuted, colText, h)
		if r.selected {
			ink = colText
		}
	case styleField:
		fill = colField
		stroke = mix(colBorder, color.NRGBA{R: 0x3c, G: 0x3c, B: 0x3c, A: 0xff}, h)
		if r.selected { // open: focus ring like a web select
			stroke = color.NRGBA{R: 0xff, G: 0xb0, B: 0x00, A: 0x99}
		}
		ink = colText
	case styleWindow:
		target := colHover
		if r.hoverFill.A != 0 {
			target = r.hoverFill
		}
		fill = mix(clear, target, h)
		ink = mix(colMuted, colText, h)
	}
	if r.disabled {
		ink = colFaint
		if r.style == styleModel {
			fill = clear
		}
		if r.style == stylePrimary || r.style == styleIcon && r.fill.A != 0 {
			fill = colRaised
		}
	}
	return
}

func quantize(v, steps float32) float32 { return float32(int(v*steps+0.5)) / steps }

// Refresh animates shapes continuously but moves text and icon colours in a
// few steps: Fyne caches one text texture per colour, so a smooth colour
// tween would rasterise new glyph textures on every frame.
func (rr *tapRowRenderer) Refresh() {
	r := rr.row
	fill, stroke, _ := rr.colorsAt(r.hover)
	_, _, ink := rr.colorsAt(quantize(r.hover, 4))
	shape := func(c color.NRGBA) color.NRGBA { c.A = uint8(float32(c.A) * r.alpha); return c }
	textAlpha := quantize(r.alpha, 5)
	text := func(c color.NRGBA) color.NRGBA { c.A = uint8(float32(c.A) * textAlpha); return c }
	rr.bg.FillColor = shape(fill)
	rr.bg.StrokeColor = shape(stroke)
	rr.bg.StrokeWidth = 0
	if stroke.A != 0 {
		rr.bg.StrokeWidth = 1
	}
	rr.label.TextSize = r.size
	rr.trailing.Text = r.trailing
	rr.trailing.TextSize = r.size - 1
	rr.trailing.Color = text(colFaint)
	rr.detail.Text = r.detail
	rr.detail.Color = text(colMuted)
	if r.style == styleModel {
		rr.detail.Color = text(colFaint)
	}
	rr.label.Color = text(ink)
	iconInk := ink
	if r.style == styleField || r.style == styleChip && !r.accent && r.iconAfter {
		iconInk = colMuted
	}
	if r.icon != nil && (rr.source != r.icon || rr.tint != iconInk) {
		rr.source, rr.tint = r.icon, iconInk
		rr.icon.Resource = tinted(r.icon, iconInk)
		rr.icon.Refresh()
	}
	if r.badge != nil && rr.marked != r.badge {
		rr.marked = r.badge
		rr.badge.Resource = tinted(r.badge, colAmber)
		rr.badge.Refresh()
	}
	// Translucency is read at draw time; refreshing the image would only
	// throw its texture away.
	iconAlpha := r.alpha
	if r.disabled && r.style == styleModel {
		iconAlpha *= 0.4
	}
	rr.icon.Translucency = float64(1 - iconAlpha)
	rr.badge.Translucency = rr.icon.Translucency
	rr.Layout(r.Size())
	rr.bg.Refresh()
	rr.label.Refresh()
	rr.trailing.Refresh()
	rr.detail.Refresh()
}

func (rr *tapRowRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{rr.bg, rr.label, rr.trailing, rr.detail}
	if rr.row.icon != nil {
		objects = append(objects, rr.icon)
	}
	if rr.row.badge != nil {
		objects = append(objects, rr.badge)
	}
	return objects
}

func (rr *tapRowRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// Soft drop shadow: stacked translucent rounded rectangles stand in for blur.

type shadow struct {
	layers []*canvas.Rectangle
	alphas []float32
}

func newShadow(strength float32) *shadow {
	s := &shadow{}
	for i, alpha := range []float32{0.10, 0.08, 0.06, 0.04} {
		r := canvas.NewRectangle(color.NRGBA{A: uint8(255 * alpha * strength)})
		r.CornerRadius = float32(12 + 3*i)
		s.layers = append(s.layers, r)
		s.alphas = append(s.alphas, alpha*strength)
	}
	return s
}

func (s *shadow) setOpacity(o float32) {
	for i, r := range s.layers {
		r.FillColor = color.NRGBA{A: uint8(255 * fyne.Min(1, s.alphas[i]) * o)}
	}
}

func (s *shadow) layout(pos fyne.Position, size fyne.Size, radius float32) {
	for i, r := range s.layers {
		spread := float32(3 * (i + 1))
		r.CornerRadius = radius + spread
		r.Move(pos.AddXY(-spread, -spread+spread/2))
		r.Resize(size.AddWidthHeight(2*spread, 2*spread))
	}
}

func (s *shadow) objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, len(s.layers))
	for i := range s.layers {
		objects[len(s.layers)-1-i] = s.layers[i]
	}
	return objects
}

func (s *shadow) refresh() {
	for _, r := range s.layers {
		r.Refresh()
	}
}

// ---------------------------------------------------------------------------
// sheet: native overlay used for dialogs and menus. Menus behave like a web
// popover: the whole surface fades and slides in, fades out on close, and
// can be driven from the keyboard.

type sheet struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	width      float32
	anchor     fyne.CanvasObject
	anchorAt   fyne.Position // anchor geometry captured when the menu opens:
	anchorSz   fyne.Size     // the trigger may move or hide while it is open
	menu       bool
	alignLeft  bool
	columns    bool // the model selector uses the HTML demo's surface and radius
	onDismiss  func()
	onClosed   func()
	afterClose func()                    // runs once the sheet is gone (menu actions)
	onKey      func(*fyne.KeyEvent) bool // optional: content-specific keys, true when handled
	canvas     fyne.Canvas
	progress   float32
	anim       *fyne.Animation
	dim        *canvas.Rectangle
	panel      *canvas.Rectangle
	shadow     *shadow
	open       bool
	closing    bool
	items      []*tapRow
	active     int
}

func newSheet(c fyne.Canvas, content fyne.CanvasObject, width float32) *sheet {
	s := &sheet{canvas: c, content: content, width: width, active: -1}
	s.ExtendBaseWidget(s)
	return s
}

func (s *sheet) animate(from, to float32, duration time.Duration, curve fyne.AnimationCurve, done func()) {
	if s.anim != nil {
		s.anim.Stop()
	}
	s.anim = fyne.NewAnimation(duration, func(p float32) {
		s.progress = from + (to-from)*p
		s.Refresh()
		if p >= 1 && done != nil {
			done()
		}
	})
	s.anim.Curve = curve
	s.anim.Start()
}

func (s *sheet) Show() {
	if s.open {
		return
	}
	s.open, s.closing = true, false
	s.progress = 0
	if s.anchor != nil {
		s.anchorAt = fyne.CurrentApp().Driver().AbsolutePositionForObject(s.anchor)
		s.anchorSz = s.anchor.Size()
	}
	s.Resize(s.canvas.Size())
	s.canvas.Overlays().Add(s)
	s.Refresh()
	duration := 200 * time.Millisecond
	if s.menu {
		duration = 170 * time.Millisecond
	}
	// Start on the next loop turn: the first frame of a new overlay builds its
	// textures, and timing that frame would swallow the start of the motion.
	go fyne.Do(func() {
		if s.open && !s.closing {
			s.animate(0, 1, duration, easeOutQuint, nil)
		}
	})
}

// easeOutQuint matches the snappy deceleration of web UI popovers.
func easeOutQuint(x float32) float32 {
	x = 1 - x
	return 1 - x*x*x*x*x
}

// Hide removes the sheet at once (dialogs whose content changes the view).
func (s *sheet) Hide() {
	if !s.open {
		return
	}
	s.open = false
	if s.anim != nil {
		s.anim.Stop()
	}
	// Fyne's OverlayStack.Remove also drops every overlay above the removed
	// one (a confirmation opened from a menu, say), so put those back.
	stack := s.canvas.Overlays()
	var above []fyne.CanvasObject
	found := false
	for _, o := range stack.List() {
		if found {
			above = append(above, o)
		}
		found = found || o == s
	}
	stack.Remove(s)
	for _, o := range above {
		stack.Add(o)
	}
	if s.onClosed != nil {
		s.onClosed()
	}
	if after := s.afterClose; after != nil {
		s.afterClose = nil
		after()
	}
}

// close fades the sheet out before removing it, like a web popover.
func (s *sheet) close() {
	if !s.open || s.closing {
		return
	}
	s.closing = true
	s.animate(s.progress, 0, 120*time.Millisecond, fyne.AnimationEaseIn, s.Hide)
}

// dismiss closes the sheet as a cancellation (outside tap or Escape).
func (s *sheet) dismiss() {
	if s.menu {
		s.close()
	} else {
		s.Hide()
	}
	if s.onDismiss != nil {
		s.onDismiss()
	}
}

func (s *sheet) Tapped(e *fyne.PointEvent) {
	if s.menu && !s.panelRect().contains(e.Position) {
		s.dismiss()
	}
}

func (s *sheet) TappedSecondary(*fyne.PointEvent) {}

func (s *sheet) TypedKey(e *fyne.KeyEvent) {
	if s.onKey != nil && s.onKey(e) {
		return
	}
	switch e.Name {
	case fyne.KeyEscape:
		s.dismiss()
	case fyne.KeyDown, fyne.KeyUp:
		if len(s.items) == 0 {
			return
		}
		step := 1
		if e.Name == fyne.KeyUp {
			step = -1
		}
		s.highlight((s.active + step + len(s.items)) % len(s.items))
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		if s.active >= 0 && s.active < len(s.items) {
			s.items[s.active].Tapped(nil)
		}
	}
}

func (s *sheet) highlight(index int) {
	for i, item := range s.items {
		if i == index {
			item.setHover(1, true)
		} else if item.hover > 0 {
			item.setHover(0, true)
		}
	}
	s.active = index
}

// Sheets take keyboard focus so Escape and arrows reach them.
func (s *sheet) FocusGained()   {}
func (s *sheet) FocusLost()     {}
func (s *sheet) TypedRune(rune) {}

type rect struct{ pos, size fyne.Position }

func (r rect) contains(p fyne.Position) bool {
	return p.X >= r.pos.X && p.Y >= r.pos.Y && p.X <= r.pos.X+r.size.X && p.Y <= r.pos.Y+r.size.Y
}

func (s *sheet) pad() float32 {
	if s.menu {
		return 5
	}
	return 20
}

func (s *sheet) panelRect() rect {
	size := s.Size()
	pad := s.pad()
	min := s.content.MinSize()
	width := s.width
	if width <= 0 {
		width = min.Width + 2*pad
	}
	width = fyne.Min(fyne.Max(width, min.Width+2*pad), size.Width-32)
	height := fyne.Min(min.Height+2*pad, size.Height-48)
	if s.menu && s.anchor != nil {
		shift := (1 - s.progress) * 6
		at, anchor := s.anchorAt, s.anchorSz
		x := at.X + anchor.Width - width
		if s.alignLeft {
			x = at.X
		}
		x = fyne.Max(8, fyne.Min(x, size.Width-width-8))
		y := at.Y + anchor.Height + 6 - shift
		if y+height > size.Height-8 {
			y = at.Y - height - 6 + shift
		}
		if s.columns {
			y = fyne.Max(8, fyne.Min(y, size.Height-height-8))
		}
		return rect{fyne.NewPos(x, y), fyne.NewPos(width, height)}
	}
	shift := (1 - s.progress) * 12
	return rect{fyne.NewPos((size.Width-width)/2, (size.Height-height)*0.42+shift), fyne.NewPos(width, height)}
}

func (s *sheet) CreateRenderer() fyne.WidgetRenderer {
	s.dim = canvas.NewRectangle(color.Transparent)
	s.panel = canvas.NewRectangle(colSurface)
	s.panel.StrokeWidth = 1
	s.shadow = newShadow(2.4)
	return &sheetRenderer{s}
}

type sheetRenderer struct{ s *sheet }

func (r *sheetRenderer) Layout(size fyne.Size) {
	s := r.s
	s.dim.Resize(size)
	box := s.panelRect()
	panelPos, panelSize := box.pos, fyne.NewSize(box.size.X, box.size.Y)
	if s.menu {
		// zoom-in-95 from the side facing the trigger, like web popovers
		k := 0.95 + 0.05*s.progress
		w, h := panelSize.Width*k, panelSize.Height*k
		originY := box.pos.Y
		if s.anchor != nil && box.pos.Y < s.anchorAt.Y {
			originY = box.pos.Y + panelSize.Height // menu above its trigger grows upward
		}
		panelPos = fyne.NewPos(box.pos.X+(panelSize.Width-w)/2, originY+(box.pos.Y-originY)*k)
		panelSize = fyne.NewSize(w, h)
	}
	s.panel.Move(panelPos)
	s.panel.Resize(panelSize)
	s.shadow.layout(panelPos.AddXY(0, 2), panelSize, s.panel.CornerRadius)
	pad := s.pad()
	s.content.Move(box.pos.AddXY(pad, pad))
	s.content.Resize(fyne.NewSize(box.size.X-2*pad, box.size.Y-2*pad))
}

func (r *sheetRenderer) MinSize() fyne.Size { return fyne.NewSize(0, 0) }

func withAlpha(c color.NRGBA, o float32) color.NRGBA {
	c.A = uint8(float32(c.A) * o)
	return c
}

func (r *sheetRenderer) Refresh() {
	s := r.s
	o := s.progress
	if s.menu {
		// Menus fade as one surface: panel, shadow and every item.
		s.dim.FillColor = color.Transparent
		s.panel.FillColor = withAlpha(colPopover, o)
		s.panel.StrokeColor = withAlpha(colPopBorder, o)
		s.panel.CornerRadius = 10
		if s.columns {
			s.panel.FillColor = withAlpha(colSurface, o)
			s.panel.StrokeColor = withAlpha(colBorder, o)
			s.panel.CornerRadius = 14
		}
		s.shadow.setOpacity(o)
		// Items follow the surface slightly later, so the panel leads.
		itemAlpha := fyne.Min(1, fyne.Max(0, (o-0.25)/0.75))
		for _, item := range s.items {
			if item.alpha != itemAlpha {
				item.alpha = itemAlpha
				item.Refresh()
			}
		}
	} else {
		s.dim.FillColor = color.NRGBA{A: uint8(150 * o)}
		s.panel.FillColor = colSurface
		s.panel.StrokeColor = colBorder
		s.panel.CornerRadius = 14
		s.shadow.setOpacity(1)
	}
	// Moving the content is enough to repaint it: refreshing the whole dialog
	// tree on every animation frame is what made it heavy.
	r.Layout(s.Size())
	s.dim.Refresh()
	s.shadow.refresh()
	s.panel.Refresh()
}

func (r *sheetRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{r.s.dim}
	objects = append(objects, r.s.shadow.objects()...)
	objects = append(objects, r.s.panel, r.s.content)
	return objects
}

func (r *sheetRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// picker: compact replacement for widget.Select with a web-style popover.

type picker struct {
	row         *tapRow
	Options     []string
	Selected    string
	Details     map[string]string // optional description under each option
	placeholder string
	onChanged   func(string)
	window      fyne.Window
	open        *sheet
	relayout    func()
}

func newPicker(window fyne.Window, placeholder string, style rowStyle, onChanged func(string)) *picker {
	p := &picker{placeholder: placeholder, onChanged: onChanged, window: window}
	p.row = newRow(placeholder, iconChevron, style, p.toggle)
	p.row.iconAfter = true
	return p
}

func (p *picker) toggle() {
	if p.open != nil {
		p.open.dismiss()
		return
	}
	p.show()
}

func (p *picker) setOpen(open bool) {
	p.row.selected = open
	p.row.icon = iconChevron
	if open {
		p.row.icon = iconChevronUp
	}
	p.row.Refresh()
}

func (p *picker) show() {
	if len(p.Options) == 0 {
		return
	}
	list := container.New(layout.NewCustomPaddedVBoxLayout(2))
	var menu *sheet
	var items []*tapRow
	for _, option := range p.Options {
		option := option
		var icon fyne.Resource = blankIcon
		if option == p.Selected {
			icon = iconCheck
		}
		item := newRow(option, icon, styleMenu, func() {
			if menu.closing {
				return
			}
			menu.close()
			p.SetSelected(option)
		})
		item.selected = option == p.Selected
		item.detail = p.Details[option]
		item.alpha = 0
		items = append(items, item)
		list.Add(item)
	}
	var content fyne.CanvasObject = list
	if len(p.Options) > 9 {
		scroll := newKineticVScroll(list)
		scroll.SetMinSize(fyne.NewSize(list.MinSize().Width, 9*list.Objects[0].MinSize().Height))
		content = scroll
	}
	width := fyne.Max(content.MinSize().Width+10, 200)
	menu = newSheet(p.window.Canvas(), content, width)
	menu.menu = true
	menu.anchor = p.row
	menu.items = items
	if p.row.style == styleField {
		// A field opens a list as wide as itself, like a web <select>.
		menu.width = fyne.Max(p.row.Size().Width, width)
		menu.alignLeft = true
	}
	for i, option := range p.Options {
		if option == p.Selected {
			menu.active = i
		}
	}
	menu.onClosed = func() {
		if p.open == menu {
			p.open = nil
			p.setOpen(false)
		}
	}
	p.open = menu
	p.setOpen(true)
	menu.Show()
	p.window.Canvas().Focus(menu)
}

func (p *picker) refresh() {
	text := p.Selected
	if text == "" {
		text = p.placeholder
	}
	p.row.SetText(text)
	if p.relayout != nil {
		p.relayout() // the label width changed, so the parent row must re-measure
	}
}

func (p *picker) SetSelected(option string) {
	p.Selected = option
	p.refresh()
	if p.onChanged != nil {
		p.onChanged(option)
	}
}

func (p *picker) ClearSelected() { p.Selected = ""; p.refresh() }
func (p *picker) Disable()       { p.row.Disable() }
func (p *picker) Enable()        { p.row.Enable() }

// hoverArea reports when the pointer enters or leaves its content, like a CSS
// :hover on a block; it does not take clicks.
type hoverArea struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onHover func(bool)
}

func newHoverArea(content fyne.CanvasObject, onHover func(bool)) *hoverArea {
	h := &hoverArea{content: content, onHover: onHover}
	h.ExtendBaseWidget(h)
	return h
}

func (h *hoverArea) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(h.content) }
func (h *hoverArea) MouseIn(*desktop.MouseEvent)         { h.onHover(true) }
func (h *hoverArea) MouseMoved(*desktop.MouseEvent)      {}
func (h *hoverArea) MouseOut()                           { h.onHover(false) }

// fadeRow animates a row's opacity (hover-revealed actions).
func fadeRow(r *tapRow, to float32, anim **fyne.Animation) {
	if *anim != nil {
		(*anim).Stop()
	}
	from := r.alpha
	*anim = fyne.NewAnimation(140*time.Millisecond, func(p float32) {
		r.alpha = from + (to-from)*p
		r.Refresh()
	})
	(*anim).Curve = fyne.AnimationEaseOut
	(*anim).Start()
}

// menuAction is one entry of a context menu.
type menuAction struct {
	label   string
	detail  string // optional second line
	icon    fyne.Resource
	checked bool // marks the active choice of a mode menu
	danger  bool
	run     func()
}

// showMenu opens a web-style popover menu of actions next to anchor.
func showMenu(window fyne.Window, anchor fyne.CanvasObject, actions []menuAction) *sheet {
	list := container.New(layout.NewCustomPaddedVBoxLayout(2))
	var menu *sheet
	var items []*tapRow
	for _, action := range actions {
		action := action
		icon := action.icon
		if icon == nil {
			icon = blankIcon
		}
		item := newRow(action.label, icon, styleMenu, func() {
			if menu.closing {
				return
			}
			// Act once the menu has faded out and left the overlay stack.
			menu.afterClose = action.run
			menu.close()
		})
		item.iconAfter = false
		item.danger = action.danger
		item.detail = action.detail
		item.selected = action.checked
		if action.checked {
			item.badge = iconCheck
		}
		item.alpha = 0
		items = append(items, item)
		list.Add(item)
	}
	menu = newSheet(window.Canvas(), list, fyne.Max(list.MinSize().Width+10, 200))
	menu.menu = true
	menu.anchor = anchor
	menu.items = items
	menu.Show()
	window.Canvas().Focus(menu)
	return menu
}

var blankIcon = fyne.NewStaticResource("1loop-blank.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"></svg>`))

// ---------------------------------------------------------------------------
// tipLayer floats short hover hints over the window. It holds only canvas
// primitives, never widgets, so the pointer keeps reaching the controls
// beneath it (an overlay would capture every event).

type tipLayer struct {
	layer  *fyne.Container
	bg     *canvas.Rectangle
	lines  []*canvas.Text
	anchor fyne.CanvasObject
	alpha  float32
	anim   *fyne.Animation
	delay  *time.Timer
}

func newTipLayer() *tipLayer {
	t := &tipLayer{bg: canvas.NewRectangle(colRaised)}
	t.bg.CornerRadius = 8
	t.bg.StrokeColor = colPopBorder
	t.bg.StrokeWidth = 1
	t.layer = container.NewWithoutLayout(t.bg)
	t.layer.Hide()
	return t
}

// show displays text (one hint per line) above anchor after a short delay,
// like a web tooltip.
func (t *tipLayer) show(anchor fyne.CanvasObject, text string) {
	if t.delay != nil {
		t.delay.Stop()
	}
	t.delay = time.AfterFunc(220*time.Millisecond, func() {
		fyne.Do(func() { t.place(anchor, text) })
	})
}

func (t *tipLayer) place(anchor fyne.CanvasObject, text string) {
	t.anchor = anchor
	t.layer.Objects = []fyne.CanvasObject{t.bg}
	t.lines = nil
	width, height := float32(0), float32(0)
	for i, line := range strings.Split(text, "\n") {
		ink := colText
		if i > 0 {
			ink = colMuted
		}
		label := textLine(line, 12.5, withAlpha(ink, quantize(t.alpha, 4))) // the fade brings it up
		size := label.MinSize()
		label.Move(fyne.NewPos(10, 6+height))
		width = fyne.Max(width, size.Width)
		height += size.Height
		t.lines = append(t.lines, label)
		t.layer.Objects = append(t.layer.Objects, label)
	}
	box := fyne.NewSize(width+20, height+12)
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(t.layer)
	limit := t.layer.Size()
	x := at.X - origin.X + (anchor.Size().Width-box.Width)/2
	x = fyne.Max(8, fyne.Min(x, limit.Width-box.Width-8))
	y := at.Y - origin.Y - box.Height - 8
	if y < 8 {
		y = at.Y - origin.Y + anchor.Size().Height + 8
	}
	t.bg.Resize(box)
	for _, o := range t.layer.Objects {
		o.Move(o.Position().AddXY(x, y))
	}
	t.layer.Show()
	t.fade(1)
}

func (t *tipLayer) hide() {
	if t.delay != nil {
		t.delay.Stop()
		t.delay = nil
	}
	t.anchor = nil
	if t.layer.Visible() {
		t.fade(0)
	}
}

// fade eases the hint in or out; text alpha moves in a few steps because
// Fyne rasterises one glyph texture per colour.
func (t *tipLayer) fade(to float32) {
	if t.anim != nil {
		t.anim.Stop()
	}
	from := t.alpha
	t.anim = fyne.NewAnimation(120*time.Millisecond, func(p float32) {
		t.alpha = from + (to-from)*p
		t.bg.FillColor = withAlpha(colRaised, t.alpha)
		t.bg.StrokeColor = withAlpha(colPopBorder, t.alpha)
		t.bg.Refresh()
		step := quantize(t.alpha, 4)
		for i, line := range t.lines {
			ink := colText
			if i > 0 {
				ink = colMuted
			}
			if c := withAlpha(ink, step); line.Color != c {
				line.Color = c
				line.Refresh()
			}
		}
		if p >= 1 && to == 0 {
			t.layer.Hide()
		}
	})
	t.anim.Curve = fyne.AnimationEaseOut
	t.anim.Start()
}

// ---------------------------------------------------------------------------
// Window chrome: drag region and resize edges for the undecorated window.

type dragArea struct {
	widget.BaseWidget
	window fyne.Window
	last   time.Time
	at     fyne.Position
}

func newDragArea(w fyne.Window) *dragArea {
	d := &dragArea{window: w}
	d.ExtendBaseWidget(d)
	return d
}

func (d *dragArea) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	near := e.AbsolutePosition.Subtract(d.at)
	if time.Since(d.last) < 400*time.Millisecond && near.X*near.X+near.Y*near.Y < 36 {
		d.last = time.Time{}
		windowToggleMaximize(d.window)
		return
	}
	d.last, d.at = time.Now(), e.AbsolutePosition
	windowMoveResize(d.window, edgeMove)
}

func (d *dragArea) MouseUp(*desktop.MouseEvent) {}

func (d *dragArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

type resizeEdge struct {
	widget.BaseWidget
	window    fyne.Window
	direction int
}

func (e *resizeEdge) MouseDown(m *desktop.MouseEvent) {
	if m.Button == desktop.MouseButtonPrimary {
		windowMoveResize(e.window, e.direction)
	}
}
func (e *resizeEdge) MouseUp(*desktop.MouseEvent) {}

func (e *resizeEdge) Cursor() desktop.Cursor {
	switch e.direction {
	case edgeLeft, edgeRight:
		return desktop.HResizeCursor
	}
	return desktop.VResizeCursor
}

func (e *resizeEdge) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

type edgesLayout struct{ thickness float32 }

func (l edgesLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (l edgesLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	t, c := l.thickness, l.thickness*2
	frames := map[int][2]fyne.Position{
		edgeTop:         {fyne.NewPos(c, 0), fyne.NewPos(size.Width-2*c, t)},
		edgeBottom:      {fyne.NewPos(c, size.Height-t), fyne.NewPos(size.Width-2*c, t)},
		edgeLeft:        {fyne.NewPos(0, c), fyne.NewPos(t, size.Height-2*c)},
		edgeRight:       {fyne.NewPos(size.Width-t, c), fyne.NewPos(t, size.Height-2*c)},
		edgeTopLeft:     {fyne.NewPos(0, 0), fyne.NewPos(c, c)},
		edgeTopRight:    {fyne.NewPos(size.Width-c, 0), fyne.NewPos(c, c)},
		edgeBottomLeft:  {fyne.NewPos(0, size.Height-c), fyne.NewPos(c, c)},
		edgeBottomRight: {fyne.NewPos(size.Width-c, size.Height-c), fyne.NewPos(c, c)},
	}
	for _, object := range objects {
		f := frames[object.(*resizeEdge).direction]
		object.Move(f[0])
		object.Resize(fyne.NewSize(f[1].X, f[1].Y))
	}
}

func resizeEdges(w fyne.Window) fyne.CanvasObject {
	edges := container.New(edgesLayout{thickness: 4})
	for _, direction := range []int{edgeTop, edgeBottom, edgeLeft, edgeRight, edgeTopLeft, edgeTopRight, edgeBottomLeft, edgeBottomRight} {
		e := &resizeEdge{window: w, direction: direction}
		e.ExtendBaseWidget(e)
		edges.Add(e)
	}
	return edges
}

func windowControls(w fyne.Window, quit func()) fyne.CanvasObject {
	minimize := newRow("", iconMinimize, styleWindow, func() { windowMinimize(w) })
	maximize := newRow("", iconMaximize, styleWindow, func() { windowToggleMaximize(w) })
	closeButton := newRow("", iconClose, styleWindow, quit)
	closeButton.hoverFill = colClose
	return container.New(layout.NewCustomPaddedHBoxLayout(2), minimize, maximize, closeButton)
}
