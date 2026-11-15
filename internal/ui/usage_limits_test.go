// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// A dashed line keeps dashes of one length and gap, whatever its length:
// a short forecast was 48 dashes squeezed into dots, a long line had long
// ones (#445). Its count follows its length, and resizing changes it.
func TestDashedLineFixedPattern(t *testing.T) {
	visible := func(d *dashedLine) []*canvas.Line {
		var out []*canvas.Line
		for _, line := range d.lines {
			if line.Visible() {
				out = append(out, line)
			}
		}
		return out
	}
	length := func(line *canvas.Line) float64 {
		return math.Hypot(float64(line.Position2.X-line.Position1.X), float64(line.Position2.Y-line.Position1.Y))
	}
	d := newDashedLine(colText, 2)
	for _, tc := range []struct {
		to   fyne.Position
		want int
	}{
		{fyne.NewPos(90, 0), 10},  // 90 px: 10 dashes of 5, 4 apart
		{fyne.NewPos(30, 40), 6},  // 50 px on a slope: 6 (the last one cut at the end)
		{fyne.NewPos(450, 0), 50}, // longer: more dashes, the same size
		{fyne.NewPos(18, 0), 2},   // the legend key
		{fyne.NewPos(0.5, 0), 0},
	} {
		d.layout(fyne.NewPos(0, 0), tc.to)
		lines := visible(d)
		if len(lines) != tc.want {
			t.Errorf("to %v: %d dashes, want %d", tc.to, len(lines), tc.want)
		}
		for i, line := range lines {
			if got := length(line); got > dashLength+0.01 || got < 1 {
				t.Errorf("to %v: dash %d is %.2f px", tc.to, i, got)
			}
			if i > 0 {
				step := math.Hypot(float64(line.Position1.X-lines[i-1].Position1.X), float64(line.Position1.Y-lines[i-1].Position1.Y))
				if math.Abs(step-(dashLength+dashGap)) > 0.01 {
					t.Errorf("to %v: dash %d starts %.2f px after the one before", tc.to, i, step)
				}
			}
		}
	}
}
