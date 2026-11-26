package gl

import (
	"testing"

	"fyne.io/fyne/v2"
)

// A texture's rectangle keeps the size its texture is drawn at, wherever
// it is: at 1.25 a square icon was drawn 26×25 at some positions (1loop).
func TestRoundToPixelCoordsKeepsTheTextureSize(t *testing.T) {
	const scale = 1.25
	want := float32(26) / scale // round(20.8 × 1.25) pixels
	for i := 0; i < 40; i++ {
		at := fyne.NewPos(float32(i)*0.3, float32(i)*0.7)
		size, pos := roundToPixelCoords(fyne.NewSquareSize(20.8), at, scale)
		if size.Width != want || size.Height != want {
			t.Fatalf("at %v: size %v, want %v square", at, size, want)
		}
		if x, y := pos.X*scale, pos.Y*scale; x != float32(int(x)) || y != float32(int(y)) {
			t.Fatalf("at %v: position %v is not on a pixel", at, pos)
		}
	}
}
