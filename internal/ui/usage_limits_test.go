// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
)

// A dashed line keeps dashes of one length and gap, whatever its length:
// a short forecast was 48 dashes squeezed into dots, a long line had long
// ones (#445).
func TestDashSpans(t *testing.T) {
	for _, tc := range []struct {
		length float32
		want   int
	}{{100, 10}, {50, 5}, {500, 50}, {18, 2}, {0.5, 0}, {10.5, 1}} {
		spans := dashSpans(tc.length)
		if len(spans) != tc.want {
			t.Errorf("%v px: %d dashes, want %d", tc.length, len(spans), tc.want)
		}
		for i, span := range spans {
			if span[1]-span[0] > dashLength || span[1]-span[0] < 1 || span[1] > tc.length {
				t.Errorf("%v px: dash %d is %v", tc.length, i, span)
			}
			if i > 0 && span[0]-spans[i-1][0] != dashLength+dashGap {
				t.Errorf("%v px: dash %d starts %v after the one before", tc.length, i, span[0]-spans[i-1][0])
			}
		}
	}
}

// A steep dashed line is drawn straight: every pixel it paints lies on the
// line, within its width (Fyne's lines, rounded to the pixel at both ends,
// made its short dashes zigzag), and its gaps are left empty.
func TestSteepDashedStrokeIsStraight(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		img := image.NewNRGBA(image.Rect(0, 0, int(60*scale), int(240*scale)))
		from, to := fyne.NewPos(8, 6), fyne.NewPos(41, 230) // as steep as "To last" before the reset
		paintStrokes(img, []stroke{{from: from, to: to, ink: color.White, width: 2, dashed: true}}, scale)
		dx, dy := float64(to.X-from.X), float64(to.Y-from.Y)
		length := math.Hypot(dx, dy)
		painted, empty := 0, 0
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				px, py := (float64(x)+0.5)/float64(scale)-float64(from.X), (float64(y)+0.5)/float64(scale)-float64(from.Y)
				distance := math.Abs(px*dy-py*dx) / length
				along := (px*dx + py*dy) / length
				if img.NRGBAAt(x, y).A > 128 {
					painted++
					if distance > 1+0.75/float64(scale) {
						t.Fatalf("scale %v: pixel %d,%d is %.2f px off the line", scale, x, y, distance)
					}
				} else if distance < 0.4 && along > 1 && along < length-1 && math.Mod(along, dashLength+dashGap) < dashLength-1 {
					empty++
				}
			}
		}
		if painted == 0 || empty > 0 {
			t.Fatalf("scale %v: %d pixels painted, %d holes inside dashes", scale, painted, empty)
		}
	}
}

// A line of several segments with round ends is solid where they meet:
// no darker speck at a joint nor at its ends.
func TestStrokeJointsAreSolid(t *testing.T) {
	ink := color.NRGBA{R: 0xe0, G: 0x5a, B: 0x4a, A: 0xff}
	points := []fyne.Position{{X: 5, Y: 5}, {X: 8, Y: 20}, {X: 30, Y: 20}, {X: 34, Y: 28}, {X: 60, Y: 40}}
	var strokes []stroke
	for i := 0; i+1 < len(points); i++ {
		strokes = append(strokes, stroke{from: points[i], to: points[i+1], ink: ink, width: 2, round: true})
	}
	for _, scale := range []float32{1, 1.4, 2} {
		img := image.NewNRGBA(image.Rect(0, 0, int(70*scale), int(50*scale)))
		paintStrokes(img, strokes, scale)
		for _, p := range points {
			// the pixel the joint lies in
			x, y := int(p.X*scale), int(p.Y*scale)
			if a := img.NRGBAAt(x, y).A; a < 0xc0 {
				t.Errorf("scale %v: the line has a hole at %v (alpha %d)", scale, p, a)
			}
		}
	}
}
