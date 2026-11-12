// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// glPixels returns the device pixel edges the GL painter draws an object
// between: like Fyne's vecRectCoordsWithPad and scaleRectCoords, it snaps
// the position and the size to the pixel grid each on its own, then scales.
func glPixels(pos, size, scale float32) (float64, float64) {
	snap := func(v float32) float32 { return float32(math.Round(float64(v*scale))) / scale }
	start, length := snap(pos), snap(size)
	return math.Round(float64(start * scale)), math.Round(float64((start + length) * scale))
}

// The white thumb sits in the middle of the amber ring around it, at rest,
// hovered, half way through the hover fade and while dragged, on a 1.25
// scale screen as on others, wherever the popover puts the slider (#367).
func TestStepSliderThumbIsCentredInItsRing(t *testing.T) {
	test.NewTempApp(t)
	off := animationsOff
	animationsOff = true
	t.Cleanup(func() { animationsOff = off })
	slider := newStepSlider(4, 1, nil)
	window := test.NewTempWindow(t, slider)
	window.Resize(fyne.NewSize(120, 260))
	slider.Resize(fyne.NewSize(sliderWidth, 202))
	renderer := test.TempWidgetRenderer(t, slider).(*stepSliderRenderer)

	states := []struct {
		name  string
		apply func()
	}{
		{"at rest", func() { slider.hover, slider.dragging = 0, false }},
		{"half hovered", func() { slider.hover, slider.dragging = 0.4, false }},
		{"hovered", func() { slider.hover, slider.dragging = 1, false }},
		{"dragged", func() { slider.hover, slider.dragging = 1, true }},
	}
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		window.Canvas().(test.WindowlessCanvas).SetScale(scale)
		for _, state := range states {
			state.apply()
			for _, stop := range []float32{0, 0.37, 1, 2.5, 3} {
				slider.pos = stop
				slider.Refresh()
				// The popover's columns put the slider at fractional points.
				for _, at := range []float32{0, 0.3, 0.5, 0.77, 113.9} {
					origin := fyne.NewPos(at, at*1.7)
					ring, thumb := renderer.fill, renderer.thumb
					rx1, rx2 := glPixels(origin.X+ring.Position().X, ring.Size().Width, scale)
					ry1, _ := glPixels(origin.Y+ring.Position().Y, ring.Size().Height, scale)
					tx1, tx2 := glPixels(origin.X+thumb.Position().X, thumb.Size().Width, scale)
					ty1, ty2 := glPixels(origin.Y+thumb.Position().Y, thumb.Size().Height, scale)
					// The ring's round top is as tall as the ring is wide.
					ringX, ringY := (rx1+rx2)/2, ry1+(rx2-rx1)/2
					thumbX, thumbY := (tx1+tx2)/2, (ty1+ty2)/2
					if math.Abs(thumbX-ringX) > 0.01 || math.Abs(thumbY-ringY) > 0.01 {
						t.Fatalf("scale %v, %s, stop %v, slider at %v: thumb centre (%v, %v) px, ring centre (%v, %v) px",
							scale, state.name, stop, origin, thumbX, thumbY, ringX, ringY)
					}
				}
			}
		}
	}
}
