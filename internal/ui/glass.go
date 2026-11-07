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
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"1loop/internal/platform"
)

// glassWidth is the width the backdrop is blurred at: small enough to be
// cheap, large enough that its shapes still read through the glass.
const glassWidth = 240

// frostedBackdrop is the window as it is on screen, blurred and tinted for
// text to stay readable over it; nil when the canvas cannot be read.
func frostedBackdrop(c fyne.Canvas) (*image.NRGBA, image.Image) {
	if c == nil || c.Size().Width <= 0 {
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
	vibrancy(small, 1.35, 0.72)
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
	return withAlpha(color.NRGBA{R: base.R, G: base.G, B: base.B, A: 0xb8}, opacity)
}

// footerGlass is the frosted band the composer floats on: the conversation
// passes under it, blurred, and fades into it at its top. The blur is taken
// again only once what is behind it has changed and settled (a scroll, a new
// message, a resize), at most every glassEvery while text streams in: a
// frame painted for it costs a few milliseconds, nothing in between.
type footerGlass struct {
	ui      *desktopUI
	footer  fyne.CanvasObject
	band    *canvas.Image
	tint    *canvas.Rectangle
	fade    *canvas.LinearGradient
	timer   *time.Timer
	first   time.Time // the first change not yet frosted
	pending bool
}

const (
	glassSettle = 140 * time.Millisecond // after the last change
	glassEvery  = 700 * time.Millisecond // while changes keep coming
	glassFade   = 44                     // how far above the band the conversation fades into it
)

func newFooterGlass(ui *desktopUI, footer fyne.CanvasObject) *footerGlass {
	glass := &footerGlass{ui: ui, footer: footer, band: canvas.NewImageFromImage(nil),
		tint: canvas.NewRectangle(color.NRGBA{R: colBackground.R, G: colBackground.G, B: colBackground.B, A: 0x48}),
		fade: canvas.NewVerticalGradient(color.Transparent, color.NRGBA{R: colBackground.R, G: colBackground.G, B: colBackground.B, A: 0xd0})}
	glass.band.FillMode, glass.band.ScaleMode = canvas.ImageFillStretch, canvas.ImageScaleSmooth
	return glass
}

// changed asks for a new frost once things settle.
func (glass *footerGlass) changed() {
	now := time.Now()
	if !glass.pending {
		glass.pending, glass.first = true, now
	}
	wait := glassSettle
	if left := glassEvery - now.Sub(glass.first); left < wait {
		wait = max(0, left)
	}
	if glass.timer != nil {
		glass.timer.Stop()
	}
	glass.timer = time.AfterFunc(wait, func() { fyne.Do(glass.frost) })
}

// frost reads what is behind the band, without the band and the composer.
func (glass *footerGlass) frost() {
	glass.pending = false
	c := glass.ui.window.Canvas()
	now, ok := c.(interface{ CaptureNow() image.Image })
	size := glass.band.Size()
	if !ok || size.Width <= 0 || size.Height <= 0 || !glass.band.Visible() {
		return
	}
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(glass.band)
	glass.footer.Hide()
	glass.band.Hide()
	glass.tint.Hide()
	glass.fade.Hide()
	shot := now.CaptureNow()
	glass.footer.Show()
	glass.band.Show()
	glass.tint.Show()
	glass.fade.Show()
	if shot == nil {
		return
	}
	scale := float32(shot.Bounds().Dx()) / c.Size().Width
	r := image.Rect(int(at.X*scale), int(at.Y*scale), int((at.X+size.Width)*scale), int((at.Y+size.Height)*scale)).Intersect(shot.Bounds())
	if r.Dx() < 8 || r.Dy() < 8 {
		return
	}
	started := time.Now()
	glass.band.Image = blurRegion(shot, r, 4)
	glass.band.Refresh()
	platform.Devf("perf", "composer glass %dx%d in %s", r.Dx(), r.Dy(), time.Since(started).Round(100*time.Microsecond))
}

// blurRegion is r of shot, shrunk by step, blurred and tinted as frosted glass.
func blurRegion(shot image.Image, r image.Rectangle, step int) *image.NRGBA {
	w, h := max(1, r.Dx()/step), max(1, r.Dy()/step)
	small := image.NewNRGBA(image.Rect(0, 0, w, h))
	pix, pw, ph := []uint8(nil), 0, 0
	if raw, ok := shot.(interface{ RawRGBA() ([]uint8, int, int) }); ok {
		pix, pw, ph = raw.RawRGBA()
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx, sy := r.Min.X+x*step+step/2, r.Min.Y+y*step+step/2
			i := small.PixOffset(x, y)
			if pix != nil {
				o := ((ph-sy-1)*pw + sx) * 4
				copy(small.Pix[i:i+3], pix[o:o+3])
			} else {
				cr, cg, cb, _ := shot.At(sx, sy).RGBA()
				small.Pix[i], small.Pix[i+1], small.Pix[i+2] = uint8(cr>>8), uint8(cg>>8), uint8(cb>>8)
			}
			small.Pix[i+3] = 0xff
		}
	}
	for pass := 0; pass < 3; pass++ {
		boxBlur(small, 4)
	}
	vibrancy(small, 1.5, 1.05)
	return small
}

// glassFooterLayout lets the conversation fill everything and floats the
// composer at the bottom, on the frosted band; the conversation's end gets
// as much room (footerSpace) so its last message can scroll above it.
type glassFooterLayout struct {
	ui    *desktopUI
	glass *footerGlass
	last  fyne.Size
}

func (layoutSpec *glassFooterLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	view, footer := o[0].MinSize(), o[4].MinSize()
	return fyne.NewSize(fyne.Max(view.Width, footer.Width), view.Height+footer.Height)
}

func (layoutSpec *glassFooterLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	glass := layoutSpec.glass
	footer := o[4].MinSize()
	top := size.Height - footer.Height
	o[0].Move(fyne.Position{})
	o[0].Resize(size)
	for _, band := range []fyne.CanvasObject{glass.band, glass.tint} {
		band.Move(fyne.NewPos(0, top))
		band.Resize(fyne.NewSize(size.Width, footer.Height))
	}
	glass.fade.Move(fyne.NewPos(0, top-glassFade))
	glass.fade.Resize(fyne.NewSize(size.Width, glassFade))
	o[4].Move(fyne.NewPos(0, top))
	o[4].Resize(fyne.NewSize(size.Width, footer.Height))
	if space := layoutSpec.ui.footerSpace; space != nil && space.MinSize().Height != footer.Height {
		space.SetMinSize(fyne.NewSize(1, footer.Height))
		fyne.Do(layoutSpec.ui.relayoutColumn) // after this layout: it lays the column out again
	}
	if size != layoutSpec.last || footer.Height != glass.band.Size().Height {
		layoutSpec.last = size
		glass.changed()
	}
}

// shineLayout lays a glass card's light along its top edge: a hairline
// inset from the rounded corners.
type shineLayout struct{}

func (shineLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (shineLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	o[0].Move(fyne.NewPos(18, 1)) // clear of the rounded corners
	o[0].Resize(fyne.NewSize(fyne.Max(0, size.Width-36), 1))
}
