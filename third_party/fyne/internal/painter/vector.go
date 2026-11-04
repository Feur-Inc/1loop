package painter

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// VectorPad returns the number of additional points that should be added around a texture.
// This is to accommodate overflow caused by stroke and line endings etc.
// THe result is in fyne.Size type coordinates and should be scaled for output.
func VectorPad(obj fyne.CanvasObject) float32 {
	switch co := obj.(type) {
	case *canvas.Circle:
		if co.StrokeWidth > 0 && co.StrokeColor != nil {
			return co.StrokeWidth + 2
		}
		return 1 // anti-alias on circle fill
	case *canvas.Line:
		if co.StrokeWidth > 0 {
			return co.StrokeWidth + 2
		}
	case *canvas.Polygon:
		if co.StrokeWidth > 0 && co.StrokeColor != nil {
			return co.StrokeWidth + 2
		}
	case *canvas.Rectangle:
		if co.StrokeWidth > 0 && co.StrokeColor != nil {
			return co.StrokeWidth + 2
		}
	case *canvas.Text:
		if co.TextStyle.Italic {
			return co.TextSize / 5 // make sure that even a 20% lean does not overflow
		}
	case *canvas.Arc:
		if co.StrokeWidth > 0 && co.StrokeColor != nil {
			return co.StrokeWidth + 2
		}
	}

	return 0
}

// TextBottomPad is how far below its measured height a text's texture
// reaches. The height ends at the font's descent, where some fonts put the
// underscore (DejaVu Sans Mono, Fyne's monospace): with the baseline moved
// down to a whole pixel it fell outside the texture, and "set_tier" read as
// "set tier". The text is laid out by its height; only its drawing has room.
func TextBottomPad(text *canvas.Text) float32 {
	return float32(math.Ceil(float64(text.TextSize) / 6))
}
