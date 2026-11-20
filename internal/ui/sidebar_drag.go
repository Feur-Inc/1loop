// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"time"

	"fyne.io/fyne/v2"
)

// A project or chat row can be dragged among its siblings (#283): the row
// follows the pointer, a line shows where it would land, and letting go
// places it there, as its menu's Move up and Move down would. The pinned
// section's rows are dragged among the pinned ones.

// sideDrag is the row being dragged: its key, the keys it goes among in the
// sidebar's order, the row itself, where it was drawn and how far it has
// moved since.
type sideDrag struct {
	key      string
	siblings []string
	object   fyne.CanvasObject
	home     fyne.Position
	height   float32
	dy       float32
}

// dragRow makes a sidebar row draggable among siblings.
func (ui *desktopUI) dragRow(row *tapRow, key string, siblings []string) {
	row.onDrag = func(dy float32) { ui.dragSide(key, siblings, dy) }
	row.onDrop = ui.dropSide
}

// dragSide moves the dragged row with the pointer by dy, and shows where it
// would land. The row's first drag event starts the drag.
func (ui *desktopUI) dragSide(key string, siblings []string, dy float32) {
	if ui.sideDrag == nil || ui.sideDrag.key != key {
		kept := ui.sideRows[key]
		if kept == nil {
			return
		}
		ui.sideDrag = &sideDrag{key: key, siblings: slices.Clone(siblings), object: kept.object,
			home: kept.object.Position(), height: kept.object.Size().Height}
	}
	drag := ui.sideDrag
	drag.dy += dy
	drag.object.Move(drag.home.AddXY(0, drag.dy))
	to := ui.dropIndex(drag)
	ui.showDropLine(drag, to)
}

// dropIndex is where the dragged row lands among its siblings: the count of
// the other siblings whose middle is above the row's middle now.
func (ui *desktopUI) dropIndex(drag *sideDrag) int {
	centre := drag.home.Y + drag.dy + drag.height/2
	to := 0
	for _, key := range drag.siblings {
		if kept := ui.sideRows[key]; key != drag.key && kept != nil && kept.object.Position().Y+kept.object.Size().Height/2 < centre {
			to++
		}
	}
	return to
}

// showDropLine draws the line at the top of the sibling that comes at index
// to among the others, or under the last one.
func (ui *desktopUI) showDropLine(drag *sideDrag, to int) {
	var others []fyne.CanvasObject
	for _, key := range drag.siblings {
		if kept := ui.sideRows[key]; key != drag.key && kept != nil {
			others = append(others, kept.object)
		}
	}
	if len(others) == 0 {
		ui.sideMarker.Hide()
		return
	}
	y := others[len(others)-1].Position().Y + others[len(others)-1].Size().Height
	if to < len(others) {
		y = others[to].Position().Y
	}
	ui.sideMarker.Move(fyne.NewPos(0, y-1))
	ui.sideMarker.Resize(fyne.NewSize(ui.projectList.Size().Width, 2))
	ui.sideMarker.Show()
	ui.sideMarker.Refresh()
}

// dropSide places the dragged row where the line was, and draws the sidebar
// again. A row let go where it was keeps its place.
func (ui *desktopUI) dropSide() {
	drag := ui.sideDrag
	if drag == nil {
		return
	}
	ui.sideDrag = nil
	ui.sideMarker.Hide()
	to := ui.dropIndex(drag)
	if ui.isPinned(drag.key) {
		ui.pinned = reorderKeys(drag.siblings, drag.key, to)
	} else {
		if ui.sideRank == nil {
			ui.sideRank = map[string]int64{}
		}
		placeRank(ui.sideRank, drag.siblings, drag.key, to, time.Now().UnixNano())
	}
	ui.save()
	ui.renderChats()
}
