// SPDX-License-Identifier: MPL-2.0

package ui

// Where a chat or a pane dragged over a pane lands (#577): the arithmetic,
// apart from the widgets (split_drag.go). A pane has five drop zones: its
// left, right, top and bottom parts split it on that side, the dragged chat
// taking that half; its middle opens the chat in its place.

import "fyne.io/fyne/v2"

// dropZone is the part of a pane the pointer is over.
type dropZone int

const (
	dropNone dropZone = iota
	dropHere          // the middle: in the pane, in place of what it shows
	dropLeft
	dropRight
	dropTop
	dropBottom
)

// dropEdge is how far into a pane, as a share of its width or height, its
// sides reach: a drop there splits it. The middle 40 % opens the chat in it.
const dropEdge = 0.3

// dropInset is the margin between a pane's edge and its drop preview.
const dropInset = 6

// dropZoneAt is the zone of a pane of size under the point at, in the
// pane's coordinates: the side the point is nearest to, measured as a share
// of the pane so that a corner goes to the side it is deeper in, unless it
// is further than dropEdge from every side.
func dropZoneAt(size fyne.Size, at fyne.Position) dropZone {
	if size.Width <= 0 || size.Height <= 0 || at.X < 0 || at.Y < 0 || at.X >= size.Width || at.Y >= size.Height {
		return dropNone
	}
	x, y := at.X/size.Width, at.Y/size.Height
	zone, nearest := dropLeft, x
	for _, side := range []struct {
		zone     dropZone
		distance float32
	}{{dropRight, 1 - x}, {dropTop, y}, {dropBottom, 1 - y}} {
		if side.distance < nearest {
			zone, nearest = side.zone, side.distance
		}
	}
	if nearest >= dropEdge {
		return dropHere
	}
	return zone
}

// splitSide is how a side zone splits a pane: top and bottom, or side by
// side, and whether the new half comes first (left or top).
func splitSide(zone dropZone) (vertical, before bool) {
	return zone == dropTop || zone == dropBottom, zone == dropLeft || zone == dropTop
}

// dropFits reports whether a pane of size has room for zone: the middle
// always, a side when both halves keep minPaneSize (splitFits).
func dropFits(size fyne.Size, zone dropZone) bool {
	switch zone {
	case dropNone:
		return false
	case dropHere:
		return true
	}
	vertical, _ := splitSide(zone)
	return splitRoom(size, vertical)
}

// dropTarget is the zone a drop at the point at, in a pane of size, uses:
// the zone under it, or the middle when that side has no room for a split.
// room is the size the pane will have once the drop is done (larger when
// the pane dragged leaves its side), the size itself otherwise.
func dropTarget(size, room fyne.Size, at fyne.Position) dropZone {
	zone := dropZoneAt(size, at)
	if zone != dropNone && !dropFits(room, zone) {
		return dropHere
	}
	return zone
}

// dropRect is the part of a pane of size a zone's drop gives the chat: the
// half on that side as the split will cut it (splitCut), or the whole pane
// for the middle; inset by dropInset, for the preview.
func dropRect(size fyne.Size, zone dropZone) rect {
	at, area := fyne.NewPos(0, 0), size
	switch zone {
	case dropNone:
		return rect{}
	case dropLeft:
		area.Width = splitCut(0.5, size.Width)
	case dropRight:
		cut := splitCut(0.5, size.Width) + dividerSize
		at.X, area.Width = cut, size.Width-cut
	case dropTop:
		area.Height = splitCut(0.5, size.Height)
	case dropBottom:
		cut := splitCut(0.5, size.Height) + dividerSize
		at.Y, area.Height = cut, size.Height-cut
	}
	return insetRect(rect{at, fyne.NewPos(area.Width, area.Height)}, dropInset)
}

// insetRect shrinks r by margin on every side, never below nothing.
func insetRect(r rect, margin float32) rect {
	width, height := max(0, r.size.X-2*margin), max(0, r.size.Y-2*margin)
	return rect{fyne.NewPos(r.pos.X+(r.size.X-width)/2, r.pos.Y+(r.size.Y-height)/2), fyne.NewPos(width, height)}
}

// scaleRect is r grown or shrunk by factor around its middle.
func scaleRect(r rect, factor float32) rect {
	width, height := r.size.X*factor, r.size.Y*factor
	return rect{fyne.NewPos(r.pos.X+(r.size.X-width)/2, r.pos.Y+(r.size.Y-height)/2), fyne.NewPos(width, height)}
}

// lerpRect is the rectangle part p of the way from a to b.
func lerpRect(a, b rect, p float32) rect {
	lerp := func(x, y float32) float32 { return x + (y-x)*p }
	return rect{fyne.NewPos(lerp(a.pos.X, b.pos.X), lerp(a.pos.Y, b.pos.Y)), fyne.NewPos(lerp(a.size.X, b.size.X), lerp(a.size.Y, b.size.Y))}
}

// unionRect is the smallest rectangle around a and b.
func unionRect(a, b rect) rect {
	left, top := min(a.pos.X, b.pos.X), min(a.pos.Y, b.pos.Y)
	right, bottom := max(a.pos.X+a.size.X, b.pos.X+b.size.X), max(a.pos.Y+a.size.Y, b.pos.Y+b.size.Y)
	return rect{fyne.NewPos(left, top), fyne.NewPos(right-left, bottom-top)}
}

// outsideCanvas reports whether the point at is off a canvas of size: the
// pointer left the window during a drag (X11 and Wayland report it while a
// button is held).
func outsideCanvas(size fyne.Size, at fyne.Position) bool {
	return at.X < 0 || at.Y < 0 || at.X >= size.Width || at.Y >= size.Height
}

// dropLabel says what a drop in zone does: for a chat, or for a pane moved
// (move), whose middle swaps the two panes.
func dropLabel(zone dropZone, move bool) string {
	verb := "Split "
	if move {
		verb = "Move "
	}
	switch zone {
	case dropHere:
		if move {
			return "Swap panes"
		}
		return "Open here"
	case dropLeft:
		return verb + "left"
	case dropRight:
		return verb + "right"
	case dropTop:
		return verb + "up"
	case dropBottom:
		return verb + "down"
	}
	return ""
}
