// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"testing"

	"fyne.io/fyne/v2"
)

// A shadow fades out evenly and smoothly past its shape's edge: no step
// where four rectangles used to end, the same on every side.
func TestSoftShadowFadesSmoothly(t *testing.T) {
	scale := float32(1.4)
	size := fyne.NewSize(200, 80)
	full := size.AddWidthHeight(2*shadowMargin, 2*shadowMargin)
	img := image.NewNRGBA(image.Rect(0, 0, int(full.Width*scale), int(full.Height*scale)))
	paintSoftShape(img, scale, size, 18, shadowInk, 1)
	cx, cy := img.Bounds().Dx()/2, img.Bounds().Dy()/2
	edge := int(shadowMargin * scale)
	// from the left edge outwards, and from the top edge upwards
	left, top := []uint8{}, []uint8{}
	for i := 0; i < edge; i++ {
		left = append(left, img.NRGBAAt(edge-1-i, cy).A)
		top = append(top, img.NRGBAAt(cx, edge-1-i).A)
	}
	if left[0] < 40 || left[len(left)-1] > 2 {
		t.Fatalf("no fade out: %v", left)
	}
	for i := 1; i < len(left); i++ {
		if left[i] > left[i-1] {
			t.Fatalf("the fade goes back up: %v", left)
		}
		if int(left[i-1])-int(left[i]) > 12 {
			t.Fatalf("a step in the fade: %v", left)
		}
		if d := int(left[i]) - int(top[i]); d < -1 || d > 1 {
			t.Fatalf("the sides differ: left %v top %v", left, top)
		}
	}
	if a := img.NRGBAAt(cx, cy).A; a != 71 {
		t.Fatalf("inside %d, want 71: as dark as the four rectangles were", a)
	}
}

func BenchmarkSoftShadowDialog(b *testing.B) {
	dropShadow := newShadow(2.4)
	dropShadow.layout(fyne.Position{}, fyne.NewSize(640, 520), 14)
	full := dropShadow.raster.Size()
	for i := 0; i < b.N; i++ {
		dropShadow.cache = nil
		dropShadow.draw(int(full.Width*1.4), int(full.Height*1.4))
	}
}
