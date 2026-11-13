// SPDX-License-Identifier: MPL-2.0

package ui

// The tree of subchats in the sidebar (#398): each subchat sits one step
// further in than its parent, joined to it by a connector line, with a dot
// for its state, as Claude Code shows nested agents.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// Where a subchat guide draws, in its own coordinates.
const (
	treeStep   = 14  // from one level's line to the next
	treeFirst  = 9   // the first level's line, from the guide's left edge
	treeElbow  = 7   // the connector's run from its line toward the dot
	treeDot    = 3.5 // the status dot's radius
	treeStroke = 1
)

// subchatGuide is drawn left of a subchat's row: the lines of the levels
// above it that go on below, its connector and its status dot. levels has
// one entry per level down to the row's own: whether that level's line goes
// on below the row (an ancestor, or the row itself, has a sibling after it).
type subchatGuide struct {
	widget.BaseWidget
	levels []bool
	state  string // agent.SubchatWorking, SubchatWaiting, SubchatDone, SubchatFailed, or "" when idle
}

func newSubchatGuide(levels []bool, state string) *subchatGuide {
	guide := &subchatGuide{levels: levels, state: state}
	guide.ExtendBaseWidget(guide)
	return guide
}

// depth is the row's level: 1 under a chat the user started.
func (guide *subchatGuide) depth() int { return len(guide.levels) }

// lineX is where level's line runs, level counted from 1.
func lineX(level int) float32 { return treeFirst + float32(level-1)*treeStep }

// dotX is the centre of the status dot of a row at depth.
func (guide *subchatGuide) dotX() float32 { return lineX(guide.depth()) + treeElbow + treeDot + 1 }

// dotColor is the status dot's colour: amber while it works, green done,
// red failed, grey idle.
func (guide *subchatGuide) dotColor() color.NRGBA {
	switch guide.state {
	case agent.SubchatWorking, agent.SubchatWaiting:
		return colAmber
	case agent.SubchatDone:
		return colAdded
	case agent.SubchatFailed:
		return colDanger
	}
	return colFaint
}

func (guide *subchatGuide) MinSize() fyne.Size {
	return fyne.NewSize(guide.dotX()+treeDot+5, 1)
}

func (guide *subchatGuide) CreateRenderer() fyne.WidgetRenderer {
	renderer := &subchatGuideRenderer{guide: guide}
	for range guide.levels {
		renderer.lines = append(renderer.lines, canvas.NewLine(colBorder))
	}
	renderer.elbow = canvas.NewLine(colBorder)
	renderer.below = canvas.NewLine(colBorder)
	renderer.dot = canvas.NewCircle(colFaint)
	renderer.Refresh()
	return renderer
}

type subchatGuideRenderer struct {
	guide *subchatGuide
	lines []*canvas.Line // one per level: the ones above the row, then its own down to the connector
	elbow *canvas.Line   // from its line to the dot
	below *canvas.Line   // its own line under the connector, toward the next sibling
	dot   *canvas.Circle
}

func (renderer *subchatGuideRenderer) Layout(size fyne.Size) {
	guide := renderer.guide
	mid := size.Height / 2
	depth := guide.depth()
	for i, line := range renderer.lines {
		x := lineX(i + 1)
		line.Position1 = fyne.NewPos(x, 0)
		line.Position2 = fyne.NewPos(x, size.Height)
		if i == depth-1 { // its own: down to the connector
			line.Position2 = fyne.NewPos(x, mid)
		}
	}
	x := lineX(depth)
	renderer.below.Position1, renderer.below.Position2 = fyne.NewPos(x, mid), fyne.NewPos(x, size.Height)
	renderer.elbow.Position1, renderer.elbow.Position2 = fyne.NewPos(x, mid), fyne.NewPos(x+treeElbow, mid)
	center := guide.dotX()
	renderer.dot.Position1 = fyne.NewPos(center-treeDot, mid-treeDot)
	renderer.dot.Position2 = fyne.NewPos(center+treeDot, mid+treeDot)
}

func (renderer *subchatGuideRenderer) MinSize() fyne.Size { return renderer.guide.MinSize() }

func (renderer *subchatGuideRenderer) Refresh() {
	guide := renderer.guide
	for i, line := range renderer.lines {
		line.StrokeColor, line.StrokeWidth = colBorder, treeStroke
		// a level above the row shows only while its line goes on below;
		// the row's own always runs down to its connector
		if i == guide.depth()-1 || guide.levels[i] {
			line.Show()
		} else {
			line.Hide()
		}
	}
	renderer.elbow.StrokeColor, renderer.elbow.StrokeWidth = colBorder, treeStroke
	renderer.below.StrokeColor, renderer.below.StrokeWidth = colBorder, treeStroke
	if guide.depth() > 0 && guide.levels[guide.depth()-1] {
		renderer.below.Show()
	} else {
		renderer.below.Hide()
	}
	ink := guide.dotColor()
	renderer.dot.FillColor, renderer.dot.StrokeColor, renderer.dot.StrokeWidth = ink, ink, 0
	if guide.state == agent.SubchatWaiting { // waiting on the user: a ring
		renderer.dot.FillColor, renderer.dot.StrokeWidth = color.NRGBA{}, 1.5
	}
	renderer.Layout(guide.Size())
	for _, object := range renderer.Objects() {
		object.Refresh()
	}
}

func (renderer *subchatGuideRenderer) Objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, 0, len(renderer.lines)+3)
	for _, line := range renderer.lines {
		objects = append(objects, line)
	}
	return append(objects, renderer.below, renderer.elbow, renderer.dot)
}

func (renderer *subchatGuideRenderer) Destroy() {}
