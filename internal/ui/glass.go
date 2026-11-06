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

	"1loop/internal/platform"
)

// glassWidth is the width the backdrop is blurred at: small enough to be
// cheap, large enough that its shapes still read through the glass.
const glassWidth = 240

// frostedBackdrop is the window as it is on screen, blurred and tinted for
// text to stay readable over it; nil when the canvas cannot be read.
func frostedBackdrop(c fyne.Canvas) *image.NRGBA {
	if c == nil || c.Size().Width <= 0 {
		return nil
	}
	started := time.Now()
	var shot image.Image
	if now, ok := c.(interface{ CaptureNow() image.Image }); ok { // third_party/fyne
		shot = now.CaptureNow()
	} else {
		shot = c.Capture()
	}
	if shot == nil {
		return nil
	}
	b := shot.Bounds()
	if b.Dx() < 8 || b.Dy() < 8 {
		return nil
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
	return small
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
