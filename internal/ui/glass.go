// SPDX-License-Identifier: MPL-2.0

package ui

// Frosted glass: dialogs open over a blurred copy of the window, and menus
// and tooltips are panes of glass over what they cover. Fyne draws no
// backdrop filter, so the window is read once as the overlay opens, shrunk
// and blurred on the CPU (a few milliseconds whatever its size), and the
// GPU stretches it back smoothly, which blurs it further. What moves behind
// an open overlay is not followed: menus and dialogs are short-lived.

import (
	"image"
	"image/color"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"1loop/internal/palette"
	"1loop/internal/platform"
)

// glassWidth is the width the backdrop is blurred at: small enough to be
// cheap, large enough that its shapes still read through the glass.
const glassWidth = 240

// frostedBackdrop is the window as it is on screen, blurred and tinted for
// text to stay readable over it; nil when the canvas cannot be read.
func frostedBackdrop(c fyne.Canvas) (*image.NRGBA, image.Image) {
	// In the browser, Fyne's WebGL ReadPixels panics (gl-js hands JS a Go
	// slice): the glass there is the tint alone.
	if c == nil || c.Size().Width <= 0 || runtime.GOOS == "js" {
		return nil, nil
	}
	started := time.Now()
	var shot image.Image
	if now, ok := c.(interface{ CaptureNow() image.Image }); ok { // third_party/fyne
		shot = now.CaptureNow()
	} else {
		shot = c.Capture()
	}
	if shot == nil {
		return nil, nil
	}
	b := shot.Bounds()
	if b.Dx() < 8 || b.Dy() < 8 {
		return nil, nil
	}
	step := max(1, b.Dx()/glassWidth)
	w, h := b.Dx()/step, b.Dy()/step
	small := image.NewNRGBA(image.Rect(0, 0, w, h))
	half := max(1, step/2)
	// The capture's own pixels when it gives them (third_party/fyne): reading
	// through At allocates a colour per sample.
	pix, pw, ph := []uint8(nil), 0, 0
	if raw, ok := shot.(interface{ RawRGBA() ([]uint8, int, int) }); ok {
		pix, pw, ph = raw.RawRGBA()
	}
	sample := func(x, y int) (int, int, int) {
		if pix != nil {
			o := ((ph-y-1)*pw + x) * 4 // bottom-up rows
			return int(pix[o]), int(pix[o+1]), int(pix[o+2])
		}
		cr, cg, cb, _ := shot.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return int(cr >> 8), int(cg >> 8), int(cb >> 8)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Four samples a cell: enough to keep thin lines from flickering in and out.
			var r, g, bl int
			for _, d := range [4][2]int{{0, 0}, {half, 0}, {0, half}, {half, half}} {
				sr, sg, sb := sample(min(x*step+d[0], b.Dx()-1), min(y*step+d[1], b.Dy()-1))
				r, g, bl = r+sr, g+sg, bl+sb
			}
			i := small.PixOffset(x, y)
			small.Pix[i], small.Pix[i+1], small.Pix[i+2], small.Pix[i+3] = uint8(r/4), uint8(g/4), uint8(bl/4), 0xff
		}
	}
	for pass := 0; pass < 3; pass++ { // three box blurs make a near-Gaussian one
		boxBlur(small, 3)
	}
	vibrancy(small, 1.35, glassBrightness(0.72))
	platform.Devf("perf", "glass backdrop %dx%d in %s", w, h, time.Since(started).Round(100*time.Microsecond))
	return small, shot
}

// windowControlsCut copies the window buttons out of shot, sharp, so a
// dialog can keep them in view over its frosted backdrop.
func windowControlsCut(c fyne.Canvas, shot image.Image) image.Image {
	if windowControlsBox == nil || !windowControlsBox.Visible() || c.Size().Width <= 0 {
		return nil
	}
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(windowControlsBox)
	scale := float32(shot.Bounds().Dx()) / c.Size().Width
	r := image.Rect(int(at.X*scale), int(at.Y*scale), int((at.X+windowControlsBox.Size().Width)*scale), int((at.Y+windowControlsBox.Size().Height)*scale)).Intersect(shot.Bounds())
	if r.Empty() {
		return nil
	}
	cut := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			cut.Set(x, y, shot.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return cut
}

// boxBlur blurs img in place, rows then columns, over 2*radius+1 pixels,
// with a running sum: its cost does not grow with the radius.
func boxBlur(img *image.NRGBA, radius int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	line := make([]int32, 3*max(w, h))
	blur := func(n, first, stride int) {
		for i, o := 0, first; i < n; i, o = i+1, o+stride {
			line[3*i], line[3*i+1], line[3*i+2] = int32(img.Pix[o]), int32(img.Pix[o+1]), int32(img.Pix[o+2])
		}
		var r, g, b, count int32
		for k := 0; k < min(radius, n); k++ {
			r, g, b, count = r+line[3*k], g+line[3*k+1], b+line[3*k+2], count+1
		}
		for i, o := 0, first; i < n; i, o = i+1, o+stride {
			if in := i + radius; in < n {
				r, g, b, count = r+line[3*in], g+line[3*in+1], b+line[3*in+2], count+1
			}
			if out := i - radius - 1; out >= 0 {
				r, g, b, count = r-line[3*out], g-line[3*out+1], b-line[3*out+2], count-1
			}
			img.Pix[o], img.Pix[o+1], img.Pix[o+2] = uint8(r/count), uint8(g/count), uint8(b/count)
		}
	}
	for y := 0; y < h; y++ {
		blur(w, img.PixOffset(0, y), 4)
	}
	for x := 0; x < w; x++ {
		blur(h, img.PixOffset(x, 0), img.Stride)
	}
}

// vibrancy lifts the colours that show through (as frosted glass does) and
// darkens them, so text over the glass keeps its contrast.
func vibrancy(img *image.NRGBA, saturation, brightness float32) {
	for i := 0; i < len(img.Pix); i += 4 {
		r, g, b := float32(img.Pix[i]), float32(img.Pix[i+1]), float32(img.Pix[i+2])
		grey := 0.299*r + 0.587*g + 0.114*b
		clamp := func(v float32) uint8 { return uint8(max(0, min(255, v))) }
		img.Pix[i] = clamp((grey + (r-grey)*saturation) * brightness)
		img.Pix[i+1] = clamp((grey + (g-grey)*saturation) * brightness)
		img.Pix[i+2] = clamp((grey + (b-grey)*saturation) * brightness)
	}
}

// glassTint is laid over the frosted backdrop of a menu or tooltip: the
// popover colour, translucent, so the blurred window shows through it.
func glassTint(base color.NRGBA, opacity float32) color.NRGBA {
	return withAlpha(color.NRGBA{R: base.R, G: base.G, B: base.B, A: 0x80}, opacity)
}

// glassEdge outlines a pane of glass: a hairline of light rather than a
// border (of shade, on the light theme).
var glassEdge = palette.Dark.GlassEdge

// glassBrightness is how much the frosted window is brightened (above 1)
// or darkened for the dark theme; on the light one it is brightened
// rather than darkened, so dark text keeps its contrast over it.
func glassBrightness(dark float32) float32 {
	if themeTokens.Dark {
		return dark
	}
	return max(dark, 1.12)
}

// dimAlpha is the black laid over the window behind a dialog: lighter on
// the light theme, where a heavy dim turns the window grey.
func dimAlpha(dark float32) float32 {
	if themeTokens.Dark {
		return dark
	}
	return dark * 0.55
}

// footerGlass lays the composer over the conversation's end: above it the
// conversation fades into the background, which is opaque under it, so the
// text behind never muddles the composer's own lines.
type footerGlass struct {
	fade  *fyne.Container   // the conversation fading out above the composer
	floor *canvas.Rectangle // the background under it

	more  bool    // more of the conversation lies below: the fade is wanted
	shown float32 // the fade's opacity, 0 at the conversation's end
	anim  *fyne.Animation
}

const glassFade = 72 // how far above the composer the conversation fades out

// fadeStrip is how tall each step of the fade is.
const fadeStrip = 2

func newFooterGlass() *footerGlass {
	// Strips of the background, more opaque towards the composer, rather
	// than a canvas.LinearGradient: Fyne computes a gradient's pixels with
	// truncated, premultiplied integers, so over the background itself it
	// showed as stripes one level darker. A rectangle's colour reaches the
	// shader exactly, and the background over itself stays the background.
	strips := make([]fyne.CanvasObject, glassFade/fadeStrip)
	for i := range strips {
		strips[i] = canvas.NewRectangle(color.Transparent)
	}
	glass := &footerGlass{fade: container.New(fadeLayout{}, strips...), floor: canvas.NewRectangle(colBackground)}
	glass.paint(0) // nothing below yet: veil brings it in
	return glass
}

// veil shows the fade while more of the conversation lies below the
// composer, and clears it at the conversation's end: nothing is hidden
// there, and the fade only dimmed the last message.
func (glass *footerGlass) veil(more bool) {
	if glass == nil || glass.more == more { // nil before the window is built
		return
	}
	glass.more = more
	if glass.anim != nil {
		glass.anim.Stop()
	}
	from, to := glass.shown, float32(0)
	if more {
		to = 1
	}
	glass.anim = motion(120*time.Millisecond, fyne.AnimationEaseOut, func(p float32) { glass.paint(from + (to-from)*p) })
}

// paint draws the fade at an opacity from 0 to 1. Hidden at 0, so nothing
// is drawn for it at the conversation's end.
func (glass *footerGlass) paint(opacity float32) {
	glass.shown = opacity
	if opacity <= 0 {
		glass.fade.Hide()
		return
	}
	steps := len(glass.fade.Objects)
	for i, strip := range glass.fade.Objects {
		alpha := uint8((float32(i) + 0.5) / float32(steps) * 0xff * opacity)
		strip.(*canvas.Rectangle).FillColor = color.NRGBA{R: colBackground.R, G: colBackground.G, B: colBackground.B, A: alpha}
	}
	glass.fade.Show()
	glass.fade.Refresh()
}

// fadeLayout stacks the fade's strips from top to bottom.
type fadeLayout struct{}

func (fadeLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (fadeLayout) Layout(strips []fyne.CanvasObject, size fyne.Size) {
	height := size.Height / float32(len(strips))
	for i, strip := range strips {
		strip.Move(fyne.NewPos(0, float32(i)*height))
		strip.Resize(fyne.NewSize(size.Width, height))
	}
}

// glassFooterLayout lets the conversation fill everything and floats the
// composer over its end; the conversation's end gets as much room
// (footerSpace) so its last message can scroll above the composer.
// Objects: the conversation, the fade, the floor, the composer.
type glassFooterLayout struct {
	ui    *desktopUI
	glass *footerGlass
}

func (layoutSpec *glassFooterLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	view, footer := o[0].MinSize(), o[3].MinSize()
	// The open editor takes the room the conversation has: it never asks
	// the window for more.
	if editor := layoutSpec.ui.editor; editor != nil {
		footer.Height -= editor.extra
	}
	return fyne.NewSize(fyne.Max(view.Width, footer.Width), view.Height+footer.Height)
}

func (layoutSpec *glassFooterLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	glass := layoutSpec.glass
	layoutSpec.ui.editor.fit(size.Height, o[3].MinSize().Height)
	footer := o[3].MinSize()
	top := size.Height - footer.Height
	o[0].Move(fyne.Position{})
	o[0].Resize(size)
	glass.fade.Move(fyne.NewPos(0, top-glassFade))
	glass.fade.Resize(fyne.NewSize(size.Width, glassFade))
	glass.floor.Move(fyne.NewPos(0, top))
	glass.floor.Resize(fyne.NewSize(size.Width, footer.Height))
	o[3].Move(fyne.NewPos(0, top))
	o[3].Resize(fyne.NewSize(size.Width, footer.Height))
	// The open editor covers the conversation: its column keeps its room.
	if space := layoutSpec.ui.footerSpace; space != nil && !layoutSpec.ui.editor.active() && space.MinSize().Height != footer.Height {
		space.SetMinSize(fyne.NewSize(1, footer.Height))
		fyne.Do(layoutSpec.ui.relayoutColumn) // after this layout: it lays the column out again
	}
}

// pageCurtain fades a page in as it takes the main area's place (Settings,
// a section, a chat): the background laid over it clears in 220 ms. Hidden
// at rest, so nothing is drawn for it; taps pass through a rectangle.
type pageCurtain struct {
	cover *canvas.Rectangle
	anim  *fyne.Animation
}

func newPageCurtain() *pageCurtain {
	curtain := &pageCurtain{cover: canvas.NewRectangle(colBackground)}
	curtain.cover.Hide()
	return curtain
}

func (curtain *pageCurtain) reveal() {
	if curtain == nil { // before the window is built
		return
	}
	if curtain.anim != nil {
		curtain.anim.Stop()
	}
	curtain.cover.FillColor = colBackground
	curtain.cover.Show()
	curtain.cover.Refresh()
	curtain.anim = motion(200*time.Millisecond, fyne.AnimationEaseOut, func(p float32) {
		curtain.cover.FillColor = withAlpha(colBackground, 1-p)
		if p >= 1 {
			curtain.cover.Hide()
		}
		curtain.cover.Refresh()
	})
}
