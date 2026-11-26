// SPDX-License-Identifier: MPL-2.0

package ui

// Selection across the conversation. Each message has its own selectLayer;
// chatSelection joins them: a drag that starts in one message and moves
// into others selects the rest of the first, every message in between and
// the start of the last, and Ctrl+C copies them in reading order. A press
// in the conversation's margins, or between two messages, starts at the
// nearest message's text (selectCatcher), as in a text editor.
//
// It also holds what the gestures share between messages: the press the
// selection grows from (its anchor), the unit it grows by, and the count of
// the presses of a double or triple click.

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

type chatSelection struct {
	ui     *desktopUI
	anchor *selectLayer   // where the press was
	lo, hi int            // what the press selected in it: a caret, a word, a paragraph
	unit   int            // what the selection grows by (selection_units.go)
	marked []*selectLayer // the layers holding part of the selection, in reading order
	// pressed is the layer the button went down on: its drags extend.
	pressed *selectLayer
	clicks  clickCounter
}

// start makes a press in layer the beginning of a new selection.
func (chatSel *chatSelection) start(layer *selectLayer) {
	chatSel.ui.selectIn(chatSel)
	for _, other := range chatSel.marked {
		if other != layer {
			other.clearSelection()
		}
	}
	chatSel.anchor, chatSel.pressed, chatSel.marked = layer, layer, []*selectLayer{layer}
}

// press starts a selection at a point of layer: the caret there, or the
// word or the paragraph under it.
func (chatSel *chatSelection) press(layer *selectLayer, at fyne.Position, unit int) {
	chatSel.start(layer)
	chatSel.unit = unit
	if len(layer.pieces) == 0 {
		chatSel.lo, chatSel.hi = 0, 0
		layer.clearSelection()
		return
	}
	lo := layer.caretAt(at)
	hi := lo
	if unit != byChar {
		lo, hi = layer.unitAt(unit, layer.hitAt(at))
	}
	chatSel.lo, chatSel.hi = lo, hi
	layer.mark(lo, hi)
}

// placed is a layer of the conversation with its place on the screen.
type placed struct {
	layer *selectLayer
	at    fyne.Position
	shown bool // on screen: out of sight, its place is not known
}

// layers are the conversation's selection layers, in reading order, those
// out of sight too.
func (chatSel *chatSelection) layers() []placed {
	var found []placed
	var walk func(fyne.CanvasObject, bool)
	walk = func(object fyne.CanvasObject, shown bool) {
		if object == nil {
			return
		}
		shown = shown && object.Visible()
		switch o := object.(type) {
		case *selectLayer:
			found = append(found, placed{layer: o, shown: shown})
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child, shown)
			}
		}
	}
	if chatSel.ui.transcript != nil {
		walk(chatSel.ui.transcript, true)
	}
	if app := fyne.CurrentApp(); app != nil {
		driver := app.Driver()
		for i := range found {
			if found[i].shown {
				found[i].at = driver.AbsolutePositionForObject(found[i].layer)
			}
		}
	}
	return found
}

// nearest is the layer on screen at the height of a point, else the
// closest one above or below it. -1 when there is none.
func nearest(layers []placed, at fyne.Position) int {
	best, bestDistance := -1, float32(0)
	for i, candidate := range layers {
		if !candidate.shown {
			continue
		}
		top, bottom := candidate.at.Y, candidate.at.Y+candidate.layer.Size().Height
		distance := float32(0)
		if at.Y < top {
			distance = top - at.Y
		} else if at.Y > bottom {
			distance = at.Y - bottom
		}
		if best < 0 || distance < bestDistance {
			best, bestDistance = i, distance
		}
	}
	return best
}

// extend moves the selection's moving end to a point on the screen,
// wherever it is in the conversation (a drag, a Shift+click): from the
// anchor's unit to the unit under the pointer. False when the anchor is no
// longer in the conversation.
func (chatSel *chatSelection) extend(at fyne.Position) bool {
	anchor := chatSel.anchor
	if anchor == nil {
		return false
	}
	chatSel.ui.selectIn(chatSel)
	layers := chatSel.layers()
	from, to := -1, nearest(layers, at)
	for i, candidate := range layers {
		if candidate.layer == anchor {
			from = i
		}
	}
	if from < 0 || to < 0 {
		return false
	}
	if anchor.stale || len(anchor.pieces) == 0 {
		anchor.known()
	}
	// Level with the anchor's message: it alone.
	if shown := layers[from]; shown.shown && at.Y >= shown.at.Y && at.Y <= shown.at.Y+anchor.Size().Height {
		to = from
	}
	var marked []*selectLayer
	if to == from {
		local := at.Subtract(layers[from].at)
		lo, hi := extendSpan(anchor.text, chatSel.unit, chatSel.lo, chatSel.hi, anchor.hitAt(local), anchor.caretAt(local))
		anchor.mark(lo, hi)
		marked = []*selectLayer{anchor}
	} else {
		forward := to > from
		first, last := from, to
		if !forward {
			first, last = to, from
		}
		for i := first; i <= last; i++ {
			current := layers[i].layer
			if layers[i].shown && current.stale {
				current.gather()
			}
			marked = append(marked, current)
			switch {
			case i == from && forward: // the press's message: from the press to its end
				current.mark(chatSel.lo, -1)
			case i == from:
				current.mark(0, chatSel.hi)
			case i == to: // the pointer's message: from its start or end to the pointer
				reach := current.reach(at.Subtract(layers[i].at), chatSel.unit, forward)
				if forward {
					current.mark(0, reach)
				} else {
					current.mark(reach, -1)
				}
			default:
				current.mark(0, -1)
			}
		}
	}
	chatSel.keep(marked)
	return true
}

// keep makes marked the layers holding the selection: the others let go.
func (chatSel *chatSelection) keep(marked []*selectLayer) {
	keep := map[*selectLayer]bool{}
	for _, layer := range marked {
		keep[layer] = true
	}
	for _, other := range chatSel.marked {
		if !keep[other] {
			other.clearSelection()
		}
	}
	chatSel.marked = marked
}

// selectAll selects the whole conversation (Ctrl+A).
func (chatSel *chatSelection) selectAll() {
	layers := chatSel.layers()
	if len(layers) == 0 {
		return
	}
	chatSel.ui.selectIn(chatSel)
	var marked []*selectLayer
	for _, candidate := range layers {
		if candidate.shown {
			candidate.layer.gather()
		}
		candidate.layer.mark(0, -1)
		marked = append(marked, candidate.layer)
	}
	chatSel.keep(marked)
	chatSel.anchor, chatSel.pressed = marked[0], nil
	chatSel.lo, chatSel.hi, chatSel.unit = 0, 0, byChar
	chatSel.offer()
}

// follow scrolls the conversation while the pointer is past its top or the
// composer floating over its end, a little more the further it is.
func (chatSel *chatSelection) follow(at fyne.Position) {
	scroll := chatSel.ui.scroll
	app := fyne.CurrentApp()
	if scroll == nil || app == nil {
		return
	}
	origin := app.Driver().AbsolutePositionForObject(scroll)
	top, bottom := origin.Y, origin.Y+scroll.Size().Height
	if space := chatSel.ui.footerSpace; space != nil {
		bottom -= space.MinSize().Height
	}
	step := float32(0)
	switch {
	case at.Y < top:
		step = -fyne.Min(40, top-at.Y)
	case at.Y > bottom:
		step = fyne.Min(40, at.Y-bottom)
	}
	if step == 0 {
		return
	}
	end := fyne.Max(0, scroll.Content.Size().Height-scroll.Size().Height)
	offset := fyne.Min(end, fyne.Max(0, scroll.Offset.Y+step))
	if offset == scroll.Offset.Y {
		return
	}
	scroll.ScrollToOffset(fyne.NewPos(scroll.Offset.X, offset))
	if scroll.OnScrolled != nil {
		scroll.OnScrolled(scroll.Offset)
	}
}

func (chatSel *chatSelection) selectedText() string {
	var parts []string
	for _, layer := range chatSel.marked {
		if text := layer.selectedText(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (chatSel *chatSelection) clearSelection() {
	for _, layer := range chatSel.marked {
		layer.clearSelection()
	}
	chatSel.anchor, chatSel.pressed, chatSel.marked = nil, nil, nil
}

// offer makes what is selected the primary selection, which a middle click
// pastes in other programs (Linux: selection_primary_*.go).
func (chatSel *chatSelection) offer() {
	if text := chatSel.selectedText(); text != "" {
		setPrimarySelection(chatSel.ui.window, text)
	}
}

// selectCatcher lies under the conversation, behind every message: a press
// that reaches it (in a margin, between messages, on a tool row) starts the
// selection at the nearest message's text, and the drag goes on from there.
type selectCatcher struct {
	widget.BaseWidget
	ui     *desktopUI
	target *selectLayer
}

func newSelectCatcher(ui *desktopUI) *selectCatcher {
	catcher := &selectCatcher{ui: ui}
	catcher.ExtendBaseWidget(catcher)
	return catcher
}

func (catcher *selectCatcher) CreateRenderer() fyne.WidgetRenderer { return emptyRenderer{} }

func (catcher *selectCatcher) MouseDown(e *desktop.MouseEvent) {
	catcher.target = nil
	if e.Button == desktop.MouseButtonSecondary {
		catcher.ui.chatSel.menu(e.AbsolutePosition)
		return
	}
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	layers := catcher.ui.chatSel.layers()
	i := nearest(layers, e.AbsolutePosition)
	if i < 0 {
		catcher.ui.clearSelection()
		return
	}
	local := *e
	local.Position = e.AbsolutePosition.Subtract(layers[i].at)
	catcher.target = layers[i].layer
	catcher.target.MouseDown(&local)
}

func (catcher *selectCatcher) MouseUp(e *desktop.MouseEvent) {
	if catcher.target != nil && e.Button == desktop.MouseButtonPrimary {
		catcher.ui.chatSel.offer()
	}
}

func (catcher *selectCatcher) Dragged(d *fyne.DragEvent) {
	if catcher.target == nil {
		return
	}
	local := *d
	local.Position = d.AbsolutePosition.Subtract(fyne.CurrentApp().Driver().AbsolutePositionForObject(catcher.target))
	catcher.target.Dragged(&local)
}

func (catcher *selectCatcher) DragEnd() {}

// emptyRenderer draws nothing: the catcher only takes the mouse.
type emptyRenderer struct{}

func (emptyRenderer) Layout(fyne.Size)             {}
func (emptyRenderer) MinSize() fyne.Size           { return fyne.Size{} }
func (emptyRenderer) Refresh()                     {}
func (emptyRenderer) Objects() []fyne.CanvasObject { return nil }
func (emptyRenderer) Destroy()                     {}
