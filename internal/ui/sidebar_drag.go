// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
)

// A project or chat row can be dragged among its siblings (#283): the row
// follows the pointer, a line shows where it would land, and letting go
// places it there, as its menu's Move up and Move down would. The pinned
// section's rows are dragged among the pinned ones. A chat row carried out
// of the sidebar goes back to its place, dimmed, and the panes show where
// it would open instead (split_drag.go); Escape cancels either drag.

// sideDrag is the row being dragged: its key, the keys it goes among in the
// sidebar's order, the row itself, where it was drawn and how far it has
// moved since. A chat row has its chat, which it can take to the panes.
type sideDrag struct {
	key       string
	siblings  []string
	object    fyne.CanvasObject
	home      fyne.Position
	height    float32
	dy        float32
	chat      *conversation // a chat that can have a pane of its own, else nil
	away      bool          // carried out of the sidebar, towards the panes
	cancelled bool          // Escape: the rest of the gesture does nothing
}

// dragRow makes a sidebar row draggable among siblings.
func (ui *desktopUI) dragRow(row *tapRow, key string, siblings []string) {
	row.onDrag = func(e *fyne.DragEvent) { ui.dragSide(key, siblings, e) }
	row.onDrop = ui.dropSide
}

// dragSide moves the dragged row with the pointer, and shows where it would
// land: among its siblings while the pointer is over the sidebar, in the
// panes once a chat's row is carried out of it. The row's first drag event
// starts the drag.
func (ui *desktopUI) dragSide(key string, siblings []string, e *fyne.DragEvent) {
	if ui.sideDrag == nil || ui.sideDrag.key != key {
		kept := ui.sideRows[key]
		if kept == nil {
			return
		}
		ui.sideDrag = &sideDrag{key: key, siblings: slices.Clone(siblings), object: kept.object,
			home: kept.object.Position(), height: kept.object.Size().Height, chat: ui.splittable(key)}
	}
	drag := ui.sideDrag
	if drag.cancelled {
		return
	}
	drag.dy += e.Dragged.DY
	if drag.chat != nil && !ui.overSidebar(e.AbsolutePosition) {
		if !drag.away {
			drag.away = true
			ui.sideMarker.Hide()
			relayout(ui.projectList) // the row back to its place
		}
		fadeRows(drag.object, carriedAlpha) // again after a rebuild of the sidebar
		ui.carryTo(drag.chat, nil, drag.chat.Title, e.AbsolutePosition)
		return
	}
	if drag.away {
		drag.away = false
		fadeRows(drag.object, 1)
		ui.endCarry()
	}
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
		ui.endCarry()
		return
	}
	ui.sideDrag = nil
	ui.sideMarker.Hide()
	if drag.cancelled {
		ui.endCarry()
		return
	}
	if drag.away { // let go over the panes, or off the window: no reordering
		fadeRows(drag.object, 1)
		ui.dropCarried()
		ui.renderChats()
		return
	}
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

// splittable is the chat a sidebar row of key shows, when it can have a
// pane or a window of its own (not a server's): its row can be carried to
// the panes.
func (ui *desktopUI) splittable(key string) *conversation {
	id, ok := strings.CutPrefix(key, chatKey(""))
	if !ok || ui.desktopApp == nil {
		return nil
	}
	if chat := ui.chatByID(id); chat != nil && ui.canOpenWindow(chat) {
		return chat
	}
	return nil
}

// overSidebar reports whether the point at (canvas coordinates) is over the
// window's sidebar.
func (ui *desktopUI) overSidebar(at fyne.Position) bool {
	column := ui.windowOf().sideColumn
	if column == nil || !column.Visible() {
		return true
	}
	origin, size := fyne.CurrentApp().Driver().AbsolutePositionForObject(column), column.Size()
	return at.X >= origin.X && at.X < origin.X+size.Width && at.Y >= origin.Y && at.Y < origin.Y+size.Height
}
