package ui

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

func (themeSpec loopTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
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
	return themeSpec.Theme.Color(name, theme.VariantDark)
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

func (themeSpec loopTheme) Font(style fyne.TextStyle) fyne.Resource {
	key := "regular"
	switch {
	case style.Monospace, style.Symbol:
		return themeSpec.Theme.Font(style)
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
	return themeSpec.Theme.Font(style)
}

func (themeSpec loopTheme) Size(name fyne.ThemeSizeName) float32 {
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
	return themeSpec.Theme.Size(name)
}

// The composer edits text directly on its surface, without a nested field box.
type bareInputTheme struct{ fyne.Theme }

func (themeSpec bareInputTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameInputBackground || name == theme.ColorNameInputBorder {
		return color.Transparent
	}
	return themeSpec.Theme.Color(name, v)
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
	if width <= 0 || (len(text) <= 600 && textSize(text, size, style).Width <= width) {
		return text
	}
	runes := []rune(text)
	if len(runes) > 600 { // no row is that wide: never measure a whole one-line script
		runes = runes[:600]
	}
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if textSize(string(runes[:mid])+"…", size, style).Width <= width {
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
	iconBookmark     = lineIcon("bookmark", `<path d="M4.5 2.5h7v11L8 10.8l-3.5 2.7z"/>`)
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
	row := &tapRow{text: text, icon: icon, style: style, onTap: onTap, size: 14, alpha: 1}
	switch style {
	case styleOutline, stylePrimary:
		row.center = true
	case styleChip:
		row.size = 13
	case styleField, styleMenu:
		row.iconAfter = true
	}
	row.ExtendBaseWidget(row)
	return row
}

func newIconButton(icon fyne.Resource, fill, ink color.NRGBA, onTap func()) *tapRow {
	row := newRow("", icon, styleIcon, onTap)
	row.fill, row.ink = fill, ink
	return row
}

func (row *tapRow) SetText(text string)       { row.text = text; row.Refresh() }
func (row *tapRow) SetTrailing(text string)   { row.trailing = text; row.Refresh() }
func (row *tapRow) SetSelected(selected bool) { row.selected = selected; row.Refresh() }
func (row *tapRow) Disable()                  { row.disabled = true; row.setHover(0, false); row.Refresh() }
func (row *tapRow) Enable()                   { row.disabled = false; row.Refresh() }

func (row *tapRow) Tapped(*fyne.PointEvent) {
	if !row.disabled && row.onTap != nil {
		row.onTap()
	}
}

// Rows are mouse targets of their own, so a title bar behind them never
// mistakes a click on a control for the start of a window move.
func (row *tapRow) MouseDown(*desktop.MouseEvent) {}
func (row *tapRow) MouseUp(*desktop.MouseEvent)   {}

func (row *tapRow) Cursor() desktop.Cursor {
	if row.disabled || row.onTap == nil {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (row *tapRow) MouseIn(*desktop.MouseEvent) {
	if !row.disabled && row.onTap != nil {
		row.setHover(1, true)
	}
	if row.onHover != nil {
		row.onHover(true)
	}
}

func (row *tapRow) MouseMoved(*desktop.MouseEvent) {}
func (row *tapRow) MouseOut() {
	row.setHover(0, true)
	if row.onHover != nil {
		row.onHover(false)
	}
}

func (row *tapRow) TappedSecondary(*fyne.PointEvent) {
	if !row.disabled && row.onMenu != nil {
		row.onMenu()
	}
}

// setHover eases the hover factor so colours fade like CSS transitions.
func (row *tapRow) setHover(target float32, animate bool) {
	if row.anim != nil {
		row.anim.Stop()
		row.anim = nil
	}
	if !animate {
		row.hover = target
		return
	}
	from := row.hover
	row.anim = fyne.NewAnimation(130*time.Millisecond, func(p float32) {
		row.hover = from + (target-from)*p
		row.Refresh()
	})
	row.anim.Curve = fyne.AnimationEaseOut
	row.anim.Start()
}

func (row *tapRow) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	label := canvas.NewText(row.text, colText)
	label.TextSize = row.size
	trailing := canvas.NewText("", colFaint)
	trailing.TextSize = row.size - 1
	icon := canvas.NewImageFromResource(nil)
	icon.FillMode = canvas.ImageFillContain
	detail := canvas.NewText("", colMuted)
	detail.TextSize = 12
	badge := canvas.NewImageFromResource(nil)
	badge.FillMode = canvas.ImageFillContain
	renderer := &tapRowRenderer{row: row, bg: bg, label: label, trailing: trailing, icon: icon, detail: detail, badge: badge}
	renderer.Refresh()
	return renderer
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

func (renderer *tapRowRenderer) metrics() (padX, padY, iconSize, gap float32) {
	switch renderer.row.style {
	case styleNav:
		return 10, 7, 18, 10
	case styleChip:
		return 7, 4, 16, 7
	case styleIcon:
		icon, pad := float32(18), float32(7)
		if renderer.row.iconPx > 0 {
			icon = renderer.row.iconPx
		}
		if renderer.row.padPx > 0 {
			pad = renderer.row.padPx
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

func (renderer *tapRowRenderer) MinSize() fyne.Size {
	padX, padY, iconSize, gap := renderer.metrics()
	textHeight := textSize("Ag", renderer.row.size, fyne.TextStyle{}).Height
	if renderer.row.style == styleIcon || renderer.row.style == styleWindow {
		return fyne.NewSize(iconSize+2*padX, iconSize+2*padY)
	}
	height := fyne.Max(textHeight, iconSize) + 2*padY
	if renderer.row.detail != "" {
		height += textSize("Ag", 12, fyne.TextStyle{}).Height + 1
	}
	width := 2 * padX
	if renderer.row.icon != nil {
		width += iconSize
		if renderer.row.text != "" {
			width += gap
		}
	}
	if renderer.row.style == styleNav || renderer.row.style == styleField || renderer.row.truncate {
		// Rows that fill a column truncate instead of forcing it wider.
		return fyne.NewSize(width+40, height)
	}
	width += fyne.Max(textSize(renderer.row.text, renderer.row.size, fyne.TextStyle{}).Width,
		textSize(renderer.row.detail, 12, fyne.TextStyle{}).Width) + 2
	if renderer.row.trailing != "" {
		width += gap + textSize(renderer.row.trailing, renderer.row.size-1, fyne.TextStyle{}).Width
	}
	if renderer.row.badge != nil {
		width += gap + iconSize
	}
	return fyne.NewSize(width, height)
}

func (renderer *tapRowRenderer) Layout(size fyne.Size) {
	padX, _, iconSize, gap := renderer.metrics()
	renderer.bg.Resize(size)
	renderer.bg.Move(fyne.NewPos(0, 0))
	switch renderer.row.style {
	case styleOutline, styleIcon:
		renderer.bg.CornerRadius = size.Height / 2
	case styleWindow:
		renderer.bg.CornerRadius = 6
	case stylePrimary, styleField:
		renderer.bg.CornerRadius = 8
	case styleModel:
		renderer.bg.CornerRadius = 9
	default:
		renderer.bg.CornerRadius = 6
	}
	if renderer.row.style == styleIcon || renderer.row.style == styleWindow {
		renderer.icon.Resize(fyne.NewSquareSize(iconSize))
		renderer.icon.Move(fyne.NewPos((size.Width-iconSize)/2, (size.Height-iconSize)/2))
		return
	}
	trailingWidth := float32(0)
	if renderer.row.badge != nil {
		renderer.badge.Resize(fyne.NewSquareSize(iconSize))
		renderer.badge.Move(fyne.NewPos(size.Width-padX-iconSize-renderer.row.endInset, (size.Height-iconSize)/2))
		trailingWidth = iconSize + gap
	}
	if renderer.row.trailing != "" {
		width := renderer.trailing.MinSize().Width
		renderer.trailing.Move(fyne.NewPos(size.Width-padX-trailingWidth-width, (size.Height-renderer.trailing.MinSize().Height)/2))
		trailingWidth += width + gap
	}
	iconRoom := float32(0)
	if renderer.row.icon != nil {
		iconRoom = iconSize
		if renderer.row.text != "" {
			iconRoom += gap
		}
	}
	room := size.Width - 2*padX - iconRoom - trailingWidth - renderer.row.endInset
	renderer.label.Text = fitText(renderer.row.text, renderer.row.size, fyne.TextStyle{}, room)
	textWidth := renderer.label.MinSize().Width
	textY := (size.Height - renderer.label.MinSize().Height) / 2
	if renderer.row.detail != "" {
		detailHeight := renderer.detail.MinSize().Height + 1
		textY = (size.Height - renderer.label.MinSize().Height - detailHeight) / 2
		renderer.detail.Text = fitText(renderer.row.detail, 12, fyne.TextStyle{}, room)
	}
	iconY := (size.Height - iconSize) / 2
	x := padX
	if renderer.row.center {
		x = (size.Width - iconRoom - textWidth) / 2
	}
	if renderer.row.icon != nil {
		renderer.icon.Resize(fyne.NewSquareSize(iconSize))
		switch {
		case !renderer.row.iconAfter:
			renderer.icon.Move(fyne.NewPos(x, iconY))
			x += iconRoom
		case renderer.row.style == styleField || renderer.row.style == styleMenu:
			renderer.icon.Move(fyne.NewPos(size.Width-padX-iconSize, iconY)) // chevron and check sit on the far edge
		default:
			renderer.icon.Move(fyne.NewPos(x+textWidth+gap, iconY))
		}
	}
	renderer.label.Move(fyne.NewPos(x, textY))
	// The second line starts under the label, past any leading icon.
	renderer.detail.Move(fyne.NewPos(x, textY+renderer.label.MinSize().Height+1))
}

func (renderer *tapRowRenderer) colorsAt(h float32) (fill, stroke, ink color.NRGBA) {
	row := renderer.row
	clear := color.NRGBA{}
	switch row.style {
	case styleNav:
		fill = mix(clear, colHover, h)
		if row.selected {
			fill = mix(colSelected, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}, h)
		}
		ink = colText
		if !row.selected && row.accent {
			ink = mix(color.NRGBA{R: 0xb4, G: 0xb4, B: 0xb4, A: 0xff}, colText, h)
		}
	case styleChip:
		fill = mix(clear, colHover, h)
		ink = mix(colMuted, colText, h)
		if row.selected {
			ink = colText
		}
		if row.accent {
			ink = colAmber
		}
		if row.danger {
			ink = colDanger
		}
	case styleOutline:
		stroke = mix(colBorder, colBorderHot, h)
		ink = mix(colMuted, colText, h)
	case stylePrimary:
		fill = mix(colAmber, colAmberHot, h)
		ink = colInk
		if row.danger {
			fill = mix(colClose, color.NRGBA{R: 0xd9, G: 0x3b, B: 0x2b, A: 0xff}, h)
			ink = colText
		}
	case styleIcon:
		hot := mix(row.fill, colText, 0.22)
		if row.fill.A < 0xff { // a translucent wash only deepens
			hot = row.fill
			hot.A = uint8(fyne.Min(255, float32(row.fill.A)*2+10))
		}
		fill = mix(row.fill, hot, h)
		if row.fill.A == 0 {
			fill = mix(clear, colHover, h)
		}
		// Controls without an ink of their own brighten on hover like the
		// chips beside them; the others keep their fixed ink.
		ink = row.ink
		if row.ink == (color.NRGBA{}) {
			ink = mix(colMuted, colText, h)
		}
	case styleMenu:
		base := clear
		if row.selected {
			base = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x09}
		}
		fill = mix(base, colMenuHover, h)
		ink = colText
		if row.danger {
			ink = colDanger
		}
	case styleModel:
		fill = mix(clear, colRaised, h)
		ink = mix(colMuted, colText, h)
		if row.selected {
			ink = colText
		}
	case styleField:
		fill = colField
		stroke = mix(colBorder, color.NRGBA{R: 0x3c, G: 0x3c, B: 0x3c, A: 0xff}, h)
		if row.selected { // open: focus ring like a web select
			stroke = color.NRGBA{R: 0xff, G: 0xb0, B: 0x00, A: 0x99}
		}
		ink = colText
	case styleWindow:
		target := colHover
		if row.hoverFill.A != 0 {
			target = row.hoverFill
		}
		fill = mix(clear, target, h)
		ink = mix(colMuted, colText, h)
	}
	if row.disabled {
		ink = colFaint
		if row.style == styleModel {
			fill = clear
		}
		if row.style == stylePrimary || row.style == styleIcon && row.fill.A != 0 {
			fill = colRaised
		}
	}
	return
}

func quantize(v, steps float32) float32 { return float32(int(v*steps+0.5)) / steps }

// Refresh animates shapes continuously but moves text and icon colours in a
// few steps: Fyne caches one text texture per colour, so a smooth colour
// tween would rasterise new glyph textures on every frame.
func (renderer *tapRowRenderer) Refresh() {
	row := renderer.row
	fill, stroke, _ := renderer.colorsAt(row.hover)
	_, _, ink := renderer.colorsAt(quantize(row.hover, 4))
	shape := func(c color.NRGBA) color.NRGBA { c.A = uint8(float32(c.A) * row.alpha); return c }
	textAlpha := quantize(row.alpha, 5)
	text := func(c color.NRGBA) color.NRGBA { c.A = uint8(float32(c.A) * textAlpha); return c }
	renderer.bg.FillColor = shape(fill)
	renderer.bg.StrokeColor = shape(stroke)
	renderer.bg.StrokeWidth = 0
	if stroke.A != 0 {
		renderer.bg.StrokeWidth = 1
	}
	renderer.label.TextSize = row.size
	renderer.trailing.Text = row.trailing
	renderer.trailing.TextSize = row.size - 1
	renderer.trailing.Color = text(colFaint)
	renderer.detail.Text = row.detail
	renderer.detail.Color = text(colMuted)
	if row.style == styleModel {
		renderer.detail.Color = text(colFaint)
	}
	renderer.label.Color = text(ink)
	iconInk := ink
	if row.style == styleField || row.style == styleChip && !row.accent && row.iconAfter {
		iconInk = colMuted
	}
	if row.icon != nil && (renderer.source != row.icon || renderer.tint != iconInk) {
		renderer.source, renderer.tint = row.icon, iconInk
		renderer.icon.Resource = tinted(row.icon, iconInk)
		renderer.icon.Refresh()
	}
	if row.badge != nil && renderer.marked != row.badge {
		renderer.marked = row.badge
		renderer.badge.Resource = tinted(row.badge, colAmber)
		renderer.badge.Refresh()
	}
	// Translucency is read at draw time; refreshing the image would only
	// throw its texture away.
	iconAlpha := row.alpha
	if row.disabled && row.style == styleModel {
		iconAlpha *= 0.4
	}
	renderer.icon.Translucency = float64(1 - iconAlpha)
	renderer.badge.Translucency = renderer.icon.Translucency
	renderer.Layout(row.Size())
	renderer.bg.Refresh()
	renderer.label.Refresh()
	renderer.trailing.Refresh()
	renderer.detail.Refresh()
}

func (renderer *tapRowRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{renderer.bg, renderer.label, renderer.trailing, renderer.detail}
	if renderer.row.icon != nil {
		objects = append(objects, renderer.icon)
	}
	if renderer.row.badge != nil {
		objects = append(objects, renderer.badge)
	}
	return objects
}

func (renderer *tapRowRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// Soft drop shadow: stacked translucent rounded rectangles stand in for blur.

type shadow struct {
	layers []*canvas.Rectangle
	alphas []float32
}

func newShadow(strength float32) *shadow {
	dropShadow := &shadow{}
	for i, alpha := range []float32{0.10, 0.08, 0.06, 0.04} {
		r := canvas.NewRectangle(color.NRGBA{A: uint8(255 * alpha * strength)})
		r.CornerRadius = float32(12 + 3*i)
		dropShadow.layers = append(dropShadow.layers, r)
		dropShadow.alphas = append(dropShadow.alphas, alpha*strength)
	}
	return dropShadow
}

func (dropShadow *shadow) setOpacity(o float32) {
	for i, r := range dropShadow.layers {
		r.FillColor = color.NRGBA{A: uint8(255 * fyne.Min(1, dropShadow.alphas[i]) * o)}
	}
}

func (dropShadow *shadow) layout(pos fyne.Position, size fyne.Size, radius float32) {
	for i, r := range dropShadow.layers {
		spread := float32(3 * (i + 1))
		r.CornerRadius = radius + spread
		r.Move(pos.AddXY(-spread, -spread+spread/2))
		r.Resize(size.AddWidthHeight(2*spread, 2*spread))
	}
}

func (dropShadow *shadow) objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, len(dropShadow.layers))
	for i := range dropShadow.layers {
		objects[len(dropShadow.layers)-1-i] = dropShadow.layers[i]
	}
	return objects
}

func (dropShadow *shadow) refresh() {
	for _, r := range dropShadow.layers {
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

func newSheet(canvas fyne.Canvas, content fyne.CanvasObject, width float32) *sheet {
	dialog := &sheet{canvas: canvas, content: content, width: width, active: -1}
	dialog.ExtendBaseWidget(dialog)
	return dialog
}

func (dialog *sheet) animate(from, to float32, duration time.Duration, curve fyne.AnimationCurve, done func()) {
	if dialog.anim != nil {
		dialog.anim.Stop()
	}
	dialog.anim = fyne.NewAnimation(duration, func(p float32) {
		dialog.progress = from + (to-from)*p
		dialog.Refresh()
		if p >= 1 && done != nil {
			done()
		}
	})
	dialog.anim.Curve = curve
	dialog.anim.Start()
}

func (dialog *sheet) Show() {
	if dialog.open {
		return
	}
	dialog.open, dialog.closing = true, false
	dialog.progress = 0
	if dialog.anchor != nil {
		dialog.anchorAt = fyne.CurrentApp().Driver().AbsolutePositionForObject(dialog.anchor)
		dialog.anchorSz = dialog.anchor.Size()
	}
	dialog.Resize(dialog.canvas.Size())
	dialog.canvas.Overlays().Add(dialog)
	dialog.Refresh()
	duration := 200 * time.Millisecond
	if dialog.menu {
		duration = 170 * time.Millisecond
	}
	// Start on the next loop turn: the first frame of a new overlay builds its
	// textures, and timing that frame would swallow the start of the motion.
	go fyne.Do(func() {
		if dialog.open && !dialog.closing {
			dialog.animate(0, 1, duration, easeOutQuint, nil)
		}
	})
}

// easeOutQuint matches the snappy deceleration of web UI popovers.
func easeOutQuint(x float32) float32 {
	x = 1 - x
	return 1 - x*x*x*x*x
}

// Hide removes the sheet at once (dialogs whose content changes the view).
func (dialog *sheet) Hide() {
	if !dialog.open {
		return
	}
	dialog.open = false
	if dialog.anim != nil {
		dialog.anim.Stop()
	}
	// Fyne's OverlayStack.Remove also drops every overlay above the removed
	// one (a confirmation opened from a menu, say), so put those back.
	stack := dialog.canvas.Overlays()
	var above []fyne.CanvasObject
	found := false
	for _, object := range stack.List() {
		if found {
			above = append(above, object)
		}
		found = found || object == dialog
	}
	stack.Remove(dialog)
	for _, canvasObjectValue := range above {
		stack.Add(canvasObjectValue)
	}
	if dialog.onClosed != nil {
		dialog.onClosed()
	}
	if after := dialog.afterClose; after != nil {
		dialog.afterClose = nil
		after()
	}
}

// close fades the sheet out before removing it, like a web popover.
func (dialog *sheet) close() {
	if !dialog.open || dialog.closing {
		return
	}
	dialog.closing = true
	dialog.animate(dialog.progress, 0, 120*time.Millisecond, fyne.AnimationEaseIn, dialog.Hide)
}

// dismiss closes the sheet as a cancellation (outside tap or Escape).
func (dialog *sheet) dismiss() {
	if dialog.menu {
		dialog.close()
	} else {
		dialog.Hide()
	}
	if dialog.onDismiss != nil {
		dialog.onDismiss()
	}
}

func (dialog *sheet) Tapped(e *fyne.PointEvent) {
	if dialog.menu && !dialog.panelRect().contains(e.Position) {
		dialog.dismiss()
	}
}

func (dialog *sheet) TappedSecondary(*fyne.PointEvent) {}

func (dialog *sheet) TypedKey(e *fyne.KeyEvent) {
	if dialog.onKey != nil && dialog.onKey(e) {
		return
	}
	switch e.Name {
	case fyne.KeyEscape:
		dialog.dismiss()
	case fyne.KeyDown, fyne.KeyUp:
		if len(dialog.items) == 0 {
			return
		}
		step := 1
		if e.Name == fyne.KeyUp {
			step = -1
		}
		dialog.highlight((dialog.active + step + len(dialog.items)) % len(dialog.items))
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		if dialog.active >= 0 && dialog.active < len(dialog.items) {
			dialog.items[dialog.active].Tapped(nil)
		}
	}
}

func (dialog *sheet) highlight(index int) {
	for i, item := range dialog.items {
		if i == index {
			item.setHover(1, true)
		} else if item.hover > 0 {
			item.setHover(0, true)
		}
	}
	dialog.active = index
}

// Sheets take keyboard focus so Escape and arrows reach them.
func (dialog *sheet) FocusGained()   {}
func (dialog *sheet) FocusLost()     {}
func (dialog *sheet) TypedRune(rune) {}

type rect struct{ pos, size fyne.Position }

func (rectangle rect) contains(position fyne.Position) bool {
	return position.X >= rectangle.pos.X && position.Y >= rectangle.pos.Y && position.X <= rectangle.pos.X+rectangle.size.X && position.Y <= rectangle.pos.Y+rectangle.size.Y
}

func (dialog *sheet) pad() float32 {
	if dialog.menu {
		return 5
	}
	return 20
}

func (dialog *sheet) panelRect() rect {
	size := dialog.Size()
	pad := dialog.pad()
	min := dialog.content.MinSize()
	width := dialog.width
	if width <= 0 {
		width = min.Width + 2*pad
	}
	width = fyne.Min(fyne.Max(width, min.Width+2*pad), size.Width-32)
	height := fyne.Min(min.Height+2*pad, size.Height-48)
	if dialog.menu && dialog.anchor != nil {
		shift := (1 - dialog.progress) * 6
		at, anchor := dialog.anchorAt, dialog.anchorSz
		x := at.X + anchor.Width - width
		if dialog.alignLeft {
			x = at.X
		}
		x = fyne.Max(8, fyne.Min(x, size.Width-width-8))
		y := at.Y + anchor.Height + 6 - shift
		if y+height > size.Height-8 {
			y = at.Y - height - 6 + shift
		}
		if dialog.columns {
			y = fyne.Max(8, fyne.Min(y, size.Height-height-8))
		}
		return rect{fyne.NewPos(x, y), fyne.NewPos(width, height)}
	}
	shift := (1 - dialog.progress) * 12
	return rect{fyne.NewPos((size.Width-width)/2, (size.Height-height)*0.42+shift), fyne.NewPos(width, height)}
}

func (dialog *sheet) CreateRenderer() fyne.WidgetRenderer {
	dialog.dim = canvas.NewRectangle(color.Transparent)
	dialog.panel = canvas.NewRectangle(colSurface)
	dialog.panel.StrokeWidth = 1
	dialog.shadow = newShadow(2.4)
	return &sheetRenderer{dialog}
}

type sheetRenderer struct{ dialog *sheet }

func (renderer *sheetRenderer) Layout(size fyne.Size) {
	sheet := renderer.dialog
	sheet.dim.Resize(size)
	box := sheet.panelRect()
	panelPos, panelSize := box.pos, fyne.NewSize(box.size.X, box.size.Y)
	if sheet.menu {
		// zoom-in-95 from the side facing the trigger, like web popovers
		k := 0.95 + 0.05*sheet.progress
		w, h := panelSize.Width*k, panelSize.Height*k
		originY := box.pos.Y
		if sheet.anchor != nil && box.pos.Y < sheet.anchorAt.Y {
			originY = box.pos.Y + panelSize.Height // menu above its trigger grows upward
		}
		panelPos = fyne.NewPos(box.pos.X+(panelSize.Width-w)/2, originY+(box.pos.Y-originY)*k)
		panelSize = fyne.NewSize(w, h)
	}
	sheet.panel.Move(panelPos)
	sheet.panel.Resize(panelSize)
	sheet.shadow.layout(panelPos.AddXY(0, 2), panelSize, sheet.panel.CornerRadius)
	pad := sheet.pad()
	sheet.content.Move(box.pos.AddXY(pad, pad))
	sheet.content.Resize(fyne.NewSize(box.size.X-2*pad, box.size.Y-2*pad))
}

func (renderer *sheetRenderer) MinSize() fyne.Size { return fyne.NewSize(0, 0) }

func withAlpha(c color.NRGBA, o float32) color.NRGBA {
	c.A = uint8(float32(c.A) * o)
	return c
}

func (renderer *sheetRenderer) Refresh() {
	sheet := renderer.dialog
	o := sheet.progress
	if sheet.menu {
		// Menus fade as one surface: panel, shadow and every item.
		sheet.dim.FillColor = color.Transparent
		sheet.panel.FillColor = withAlpha(colPopover, o)
		sheet.panel.StrokeColor = withAlpha(colPopBorder, o)
		sheet.panel.CornerRadius = 10
		if sheet.columns {
			sheet.panel.FillColor = withAlpha(colSurface, o)
			sheet.panel.StrokeColor = withAlpha(colBorder, o)
			sheet.panel.CornerRadius = 14
		}
		sheet.shadow.setOpacity(o)
		// Items follow the surface slightly later, so the panel leads.
		itemAlpha := fyne.Min(1, fyne.Max(0, (o-0.25)/0.75))
		for _, item := range sheet.items {
			if item.alpha != itemAlpha {
				item.alpha = itemAlpha
				item.Refresh()
			}
		}
	} else {
		sheet.dim.FillColor = color.NRGBA{A: uint8(150 * o)}
		sheet.panel.FillColor = colSurface
		sheet.panel.StrokeColor = colBorder
		sheet.panel.CornerRadius = 14
		sheet.shadow.setOpacity(1)
	}
	// Moving the content is enough to repaint it: refreshing the whole dialog
	// tree on every animation frame is what made it heavy.
	renderer.Layout(sheet.Size())
	sheet.dim.Refresh()
	sheet.shadow.refresh()
	sheet.panel.Refresh()
}

func (renderer *sheetRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{renderer.dialog.dim}
	objects = append(objects, renderer.dialog.shadow.objects()...)
	objects = append(objects, renderer.dialog.panel, renderer.dialog.content)
	return objects
}

func (renderer *sheetRenderer) Destroy() {}

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
	pickerControl := &picker{placeholder: placeholder, onChanged: onChanged, window: window}
	pickerControl.row = newRow(placeholder, iconChevron, style, pickerControl.toggle)
	pickerControl.row.iconAfter = true
	return pickerControl
}

func (pickerControl *picker) toggle() {
	if pickerControl.open != nil {
		pickerControl.open.dismiss()
		return
	}
	pickerControl.show()
}

func (pickerControl *picker) setOpen(open bool) {
	pickerControl.row.selected = open
	pickerControl.row.icon = iconChevron
	if open {
		pickerControl.row.icon = iconChevronUp
	}
	pickerControl.row.Refresh()
}

func (pickerControl *picker) show() {
	if len(pickerControl.Options) == 0 {
		return
	}
	list := container.New(layout.NewCustomPaddedVBoxLayout(2))
	var menu *sheet
	var items []*tapRow
	for _, option := range pickerControl.Options {
		option := option
		var icon fyne.Resource = blankIcon
		if option == pickerControl.Selected {
			icon = iconCheck
		}
		item := newRow(option, icon, styleMenu, func() {
			if menu.closing {
				return
			}
			menu.close()
			pickerControl.SetSelected(option)
		})
		item.selected = option == pickerControl.Selected
		item.detail = pickerControl.Details[option]
		item.alpha = 0
		items = append(items, item)
		list.Add(item)
	}
	var content fyne.CanvasObject = list
	if len(pickerControl.Options) > 9 {
		scroll := newKineticVScroll(list)
		scroll.SetMinSize(fyne.NewSize(list.MinSize().Width, 9*list.Objects[0].MinSize().Height))
		content = scroll
	}
	width := fyne.Max(content.MinSize().Width+10, 200)
	menu = newSheet(pickerControl.window.Canvas(), content, width)
	menu.menu = true
	menu.anchor = pickerControl.row
	menu.items = items
	if pickerControl.row.style == styleField {
		// A field opens a list as wide as itself, like a web <select>.
		menu.width = fyne.Max(pickerControl.row.Size().Width, width)
		menu.alignLeft = true
	}
	for i, option := range pickerControl.Options {
		if option == pickerControl.Selected {
			menu.active = i
		}
	}
	menu.onClosed = func() {
		if pickerControl.open == menu {
			pickerControl.open = nil
			pickerControl.setOpen(false)
		}
	}
	pickerControl.open = menu
	pickerControl.setOpen(true)
	menu.Show()
	pickerControl.window.Canvas().Focus(menu)
}

func (pickerControl *picker) refresh() {
	text := pickerControl.Selected
	if text == "" {
		text = pickerControl.placeholder
	}
	pickerControl.row.SetText(text)
	if pickerControl.relayout != nil {
		pickerControl.relayout() // the label width changed, so the parent row must re-measure
	}
}

func (pickerControl *picker) SetSelected(option string) {
	pickerControl.Selected = option
	pickerControl.refresh()
	if pickerControl.onChanged != nil {
		pickerControl.onChanged(option)
	}
}

func (pickerControl *picker) ClearSelected() { pickerControl.Selected = ""; pickerControl.refresh() }
func (pickerControl *picker) Disable()       { pickerControl.row.Disable() }
func (pickerControl *picker) Enable()        { pickerControl.row.Enable() }

// fadeRow animates a row's opacity (hover-revealed actions).
func fadeRow(row *tapRow, to float32, anim **fyne.Animation) {
	if *anim != nil {
		(*anim).Stop()
	}
	from := row.alpha
	*anim = fyne.NewAnimation(140*time.Millisecond, func(p float32) {
		row.alpha = from + (to-from)*p
		row.Refresh()
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
	tooltip := &tipLayer{bg: canvas.NewRectangle(colRaised)}
	tooltip.bg.CornerRadius = 8
	tooltip.bg.StrokeColor = colPopBorder
	tooltip.bg.StrokeWidth = 1
	tooltip.layer = container.NewWithoutLayout(tooltip.bg)
	tooltip.layer.Hide()
	return tooltip
}

// show displays text (one hint per line) above anchor after a short delay,
// like a web tooltip.
func (tooltip *tipLayer) show(anchor fyne.CanvasObject, text string) {
	if tooltip.delay != nil {
		tooltip.delay.Stop()
	}
	tooltip.delay = time.AfterFunc(220*time.Millisecond, func() {
		fyne.Do(func() { tooltip.place(anchor, text) })
	})
}

func (tooltip *tipLayer) place(anchor fyne.CanvasObject, text string) {
	tooltip.bg.Move(fyne.NewPos(0, 0))
	tooltip.anchor = anchor
	tooltip.layer.Objects = []fyne.CanvasObject{tooltip.bg}
	tooltip.lines = nil
	width, height := float32(0), float32(0)
	for i, line := range strings.Split(text, "\n") {
		ink := colText
		if i > 0 {
			ink = colMuted
		}
		label := textLine(line, 12.5, withAlpha(ink, quantize(tooltip.alpha, 4))) // the fade brings it up
		size := label.MinSize()
		label.Move(fyne.NewPos(10, 6+height))
		width = fyne.Max(width, size.Width)
		height += size.Height
		tooltip.lines = append(tooltip.lines, label)
		tooltip.layer.Objects = append(tooltip.layer.Objects, label)
	}
	box := fyne.NewSize(width+20, height+12)
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(tooltip.layer)
	limit := tooltip.layer.Size()
	x := at.X - origin.X + (anchor.Size().Width-box.Width)/2
	x = fyne.Max(8, fyne.Min(x, limit.Width-box.Width-8))
	y := at.Y - origin.Y - box.Height - 8
	if y < 8 {
		y = at.Y - origin.Y + anchor.Size().Height + 8
	}
	tooltip.bg.Resize(box)
	for _, object := range tooltip.layer.Objects {
		object.Move(object.Position().AddXY(x, y))
	}
	tooltip.layer.Show()
	tooltip.fade(1)
}

func (tooltip *tipLayer) hide() {
	if tooltip.delay != nil {
		tooltip.delay.Stop()
		tooltip.delay = nil
	}
	tooltip.anchor = nil
	if tooltip.layer.Visible() {
		tooltip.fade(0)
	}
}

// fade eases the hint in or out; text alpha moves in a few steps because
// Fyne rasterises one glyph texture per colour.
func (tooltip *tipLayer) fade(to float32) {
	if tooltip.anim != nil {
		tooltip.anim.Stop()
	}
	from := tooltip.alpha
	tooltip.anim = fyne.NewAnimation(120*time.Millisecond, func(p float32) {
		tooltip.alpha = from + (to-from)*p
		tooltip.bg.FillColor = withAlpha(colRaised, tooltip.alpha)
		tooltip.bg.StrokeColor = withAlpha(colPopBorder, tooltip.alpha)
		tooltip.bg.Refresh()
		step := quantize(tooltip.alpha, 4)
		for i, line := range tooltip.lines {
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
			tooltip.layer.Hide()
		}
	})
	tooltip.anim.Curve = fyne.AnimationEaseOut
	tooltip.anim.Start()
}

// ---------------------------------------------------------------------------
// Window chrome: drag region and resize edges for the undecorated window.

type dragArea struct {
	widget.BaseWidget
	window fyne.Window
	last   time.Time
	at     fyne.Position
}

func newDragArea(window fyne.Window) *dragArea {
	area := &dragArea{window: window}
	area.ExtendBaseWidget(area)
	return area
}

func (area *dragArea) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	near := e.AbsolutePosition.Subtract(area.at)
	if time.Since(area.last) < 400*time.Millisecond && near.X*near.X+near.Y*near.Y < 36 {
		area.last = time.Time{}
		windowToggleMaximize(area.window)
		return
	}
	area.last, area.at = time.Now(), e.AbsolutePosition
	windowMoveResize(area.window, edgeMove)
}

func (area *dragArea) MouseUp(*desktop.MouseEvent) {}

func (area *dragArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

type resizeEdge struct {
	widget.BaseWidget
	window    fyne.Window
	direction int
}

func (edge *resizeEdge) MouseDown(m *desktop.MouseEvent) {
	if m.Button == desktop.MouseButtonPrimary {
		windowMoveResize(edge.window, edge.direction)
	}
}

func (edge *resizeEdge) MouseUp(*desktop.MouseEvent) {}

func (edge *resizeEdge) Cursor() desktop.Cursor {
	switch edge.direction {
	case edgeLeft, edgeRight:
		return desktop.HResizeCursor
	}
	return desktop.VResizeCursor
}

func (edge *resizeEdge) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

type edgesLayout struct{ thickness float32 }

func (layoutSpec edgesLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (layoutSpec edgesLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	t, c := layoutSpec.thickness, layoutSpec.thickness*2
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

func resizeEdges(window fyne.Window) fyne.CanvasObject {
	edges := container.New(edgesLayout{thickness: 4})
	for _, direction := range []int{edgeTop, edgeBottom, edgeLeft, edgeRight, edgeTopLeft, edgeTopRight, edgeBottomLeft, edgeBottomRight} {
		edge := &resizeEdge{window: window, direction: direction}
		edge.ExtendBaseWidget(edge)
		edges.Add(edge)
	}
	return edges
}

func windowControls(window fyne.Window, quit func()) fyne.CanvasObject {
	minimize := newRow("", iconMinimize, styleWindow, func() { windowMinimize(window) })
	maximize := newRow("", iconMaximize, styleWindow, func() { windowToggleMaximize(window) })
	closeButton := newRow("", iconClose, styleWindow, quit)
	closeButton.hoverFill = colClose
	return container.New(layout.NewCustomPaddedHBoxLayout(2), minimize, maximize, closeButton)
}

// tapArea makes what is under it clickable, without drawing anything.
type tapArea struct {
	widget.BaseWidget
	onTap func()
}

func newTapArea(onTap func()) *tapArea {
	area := &tapArea{onTap: onTap}
	area.ExtendBaseWidget(area)
	return area
}

func (area *tapArea) Tapped(*fyne.PointEvent) { area.onTap() }

func (area *tapArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
