// SPDX-License-Identifier: MPL-2.0

package ui

// Blurred elements (issue #464): a theme file's [blur] gives the sidebar,
// the side panel, the composer, the pane headers, menus and dialogs a blur
// radius; what lies behind them (a shader or picture background, the
// window) shows through, blurred on the GPU by third_party/fyne's
// canvas.Blur, under a translucent tint ([blur_tint], or the element's own
// colour). Where GL cannot blur (Mesa on the CPU, #416) the elements keep
// their plain, opaque look.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"1loop/internal/palette"
)

var (
	// themeBlur and themeBlurTint are the active theme's [blur] and [blur_tint].
	themeBlur     map[string]float64
	themeBlurTint map[string]color.NRGBA
	// elementGlasses are the long-lived blurred backgrounds, synced on each
	// theme change.
	elementGlasses []*elementGlass
)

// elementOpacity is how opaque an element's own colour stays over its blur
// when the theme gives it no tint.
const elementOpacity = 0.6

func init() {
	canvas.BlurChanged = func() { fyne.Do(syncElementGlasses) }
}

// blurRadius is the blur an element has now, 0 for none.
func blurRadius(element string) float32 {
	if !canvas.BlurSupported() {
		return 0
	}
	return float32(themeBlur[element])
}

// blurTint is laid over an element's blur: the theme's, or base translucent.
func blurTint(element string, base color.NRGBA, opacity float32) color.NRGBA {
	if tint, ok := themeBlurTint[element]; ok {
		return tint
	}
	base.A = uint8(float32(base.A) * opacity)
	return base
}

// setThemeBlur takes the active theme file's blur, or none.
func setThemeBlur(file palette.ThemeFile, ok bool) {
	themeBlur, themeBlurTint = nil, nil
	if ok {
		themeBlur, themeBlurTint = file.Blur, file.BlurTint
	}
	syncElementGlasses()
}

// elementGlass is an element's background that a theme can blur.
type elementGlass struct {
	element string
	blur    *canvas.Blur
	fill    *canvas.Rectangle
	base    *color.NRGBA // the token the fill shows when not blurred
	tinted  bool
}

// blurBehind lays a blur under fill, an element's background, for the
// theme to turn on.
func blurBehind(element string, fill *canvas.Rectangle, base *color.NRGBA) fyne.CanvasObject {
	glass := &elementGlass{element: element, blur: canvas.NewBlur(0), fill: fill, base: base}
	glass.blur.Hide()
	elementGlasses = append(elementGlasses, glass)
	glass.sync()
	return container.New(glassLayout{glass}, glass.blur, fill)
}

func syncElementGlasses() {
	for _, glass := range elementGlasses {
		glass.sync()
	}
}

func (glass *elementGlass) sync() {
	radius := blurRadius(glass.element)
	if radius <= 0 {
		glass.blur.Hide()
		if glass.tinted {
			glass.tinted = false
			glass.fill.FillColor = *glass.base
			glass.fill.Refresh()
		}
		return
	}
	glass.tinted = true
	glass.blur.Radius = radius
	glass.copyCorners()
	glass.fill.FillColor = blurTint(glass.element, *glass.base, elementOpacity)
	glass.blur.Show()
	glass.blur.Refresh()
	glass.fill.Refresh()
}

func (glass *elementGlass) copyCorners() {
	b, f := glass.blur, glass.fill
	b.CornerRadius, b.TopLeftCornerRadius, b.TopRightCornerRadius = f.CornerRadius, f.TopLeftCornerRadius, f.TopRightCornerRadius
	b.BottomLeftCornerRadius, b.BottomRightCornerRadius = f.BottomLeftCornerRadius, f.BottomRightCornerRadius
}

// glassLayout lays the blur and its fill over one another, the blur with
// the fill's corners (the roundness setting changes them).
type glassLayout struct{ glass *elementGlass }

func (l glassLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.glass.copyCorners()
	for _, object := range objects {
		object.Move(fyne.Position{})
		object.Resize(size)
	}
}

func (l glassLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return l.glass.fill.MinSize()
}

// canvasBlur is a blur, hidden until a theme asks for it.
func canvasBlur() *canvas.Blur {
	blur := canvas.NewBlur(0)
	blur.Hide()
	return blur
}
