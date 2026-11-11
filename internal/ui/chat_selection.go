// SPDX-License-Identifier: MPL-2.0

package ui

// Selection across the conversation. Each message has its own selectLayer;
// chatSelection joins them: a drag that starts in one message and moves
// into others selects the rest of the first, every message in between and
// the start of the last, and Ctrl+C copies them in reading order. A press
// in the conversation's margins, or between two messages, starts at the
// nearest message's text (selectCatcher), as in a text editor.

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

type chatSelection struct {
	ui     *desktopUI
	anchor *selectLayer   // where the press was
	marked []*selectLayer // the layers holding part of the selection, in reading order
}

// start makes a press in layer the beginning of a new selection.
func (chatSel *chatSelection) start(layer *selectLayer) {
	chatSel.ui.selectIn(chatSel)
	for _, other := range chatSel.marked {
		if other != layer {
			other.clearSelection()
		}
	}
	chatSel.anchor, chatSel.marked = layer, []*selectLayer{layer}
}

// placed is a layer of the conversation with its place on the screen.
type placed struct {
	layer *selectLayer
	at    fyne.Position
}

// layers are the conversation's selection layers on screen, in reading order.
func (chatSel *chatSelection) layers() []placed {
	var found []placed
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		if object == nil || !object.Visible() {
			return
		}
		switch o := object.(type) {
		case *selectLayer:
			found = append(found, placed{layer: o})
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		}
	}
	if chatSel.ui.transcript != nil {
		walk(chatSel.ui.transcript)
	}
	if app := fyne.CurrentApp(); app != nil {
		driver := app.Driver()
		for i := range found {
			found[i].at = driver.AbsolutePositionForObject(found[i].layer)
		}
	}
	return found
}

// nearest is the layer at the height of a point on the screen, else the
// closest one above or below it. -1 when there is none.
func nearest(layers []placed, at fyne.Position) int {
	best, bestDistance := -1, float32(0)
	for i, candidate := range layers {
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

// drag moves the selection's moving end to the pointer, wherever it is in
// the conversation, and scrolls when the pointer goes past its top or bottom.
func (chatSel *chatSelection) drag(layer *selectLayer, d *fyne.DragEvent) {
	if chatSel.anchor != layer {
		chatSel.start(layer)
	}
	chatSel.follow(d.AbsolutePosition)
	layers := chatSel.layers()
	from, to := -1, nearest(layers, d.AbsolutePosition)
	for i, candidate := range layers {
		if candidate.layer == layer {
			from = i
		}
	}
	top, bottom := float32(0), float32(0)
	if from >= 0 {
		top = layers[from].at.Y
		bottom = top + layer.Size().Height
	}
	// Within the press's own message (or level with it): only that one.
	if from < 0 || to < 0 || to == from || (d.AbsolutePosition.Y >= top && d.AbsolutePosition.Y <= bottom) {
		for _, other := range chatSel.marked {
			if other != layer {
				other.clearSelection()
			}
		}
		chatSel.marked = []*selectLayer{layer}
		layer.point = layer.locate(d.Position)
		layer.Refresh()
		return
	}
	forward := to > from
	first, last := from, to
	if !forward {
		first, last = to, from
	}
	keep := map[*selectLayer]bool{}
	var marked []*selectLayer
	for i := first; i <= last; i++ {
		current := layers[i].layer
		if current.stale {
			current.gather()
		}
		keep[current] = true
		marked = append(marked, current)
		switch {
		case i == from: // the press's message: from the press to its end or start
			current.mark(current.from, current.edge(forward))
		case i == to: // the pointer's message: from its start or end to the pointer
			current.mark(current.edge(!forward), current.locate(d.AbsolutePosition.Subtract(layers[i].at)))
		default:
			current.mark(current.edge(false), current.edge(true))
		}
	}
	for _, other := range chatSel.marked {
		if !keep[other] {
			other.clearSelection()
		}
	}
	chatSel.marked = marked
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
	chatSel.anchor, chatSel.marked = nil, nil
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

func (catcher *selectCatcher) MouseUp(*desktop.MouseEvent) {}

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
