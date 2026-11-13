// SPDX-License-Identifier: MPL-2.0

package ui

// The tree of subchats in the sidebar (#398, #419), drawn as Claude Code
// draws nested agents: each subchat is plain text one step further in than
// its parent, a thin line runs down the left of a chat's subchats and bends
// into the last one, and the state's dot sits at the right of the row.

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// Where a subchat guide draws, in its own coordinates. The guide starts
// where its parent chat's row does, whose text is 10 in.
const (
	treeStep   = 20 // each level's indent
	treeLine   = 14 // the line, from the guide's left edge
	treeReach  = 19 // where the bend into the last subchat ends
	treeBend   = 4  // the bend's radius
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
	renderer.down = canvas.NewLine(colMuted)
	renderer.across = canvas.NewLine(colMuted)
	for i := 0; i < treeBendSegments; i++ {
		renderer.bend = append(renderer.bend, canvas.NewLine(colMuted))
	}
	renderer.Refresh()
	return renderer
}

// treeBendSegments are the short lines the bend is made of.
const treeBendSegments = 4

type subchatGuideRenderer struct {
	guide  *subchatGuide
	down   *canvas.Line   // the line down the left
	bend   []*canvas.Line // its rounded corner into the last subchat
	across *canvas.Line   // from the corner toward the row's text
}

func (renderer *subchatGuideRenderer) Layout(size fyne.Size) {
	mid := float32(math.Round(float64(size.Height / 2)))
	renderer.down.Position1 = fyne.NewPos(treeLine, 0)
	renderer.down.Position2 = fyne.NewPos(treeLine, size.Height+1) // over the gap to the next row
	if renderer.guide.trunk == trunkLast {
		renderer.down.Position2 = fyne.NewPos(treeLine, mid-treeBend)
	}
	// a quarter circle from the line, going down, to the row, going right
	centre := fyne.NewPos(treeLine+treeBend, mid-treeBend)
	at := func(i int) fyne.Position {
		angle := math.Pi - float64(i)*math.Pi/2/treeBendSegments
		return fyne.NewPos(centre.X+treeBend*float32(math.Cos(angle)), centre.Y+treeBend*float32(math.Sin(angle)))
	}
	for i, line := range renderer.bend {
		line.Position1, line.Position2 = at(i), at(i+1)
	}
	renderer.across.Position1 = fyne.NewPos(treeLine+treeBend, mid)
	renderer.across.Position2 = fyne.NewPos(treeReach, mid)
}

func (renderer *subchatGuideRenderer) MinSize() fyne.Size { return renderer.guide.MinSize() }

func (renderer *subchatGuideRenderer) Refresh() {
	trunk := renderer.guide.trunk
	for _, line := range renderer.lines() {
		line.StrokeColor, line.StrokeWidth = colMuted, treeStroke
		line.Hidden = trunk == trunkNone
	}
	for _, line := range append(renderer.bend, renderer.across) {
		line.Hidden = trunk != trunkLast
	}
	renderer.Layout(renderer.guide.Size())
	for _, line := range renderer.lines() {
		line.Refresh()
	}
}

func (renderer *subchatGuideRenderer) lines() []*canvas.Line {
	return append([]*canvas.Line{renderer.down, renderer.across}, renderer.bend...)
}

func (renderer *subchatGuideRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{}
	for _, line := range renderer.lines() {
		objects = append(objects, line)
	}
	return objects
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
	case agent.SubchatDone:
		return tinted(iconDot, colAdded)
	case agent.SubchatFailed:
		return tinted(iconDot, colDanger)
	}
	return nil
}
