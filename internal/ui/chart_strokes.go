// SPDX-License-Identifier: MPL-2.0

package ui

// The usage chart's lines, drawn by 1loop in one picture rather than as
// Fyne lines (#445). Fyne rounds both ends of every line to the pixel, so
// the 5 px dashes of a steep line each turned a little: the line zigzagged.
// Here every dash sits exactly on its line, smoothed, at the screen's own
// resolution.

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"golang.org/x/image/vector"
)

// A dashed line is dashes of the same length and gap, in pixels, however
// long it is: as many as fit, as in its legend key.
const (
	dashLength = 6
	dashGap    = 4
)

// dashSpans are where the dashes of a line length long start and end,
// along it: the last one cut at the end, a last speck under 1 px left out.
func dashSpans(length float32) [][2]float32 {
	var spans [][2]float32
	for a := float32(0); a < length; a += dashLength + dashGap {
		b := min(a+dashLength, length)
		if b-a >= 1 {
			spans = append(spans, [2]float32{a, b})
		}
	}
	return spans
}

// stroke is one line of the chart, in the chart's coordinates.
type stroke struct {
	from, to fyne.Position
	ink      color.Color
	width    float32
	dashed   bool
	round    bool // round ends, so that the segments of a polyline join
}

// chartStrokes draws strokes into a raster as large as the chart.
type chartStrokes struct {
	raster  *canvas.Raster
	size    fyne.Size
	strokes []stroke
}

func newChartStrokes() *chartStrokes {
	ink := &chartStrokes{}
	ink.raster = canvas.NewRaster(ink.draw)
	return ink
}

// set replaces the strokes, for a chart size large, and redraws them.
func (ink *chartStrokes) set(size fyne.Size, strokes []stroke) {
	ink.size, ink.strokes = size, strokes
	ink.raster.Move(fyne.Position{})
	ink.raster.Resize(size)
	ink.raster.Refresh()
}

// draw paints the strokes at w × h pixels.
func (ink *chartStrokes) draw(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if ink.size.Width <= 0 || ink.size.Height <= 0 || w <= 0 || h <= 0 {
		return img
	}
	scale := float32(w) / ink.size.Width
	paintStrokes(img, ink.strokes, scale)
	return img
}

// paintStrokes draws strokes on img, chart coordinates times scale.
func paintStrokes(img draw.Image, strokes []stroke, scale float32) {
	bounds := img.Bounds()
	var r vector.Rasterizer
	for _, s := range strokes {
		r.Reset(bounds.Dx(), bounds.Dy())
		ax, ay := s.from.X*scale, s.from.Y*scale
		dx, dy := (s.to.X-s.from.X)*scale, (s.to.Y-s.from.Y)*scale
		length := float32(math.Hypot(float64(dx), float64(dy)))
		half := s.width * scale / 2
		if length < 0.01 {
			continue
		}
		ux, uy := dx/length, dy/length
		quad := func(a, b float32) {
			nx, ny := -uy*half, ux*half
			x1, y1 := ax+ux*a, ay+uy*a
			x2, y2 := ax+ux*b, ay+uy*b
			r.MoveTo(x1+nx, y1+ny)
			r.LineTo(x2+nx, y2+ny)
			r.LineTo(x2-nx, y2-ny)
			r.LineTo(x1-nx, y1-ny)
			r.ClosePath()
		}
		if s.dashed {
			for _, span := range dashSpans(length / scale) {
				quad(span[0]*scale, span[1]*scale)
			}
		} else {
			quad(0, length)
		}
		if s.round {
			disc(&r, ax, ay, half)
			disc(&r, ax+dx, ay+dy, half)
		}
		r.Draw(img, bounds, image.NewUniform(s.ink), image.Point{})
	}
}

// disc adds a small circle at x, y, as a polygon. It turns the way a
// stroke's quad does: the rasterizer adds up the areas of a path, so a
// circle turning the other way cut a hole where it overlapped its quad,
// and every joint of the recorded line showed a dark speck (#533).
func disc(r *vector.Rasterizer, x, y, radius float32) {
	const sides = 16
	for i := 0; i <= sides; i++ {
		angle := -2 * math.Pi * float64(i) / sides
		px, py := x+radius*float32(math.Cos(angle)), y+radius*float32(math.Sin(angle))
		if i == 0 {
			r.MoveTo(px, py)
		} else {
			r.LineTo(px, py)
		}
	}
	r.ClosePath()
}
