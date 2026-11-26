// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #474): frames that paint only what changed.

package glfw

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal/driver/common"
)

// damageMargin is painted around what changed: antialiased edges and
// strokes reach a little past an object's bounds.
const damageMargin = 2

// retainGap is how far apart frames must come for the next one to be
// painted into the retained frame: copying it into the window costs a
// pass over the whole window (2 ms a frame on llvmpipe), which frames
// drawn back to back (a shader, a scroll, an animation) are painted
// whole without, straight into the window.
const retainGap = 50 * time.Millisecond

// retention is what a window remembers of its last frame, to choose how
// the next one is painted.
type retention struct {
	last    time.Time // when it was painted
	partial bool      // it was painted in part
	held    bool      // the retained framebuffer holds the last frame
}

// retains says whether the frame painted at now goes into the retained
// frame: when frames come far apart (an idle window, a caret's blink,
// typing, streaming), the last was painted in part, or the next one can
// be painted in part over the frame already held.
func (r *retention) retains(now time.Time, canPartial bool) bool {
	return r.partial || (r.held && canPartial) || now.Sub(r.last) >= retainGap
}

// damageArea is the part of a window a frame paints, in canvas units.
type damageArea struct {
	pos  fyne.Position
	size fyne.Size
}

// damageOf is the area that the objects refreshed since the last frame
// changed, when a frame may paint that alone: each is a shape that draws
// within its bounds (the caret, a dot, a ring), found in the window, none
// behind a blur (which reads what is under it), and together they cover
// a small part of the window. ok is false otherwise: the frame is painted
// whole.
func (c *glCanvas) damageOf(objects []fyne.CanvasObject) (area damageArea, ok bool) {
	var stroke float32
	for _, obj := range objects {
		switch o := obj.(type) {
		case *canvas.Rectangle:
			stroke = fyne.Max(stroke, o.StrokeWidth)
		case *canvas.Circle:
			stroke = fyne.Max(stroke, o.StrokeWidth)
		case *canvas.Line:
			stroke = fyne.Max(stroke, o.StrokeWidth)
		case *canvas.Image:
			// An image draws strictly within its bounds and triggers no layout.
		default:
			return area, false // text, widgets: they may lay out, or draw past their bounds
		}
	}
	var found [common.MaxDamaged]bool
	if len(objects) > len(found) {
		return area, false
	}
	var blurs []damageArea
	left := len(objects)
	var x1, y1, x2, y2 float32
	c.WalkTrees(func(node *common.RenderCacheNode, pos fyne.Position) {
		obj := node.Obj()
		if _, blur := obj.(*canvas.Blur); blur {
			blurs = append(blurs, damageArea{pos, obj.Size()})
			return
		}
		if left == 0 {
			return
		}
		for i, o := range objects {
			if o != obj || found[i] {
				continue
			}
			found[i] = true
			size := obj.Size()
			if left == len(objects) {
				x1, y1, x2, y2 = pos.X, pos.Y, pos.X+size.Width, pos.Y+size.Height
			} else {
				x1, y1 = fyne.Min(x1, pos.X), fyne.Min(y1, pos.Y)
				x2, y2 = fyne.Max(x2, pos.X+size.Width), fyne.Max(y2, pos.Y+size.Height)
			}
			left--
		}
	}, nil)
	if left > 0 {
		return area, false // hidden, or not in this window
	}
	pad := damageMargin + stroke
	area = damageArea{fyne.NewPos(x1-pad, y1-pad), fyne.NewSize(x2-x1+2*pad, y2-y1+2*pad)}
	whole := c.Size()
	if area.size.Width*area.size.Height > whole.Width*whole.Height/16 {
		return area, false // as well painted whole
	}
	for _, blur := range blurs {
		if area.overlaps(blur.pos, blur.size) {
			return area, false
		}
	}
	return area, true
}

func (a *damageArea) overlaps(pos fyne.Position, size fyne.Size) bool {
	return pos.X < a.pos.X+a.size.Width && pos.X+size.Width > a.pos.X &&
		pos.Y < a.pos.Y+a.size.Height && pos.Y+size.Height > a.pos.Y
}

// reaches says whether painting obj at pos may change a pixel of the area.
// Shapes and images draw within their bounds (plus strokes); text is culled
// by its vertical band so off-band lines skip Painter.Paint.
func (a *damageArea) reaches(obj fyne.CanvasObject, pos fyne.Position) bool {
	var stroke float32
	switch o := obj.(type) {
	case *canvas.Rectangle:
		stroke = o.StrokeWidth
	case *canvas.Circle:
		stroke = o.StrokeWidth
	case *canvas.Line:
		stroke = o.StrokeWidth
	case *canvas.Image:
	case *canvas.Text:
		pad := float32(damageMargin)
		h := fyne.Max(o.Size().Height, o.TextSize*2)
		return pos.Y-pad < a.pos.Y+a.size.Height && pos.Y+h+pad > a.pos.Y
	default:
		return true
	}
	pad := damageMargin + stroke
	size := obj.Size()
	return a.overlaps(fyne.NewPos(pos.X-pad, pos.Y-pad), fyne.NewSize(size.Width+2*pad, size.Height+2*pad))
}
