//go:build !server

package main

// Text selection in answers. Fyne's rich text (which draws the answers:
// paragraphs, lists, inline styles) cannot be selected, and its Label can,
// but only one at a time and in a single style. selectLayer lies over a
// whole answer instead: the mouse selects across paragraphs, lists, code
// blocks and tables alike, Ctrl+C copies what is highlighted.
//
// The widgets below say what they have drawn: each implements selSource and
// lists its pieces of text (a run of one style on one row, with its place).
// The layer collects them in reading order when the pointer comes over the
// answer, and works from their geometry alone.
//
// Lying on top, the layer receives the mouse. It keeps the presses and the
// drags for the selection, and passes the rest on to what is below: a plain
// click goes to the button or the link under it, and so does hovering, so
// copy buttons light up and links show the hand.

import (
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// textSelection is something that holds the application's one selection.
type textSelection interface {
	selectedText() string
	clearSelection()
}

// selectIn makes s the holder of the selection: there is only one at a
// time, as in any application.
func (ui *desktopUI) selectIn(textSelectionValue textSelection) {
	if ui.selected != nil && ui.selected != textSelectionValue {
		ui.selected.clearSelection()
	}
	ui.selected = textSelectionValue
}

func (ui *desktopUI) clearSelection() {
	if ui.selected != nil {
		ui.selected.clearSelection()
		ui.selected = nil
	}
}

// copySelection puts the selected text on the clipboard; false when
// nothing is selected.
func (ui *desktopUI) copySelection() bool {
	if ui.selected == nil {
		return false
	}
	text := ui.selected.selectedText()
	if text == "" {
		return false
	}
	fyne.CurrentApp().Clipboard().SetContent(text)
	devf("ui", "copied %d selected characters", len([]rune(text)))
	return true
}

// selPiece is one run of text as drawn: one style, one row.
type selPiece struct {
	runes    []rune
	at       fyne.Position // in its widget's coordinates, then in the layer's
	size     fyne.Size
	textSize float32
	style    fyne.TextStyle
	solid    bool   // selected whole or not at all (a formula, a list mark)
	after    string // what separates it from the next piece in copied text
}

// selSource is a widget whose text can be selected.
type selSource interface {
	selPieces() []selPiece
}

// linkSource is a widget with links in it.
type linkSource interface {
	linkAt(fyne.Position) string
}

type selTap struct {
	object fyne.CanvasObject
	at     fyne.Position
}

// selPoint is a position in the text: before rune offset of piece index.
type selPoint struct{ index, offset int }

func (point selPoint) before(selPointValue selPoint) bool {
	return point.index < selPointValue.index || (point.index == selPointValue.index && point.offset < selPointValue.offset)
}

type selectLayer struct {
	widget.BaseWidget
	ui          *desktopUI
	under       fyne.CanvasObject // what the layer covers
	pieces      []selPiece
	taps        []selTap // what can be clicked or hovered below, the topmost last
	stale       bool     // what is below was laid out again since it was collected
	from, point selPoint // where the selection began, and where the mouse is
	active      bool
	pressed     time.Time
	pressAt     fyne.Position
	dragged     bool
	byWord      bool // the press was a double click
	over        *selTap
	hand        bool // the pointer is on a link
}

func newSelectLayer(ui *desktopUI, under fyne.CanvasObject) *selectLayer {
	selection := &selectLayer{ui: ui, under: under, stale: true}
	selection.ExtendBaseWidget(selection)
	return selection
}

// gather collects the text drawn under the layer and what reacts to the
// mouse there.
func (selection *selectLayer) gather() {
	selection.pieces, selection.taps, selection.stale, selection.over = selection.pieces[:0], selection.taps[:0], false, nil
	selection.collect(selection.under, fyne.Position{})
}

func (selection *selectLayer) collect(object fyne.CanvasObject, origin fyne.Position) {
	if object == nil || !object.Visible() {
		return
	}
	_, taps := object.(fyne.Tappable)
	_, hovers := object.(desktop.Hoverable)
	if taps || hovers {
		selection.taps = append(selection.taps, selTap{object, origin})
	}
	if source, ok := object.(selSource); ok {
		for _, piece := range source.selPieces() {
			piece.at = piece.at.Add(origin)
			selection.pieces = append(selection.pieces, piece)
		}
		return
	}
	switch o := object.(type) {
	case *fyne.Container:
		for _, child := range o.Objects {
			selection.collect(child, origin.Add(child.Position()))
		}
	case *kineticScroll:
		selection.collect(o.Content, origin.Add(o.Content.Position()))
	case *container.Scroll:
		selection.collect(o.Content, origin.Add(o.Content.Position()))
	}
}

// offsetAt is the rune boundary of a piece nearest to x (from its left).
func (piece selPiece) offsetAt(x float32) int {
	if piece.solid {
		if x > piece.size.Width/2 {
			return len(piece.runes)
		}
		return 0
	}
	if x <= 0 {
		return 0
	}
	if x >= piece.size.Width {
		return len(piece.runes)
	}
	low, high := 0, len(piece.runes) // the boundary is in [low, high]
	for high-low > 1 {
		mid := (low + high) / 2
		if piece.width(mid) <= x {
			low = mid
		} else {
			high = mid
		}
	}
	if x-piece.width(low) > piece.width(high)-x {
		return high
	}
	return low
}

// width is how wide the first n runes of the piece are.
func (piece selPiece) width(n int) float32 {
	if n <= 0 {
		return 0
	}
	if n >= len(piece.runes) || piece.solid {
		return piece.size.Width
	}
	return textSize(string(piece.runes[:n]), piece.textSize, piece.style).Width
}

// locate is the text position nearest to a point of the layer: on the row
// the point is level with (else the closest one), the piece it is over or
// the nearest end.
func (selection *selectLayer) locate(at fyne.Position) selPoint {
	best, bestY, bestX := -1, float32(math.MaxFloat32), float32(math.MaxFloat32)
	for i, piece := range selection.pieces {
		dy := float32(0)
		if at.Y < piece.at.Y {
			dy = piece.at.Y - at.Y
		} else if at.Y > piece.at.Y+piece.size.Height {
			dy = at.Y - piece.at.Y - piece.size.Height
		}
		dx := float32(0)
		if at.X < piece.at.X {
			dx = piece.at.X - at.X
		} else if at.X > piece.at.X+piece.size.Width {
			dx = at.X - piece.at.X - piece.size.Width
		}
		if dy < bestY || (dy == bestY && dx < bestX) {
			best, bestY, bestX = i, dy, dx
		}
	}
	if best < 0 {
		return selPoint{}
	}
	return selPoint{best, selection.pieces[best].offsetAt(at.X - selection.pieces[best].at.X)}
}

func (selection *selectLayer) span() (start, end selPoint) {
	if selection.point.before(selection.from) {
		return selection.point, selection.from
	}
	return selection.from, selection.point
}

// below is what reacts to the mouse under a point, the topmost first found.
func (selection *selectLayer) below(at fyne.Position) *selTap {
	if selection.stale {
		selection.gather()
	}
	for i := len(selection.taps) - 1; i >= 0; i-- { // the last drawn is on top
		tap := &selection.taps[i]
		size := tap.object.Size()
		if at.X >= tap.at.X && at.Y >= tap.at.Y && at.X < tap.at.X+size.Width && at.Y < tap.at.Y+size.Height {
			return tap
		}
	}
	return nil
}

// Hovering is passed on to what is below, and decides the pointer: the
// hand on a link or a button, the text cursor elsewhere.
func (selection *selectLayer) MouseIn(e *desktop.MouseEvent) {
	selection.stale = true
	selection.MouseMoved(e)
}

func (selection *selectLayer) MouseMoved(e *desktop.MouseEvent) {
	tap := selection.below(e.Position)
	if tap != selection.over {
		selection.leave()
		selection.over = tap
		if tap != nil {
			if hover, ok := tap.object.(desktop.Hoverable); ok {
				hover.MouseIn(selection.local(e, tap))
			}
		}
	} else if tap != nil {
		if hover, ok := tap.object.(desktop.Hoverable); ok {
			hover.MouseMoved(selection.local(e, tap))
		}
	}
	selection.hand = false
	if tap != nil {
		if links, ok := tap.object.(linkSource); ok {
			selection.hand = links.linkAt(e.Position.Subtract(tap.at)) != ""
		} else if _, ok := tap.object.(fyne.Tappable); ok {
			selection.hand = true
		}
	}
}

func (selection *selectLayer) MouseOut() { selection.leave(); selection.hand = false }

func (selection *selectLayer) leave() {
	if selection.over != nil {
		if hover, ok := selection.over.object.(desktop.Hoverable); ok {
			hover.MouseOut()
		}
		selection.over = nil
	}
}

func (selection *selectLayer) local(e *desktop.MouseEvent, tap *selTap) *desktop.MouseEvent {
	local := *e
	local.Position = e.Position.Subtract(tap.at)
	return &local
}

func (selection *selectLayer) Cursor() desktop.Cursor {
	if selection.hand {
		return desktop.PointerCursor
	}
	return desktop.TextCursor
}

func (selection *selectLayer) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	selection.ui.selectIn(selection)
	selection.gather()
	now := time.Now()
	again := now.Sub(selection.pressed) < 400*time.Millisecond && distance(e.Position, selection.pressAt) < 4
	selection.pressed, selection.pressAt, selection.dragged, selection.byWord = now, e.Position, false, false
	point := selection.locate(e.Position)
	selection.from, selection.point, selection.active = point, point, len(selection.pieces) > 0
	if again && selection.active { // double click: the word under the pointer
		selection.byWord = true
		runes := selection.pieces[point.index].runes
		start, end := point.offset, point.offset
		for start > 0 && !isBreak(runes[start-1]) {
			start--
		}
		for end < len(runes) && !isBreak(runes[end]) {
			end++
		}
		selection.from, selection.point = selPoint{point.index, start}, selPoint{point.index, end}
	}
	selection.Refresh()
}

func isBreak(r rune) bool {
	return r == ' ' || r == '\t' || strings.ContainsRune(`.,;:!?()[]{}"'«»`, r)
}

func distance(position, positionValue fyne.Position) float64 {
	return math.Hypot(float64(position.X-positionValue.X), float64(position.Y-positionValue.Y))
}

// MouseUp passes a plain click on to what is under the layer.
func (selection *selectLayer) MouseUp(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary || selection.dragged || selection.byWord {
		return
	}
	if tap := selection.below(e.Position); tap != nil {
		if target, ok := tap.object.(fyne.Tappable); ok {
			target.Tapped(&fyne.PointEvent{Position: e.Position.Subtract(tap.at), AbsolutePosition: e.AbsolutePosition})
		}
	}
}

func (selection *selectLayer) Dragged(d *fyne.DragEvent) {
	if !selection.active {
		return
	}
	selection.dragged = true
	selection.point = selection.locate(d.Position)
	selection.Refresh()
}

func (selection *selectLayer) DragEnd() {}

// changed is called when the text below was laid out again (an answer
// still being written): the selection keeps its place in it.
func (selection *selectLayer) changed() {
	selection.stale = true
	if !selection.active {
		return
	}
	selection.gather()
	last := selPoint{len(selection.pieces) - 1, 0}
	if len(selection.pieces) == 0 {
		selection.active = false
	} else {
		last.offset = len(selection.pieces[last.index].runes)
		for _, point := range []*selPoint{&selection.from, &selection.point} {
			if last.before(*point) || point.offset > len(selection.pieces[point.index].runes) {
				*point = last
			}
		}
	}
	selection.Refresh()
}

func (selection *selectLayer) clearSelection() {
	if selection.active {
		selection.active = false
		selection.Refresh()
	}
}

// selectedText is the selection as text. Each piece says what separates it
// from the next: nothing inside a row, a space where a line wraps, a line
// break or a blank line at the end of a block.
func (selection *selectLayer) selectedText() string {
	if !selection.active {
		return ""
	}
	start, end := selection.span()
	var builder strings.Builder
	for i := start.index; i <= end.index && i < len(selection.pieces); i++ {
		piece := selection.pieces[i]
		a, z := 0, len(piece.runes)
		if i == start.index {
			a = start.offset
		}
		if i == end.index {
			z = end.offset
		}
		if a > z {
			continue
		}
		builder.WriteString(string(piece.runes[a:z]))
		// What follows a piece taken to its end, except when the selection
		// only starts there.
		if i < end.index && z == len(piece.runes) && !(i == start.index && a == z && z > 0) {
			builder.WriteString(piece.after)
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

func (selection *selectLayer) CreateRenderer() fyne.WidgetRenderer {
	return &selectRenderer{layer: selection}
}

type selectRenderer struct {
	layer *selectLayer
	marks []*canvas.Rectangle
	size  fyne.Size
}

func (renderer *selectRenderer) MinSize() fyne.Size { return fyne.Size{} }

// Layout: a new width wraps the text below differently, so what was
// selected is no longer where the marks are.
func (renderer *selectRenderer) Layout(size fyne.Size) {
	renderer.layer.stale = true
	if size.Width != renderer.size.Width && renderer.layer.active {
		renderer.layer.active = false
		renderer.Refresh()
	}
	renderer.size = size
}

func (renderer *selectRenderer) Refresh() {
	selection := renderer.layer
	used := 0
	if selection.active {
		ink := theme.Color(theme.ColorNameSelection)
		start, end := selection.span()
		for i := start.index; i <= end.index && i < len(selection.pieces); i++ {
			piece := selection.pieces[i]
			a, z := 0, len(piece.runes)
			if i == start.index {
				a = start.offset
			}
			if i == end.index {
				z = end.offset
			}
			if a >= z {
				continue
			}
			if used == len(renderer.marks) {
				renderer.marks = append(renderer.marks, canvas.NewRectangle(ink))
			}
			mark := renderer.marks[used]
			used++
			left, right := piece.width(a), piece.width(z)
			mark.FillColor = ink
			mark.Move(fyne.NewPos(piece.at.X+left, piece.at.Y-1))
			mark.Resize(fyne.NewSize(right-left, piece.size.Height+2))
			mark.Show()
			mark.Refresh()
		}
	}
	for _, mark := range renderer.marks[used:] {
		mark.Hide()
	}
	canvas.Refresh(selection)
}

func (renderer *selectRenderer) Objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, len(renderer.marks))
	for i, mark := range renderer.marks {
		objects[i] = mark
	}
	return objects
}

func (renderer *selectRenderer) Destroy() {}
