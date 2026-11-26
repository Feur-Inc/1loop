// SPDX-License-Identifier: MPL-2.0

package ui

// The tree of subchats in the sidebar (#398, #419), drawn as Claude Code
// draws nested agents: each subchat is plain text one step further in than
// its parent, a thin line runs down the left of a chat's subchats and bends
// into the last one, and the state's dot sits at the right of the row.

import (
	"image"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/image/vector"

	"1loop/internal/agent"
)

// Where a subchat guide draws, in its own coordinates. The guide starts
// where its parent chat's row does, whose text is 10 in.
const (
	treeStep   = 20 // each level's indent
	treeLine   = 14 // the line, from the guide's left edge
	treeReach  = 20 // where the line into the last subchat ends: the guide's right edge
	treeBend   = 3  // the bend's radius
	treeStroke = 1
)

// treeTrunk is what the line down a chat's subchats does beside a row.
type treeTrunk int

const (
	trunkNone    treeTrunk = iota // below the last subchat: the line has ended
	trunkThrough                  // a subchat with another after it, or a row under one: the line runs past
	trunkLast                     // the last subchat: the line bends into it
)

// subchatGuide is drawn left of a subchat's row: its indent, and beside it
// the line down the subchats of the chat the user started.
type subchatGuide struct {
	widget.BaseWidget
	depth int // the row's level: 1 under a chat the user started
	trunk treeTrunk
}

func newSubchatGuide(depth int, trunk treeTrunk) *subchatGuide {
	guide := &subchatGuide{depth: depth, trunk: trunk}
	guide.ExtendBaseWidget(guide)
	return guide
}

func (guide *subchatGuide) MinSize() fyne.Size {
	return fyne.NewSize(float32(guide.depth)*treeStep, 1)
}

func (guide *subchatGuide) CreateRenderer() fyne.WidgetRenderer {
	renderer := &subchatGuideRenderer{guide: guide}
	renderer.raster = canvas.NewRaster(renderer.draw)
	renderer.raster.ScaleMode = canvas.ImageScalePixels // see wholePixels
	renderer.Refresh()
	return renderer
}

// subchatGuideRenderer draws the line as one picture, at the screen's own
// resolution: its straight parts on whole pixels, crisp, and its bend a
// smooth quarter circle. Fyne's lines round both ends to the pixel, so the
// bend made of four short ones was lumpy (as the usage chart's lines, #445).
type subchatGuideRenderer struct {
	guide  *subchatGuide
	raster *canvas.Raster
}

// guideShape is what a guide draws, in its own coordinates.
type guideShape struct {
	down   bool    // the line down the left
	downTo float32 // where it ends: past the row, or where it bends
	bend   bool    // the quarter circle into the row, and the line across
	mid    float32 // the row's middle, where the line across runs
}

func (guide *subchatGuide) shape(size fyne.Size) guideShape {
	mid := float32(math.Round(float64(size.Height / 2)))
	switch guide.trunk {
	case trunkThrough:
		return guideShape{down: true, downTo: size.Height + 1, mid: mid} // over the gap to the next row
	case trunkLast:
		return guideShape{down: true, downTo: mid - treeBend, bend: true, mid: mid}
	}
	return guideShape{mid: mid}
}

func (renderer *subchatGuideRenderer) Layout(size fyne.Size) {
	renderer.raster.Move(fyne.Position{})
	// the line runs over the gap to the next row; whole pixels keep it crisp
	renderer.raster.Resize(wholePixels(size.AddWidthHeight(0, 1), objectScale(renderer.guide)))
}

func (renderer *subchatGuideRenderer) MinSize() fyne.Size { return renderer.guide.MinSize() }

func (renderer *subchatGuideRenderer) Refresh() {
	renderer.raster.Hidden = renderer.guide.trunk == trunkNone
	renderer.Layout(renderer.guide.Size())
	renderer.raster.Refresh()
}

// draw paints the guide at w × h pixels.
func (renderer *subchatGuideRenderer) draw(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	size := renderer.raster.Size()
	if w <= 0 || h <= 0 || size.Width <= 0 {
		return img
	}
	scale := float32(w) / size.Width
	shape := renderer.guide.shape(renderer.guide.Size())
	stroke := float32(math.Max(1, math.Round(float64(treeStroke*scale))))
	// on whole pixels: the line's centre half a stroke from a pixel's edge
	snap := func(v float32) float32 { return float32(math.Round(float64(v*scale-stroke/2))) + stroke/2 }
	x, y := snap(treeLine), snap(shape.mid)
	radius := float32(math.Round(float64(treeBend * scale)))
	var path vector.Rasterizer
	path.Reset(w, h)
	// Every shape turns the same way as the bend (anticlockwise on the
	// screen): the rasterizer adds up their areas, and one turning the
	// other way would cut a hole where they overlap.
	box := func(x0, y0, x1, y1 float32) {
		path.MoveTo(x0, y0)
		path.LineTo(x0, y1)
		path.LineTo(x1, y1)
		path.LineTo(x1, y0)
		path.ClosePath()
	}
	if shape.down {
		end := float32(h)
		if shape.bend {
			end = y - radius + 0.5 // into the bend: no seam
		}
		box(x-stroke/2, 0, x+stroke/2, end)
	}
	if shape.bend {
		// a quarter ring from the line down to the line across, centred
		// where they would meet a radius in
		cx, cy := x+radius, y-radius
		const sides = 12
		// As wide as the straight parts: its edges carry on theirs, with
		// no overhang where they meet.
		width := stroke
		outer, inner := radius+width/2, max(radius-width/2, 0)
		for i := 0; i <= sides; i++ {
			angle := math.Pi - float64(i)*math.Pi/2/sides
			px, py := cx+outer*float32(math.Cos(angle)), cy+outer*float32(math.Sin(angle))
			if i == 0 {
				path.MoveTo(px, py)
			} else {
				path.LineTo(px, py)
			}
		}
		for i := sides; i >= 0; i-- {
			angle := math.Pi - float64(i)*math.Pi/2/sides
			path.LineTo(cx+inner*float32(math.Cos(angle)), cy+inner*float32(math.Sin(angle)))
		}
		path.ClosePath()
		box(cx-0.5, y-stroke/2, float32(math.Round(float64(treeReach*scale))), y+stroke/2)
	}
	path.Draw(img, img.Bounds(), image.NewUniform(colMuted), image.Point{})
	return img
}

func (renderer *subchatGuideRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.raster}
}

func (renderer *subchatGuideRenderer) Destroy() {}

// subchatBadge is the dot at the right of a subchat's row for its state,
// nil while it is idle: amber while it works, the waiting mark while it
// waits on the user, green once done, red when it failed.
func subchatBadge(state string) fyne.Resource {
	switch state {
	case agent.SubchatWorking:
		return iconDot // tinted amber, as a chat at work
	case agent.SubchatWaiting:
		return iconWaiting
	case agent.SubchatQueued:
		return tinted(iconDot, colMuted)
	case agent.SubchatDone:
		return tinted(iconDot, colAdded)
	case agent.SubchatFailed:
		return tinted(iconDot, colDanger)
	}
	return nil
}
