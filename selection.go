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
func (u *desktopUI) selectIn(s textSelection) {
	if u.selected != nil && u.selected != s {
		u.selected.clearSelection()
	}
	u.selected = s
}

func (u *desktopUI) clearSelection() {
	if u.selected != nil {
		u.selected.clearSelection()
		u.selected = nil
	}
}

// copySelection puts the selected text on the clipboard; false when
// nothing is selected.
func (u *desktopUI) copySelection() bool {
	if u.selected == nil {
		return false
	}
	text := u.selected.selectedText()
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

func (p selPoint) before(q selPoint) bool {
	return p.index < q.index || (p.index == q.index && p.offset < q.offset)
}

type selectLayer struct {
	widget.BaseWidget
	u        *desktopUI
	under    fyne.CanvasObject // what the layer covers
	pieces   []selPiece
	taps     []selTap // what can be clicked or hovered below, the topmost last
	stale    bool     // what is below was laid out again since it was collected
	from, to selPoint // where the selection began, and where the mouse is
	active   bool
	pressed  time.Time
	pressAt  fyne.Position
	dragged  bool
	byWord   bool // the press was a double click
	over     *selTap
	hand     bool // the pointer is on a link
}

func newSelectLayer(u *desktopUI, under fyne.CanvasObject) *selectLayer {
	l := &selectLayer{u: u, under: under, stale: true}
	l.ExtendBaseWidget(l)
	return l
}

// gather collects the text drawn under the layer and what reacts to the
// mouse there.
func (l *selectLayer) gather() {
	l.pieces, l.taps, l.stale, l.over = l.pieces[:0], l.taps[:0], false, nil
	l.collect(l.under, fyne.Position{})
}

func (l *selectLayer) collect(object fyne.CanvasObject, origin fyne.Position) {
	if object == nil || !object.Visible() {
		return
	}
	_, taps := object.(fyne.Tappable)
	_, hovers := object.(desktop.Hoverable)
	if taps || hovers {
		l.taps = append(l.taps, selTap{object, origin})
	}
	if source, ok := object.(selSource); ok {
		for _, piece := range source.selPieces() {
			piece.at = piece.at.Add(origin)
			l.pieces = append(l.pieces, piece)
		}
		return
	}
	switch o := object.(type) {
	case *fyne.Container:
		for _, child := range o.Objects {
			l.collect(child, origin.Add(child.Position()))
		}
	case *kineticScroll:
		l.collect(o.Content, origin.Add(o.Content.Position()))
	case *container.Scroll:
		l.collect(o.Content, origin.Add(o.Content.Position()))
	}
}

// offsetAt is the rune boundary of a piece nearest to x (from its left).
func (p selPiece) offsetAt(x float32) int {
	if p.solid {
		if x > p.size.Width/2 {
			return len(p.runes)
		}
		return 0
	}
	if x <= 0 {
		return 0
	}
	if x >= p.size.Width {
		return len(p.runes)
	}
	low, high := 0, len(p.runes) // the boundary is in [low, high]
	for high-low > 1 {
		mid := (low + high) / 2
		if p.width(mid) <= x {
			low = mid
		} else {
			high = mid
		}
	}
	if x-p.width(low) > p.width(high)-x {
		return high
	}
	return low
}

// width is how wide the first n runes of the piece are.
func (p selPiece) width(n int) float32 {
	if n <= 0 {
		return 0
	}
	if n >= len(p.runes) || p.solid {
		return p.size.Width
	}
	return fyne.MeasureText(string(p.runes[:n]), p.textSize, p.style).Width
}

// locate is the text position nearest to a point of the layer: on the row
// the point is level with (else the closest one), the piece it is over or
// the nearest end.
func (l *selectLayer) locate(at fyne.Position) selPoint {
	best, bestY, bestX := -1, float32(math.MaxFloat32), float32(math.MaxFloat32)
	for i, p := range l.pieces {
		dy := float32(0)
		if at.Y < p.at.Y {
			dy = p.at.Y - at.Y
		} else if at.Y > p.at.Y+p.size.Height {
			dy = at.Y - p.at.Y - p.size.Height
		}
		dx := float32(0)
		if at.X < p.at.X {
			dx = p.at.X - at.X
		} else if at.X > p.at.X+p.size.Width {
			dx = at.X - p.at.X - p.size.Width
		}
		if dy < bestY || (dy == bestY && dx < bestX) {
			best, bestY, bestX = i, dy, dx
		}
	}
	if best < 0 {
		return selPoint{}
	}
	return selPoint{best, l.pieces[best].offsetAt(at.X - l.pieces[best].at.X)}
}

func (l *selectLayer) span() (start, end selPoint) {
	if l.to.before(l.from) {
		return l.to, l.from
	}
	return l.from, l.to
}

// below is what reacts to the mouse under a point, the topmost first found.
func (l *selectLayer) below(at fyne.Position) *selTap {
	if l.stale {
		l.gather()
	}
	for i := len(l.taps) - 1; i >= 0; i-- { // the last drawn is on top
		tap := &l.taps[i]
		size := tap.object.Size()
		if at.X >= tap.at.X && at.Y >= tap.at.Y && at.X < tap.at.X+size.Width && at.Y < tap.at.Y+size.Height {
			return tap
		}
	}
	return nil
}

// Hovering is passed on to what is below, and decides the pointer: the
// hand on a link or a button, the text cursor elsewhere.
func (l *selectLayer) MouseIn(e *desktop.MouseEvent) {
	l.stale = true
	l.MouseMoved(e)
}

func (l *selectLayer) MouseMoved(e *desktop.MouseEvent) {
	tap := l.below(e.Position)
	if tap != l.over {
		l.leave()
		l.over = tap
		if tap != nil {
			if hover, ok := tap.object.(desktop.Hoverable); ok {
				hover.MouseIn(l.local(e, tap))
			}
		}
	} else if tap != nil {
		if hover, ok := tap.object.(desktop.Hoverable); ok {
			hover.MouseMoved(l.local(e, tap))
		}
	}
	l.hand = false
	if tap != nil {
		if links, ok := tap.object.(linkSource); ok {
			l.hand = links.linkAt(e.Position.Subtract(tap.at)) != ""
		} else if _, ok := tap.object.(fyne.Tappable); ok {
			l.hand = true
		}
	}
}

func (l *selectLayer) MouseOut() { l.leave(); l.hand = false }

func (l *selectLayer) leave() {
	if l.over != nil {
		if hover, ok := l.over.object.(desktop.Hoverable); ok {
			hover.MouseOut()
		}
		l.over = nil
	}
}

func (l *selectLayer) local(e *desktop.MouseEvent, tap *selTap) *desktop.MouseEvent {
	local := *e
	local.Position = e.Position.Subtract(tap.at)
	return &local
}

func (l *selectLayer) Cursor() desktop.Cursor {
	if l.hand {
		return desktop.PointerCursor
	}
	return desktop.TextCursor
}

func (l *selectLayer) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	l.u.selectIn(l)
	l.gather()
	now := time.Now()
	again := now.Sub(l.pressed) < 400*time.Millisecond && distance(e.Position, l.pressAt) < 4
	l.pressed, l.pressAt, l.dragged, l.byWord = now, e.Position, false, false
	at := l.locate(e.Position)
	l.from, l.to, l.active = at, at, len(l.pieces) > 0
	if again && l.active { // double click: the word under the pointer
		l.byWord = true
		runes := l.pieces[at.index].runes
		start, end := at.offset, at.offset
		for start > 0 && !isBreak(runes[start-1]) {
			start--
		}
		for end < len(runes) && !isBreak(runes[end]) {
			end++
		}
		l.from, l.to = selPoint{at.index, start}, selPoint{at.index, end}
	}
	l.Refresh()
}

func isBreak(r rune) bool {
	return r == ' ' || r == '\t' || strings.ContainsRune(`.,;:!?()[]{}"'«»`, r)
}

func distance(a, b fyne.Position) float64 {
	return math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y))
}

// MouseUp passes a plain click on to what is under the layer.
func (l *selectLayer) MouseUp(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary || l.dragged || l.byWord {
		return
	}
	if tap := l.below(e.Position); tap != nil {
		if target, ok := tap.object.(fyne.Tappable); ok {
			target.Tapped(&fyne.PointEvent{Position: e.Position.Subtract(tap.at), AbsolutePosition: e.AbsolutePosition})
		}
	}
}

func (l *selectLayer) Dragged(d *fyne.DragEvent) {
	if !l.active {
		return
	}
	l.dragged = true
	l.to = l.locate(d.Position)
	l.Refresh()
}

func (l *selectLayer) DragEnd() {}

// changed is called when the text below was laid out again (an answer
// still being written): the selection keeps its place in it.
func (l *selectLayer) changed() {
	l.stale = true
	if !l.active {
		return
	}
	l.gather()
	last := selPoint{len(l.pieces) - 1, 0}
	if len(l.pieces) == 0 {
		l.active = false
	} else {
		last.offset = len(l.pieces[last.index].runes)
		for _, point := range []*selPoint{&l.from, &l.to} {
			if last.before(*point) || point.offset > len(l.pieces[point.index].runes) {
				*point = last
			}
		}
	}
	l.Refresh()
}

func (l *selectLayer) clearSelection() {
	if l.active {
		l.active = false
		l.Refresh()
	}
}

// selectedText is the selection as text. Each piece says what separates it
// from the next: nothing inside a row, a space where a line wraps, a line
// break or a blank line at the end of a block.
func (l *selectLayer) selectedText() string {
	if !l.active {
		return ""
	}
	start, end := l.span()
	var b strings.Builder
	for i := start.index; i <= end.index && i < len(l.pieces); i++ {
		p := l.pieces[i]
		a, z := 0, len(p.runes)
		if i == start.index {
			a = start.offset
		}
		if i == end.index {
			z = end.offset
		}
		if a > z {
			continue
		}
		b.WriteString(string(p.runes[a:z]))
		// What follows a piece taken to its end, except when the selection
		// only starts there.
		if i < end.index && z == len(p.runes) && !(i == start.index && a == z && z > 0) {
			b.WriteString(p.after)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (l *selectLayer) CreateRenderer() fyne.WidgetRenderer {
	return &selectRenderer{layer: l}
}

type selectRenderer struct {
	layer *selectLayer
	marks []*canvas.Rectangle
	size  fyne.Size
}

func (r *selectRenderer) MinSize() fyne.Size { return fyne.Size{} }

// Layout: a new width wraps the text below differently, so what was
// selected is no longer where the marks are.
func (r *selectRenderer) Layout(size fyne.Size) {
	r.layer.stale = true
	if size.Width != r.size.Width && r.layer.active {
		r.layer.active = false
		r.Refresh()
	}
	r.size = size
}

func (r *selectRenderer) Refresh() {
	l := r.layer
	used := 0
	if l.active {
		ink := theme.Color(theme.ColorNameSelection)
		start, end := l.span()
		for i := start.index; i <= end.index && i < len(l.pieces); i++ {
			p := l.pieces[i]
			a, z := 0, len(p.runes)
			if i == start.index {
				a = start.offset
			}
			if i == end.index {
				z = end.offset
			}
			if a >= z {
				continue
			}
			if used == len(r.marks) {
				r.marks = append(r.marks, canvas.NewRectangle(ink))
			}
			mark := r.marks[used]
			used++
			left, right := p.width(a), p.width(z)
			mark.FillColor = ink
			mark.Move(fyne.NewPos(p.at.X+left, p.at.Y-1))
			mark.Resize(fyne.NewSize(right-left, p.size.Height+2))
			mark.Show()
			mark.Refresh()
		}
	}
	for _, mark := range r.marks[used:] {
		mark.Hide()
	}
	canvas.Refresh(l)
}

func (r *selectRenderer) Objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, len(r.marks))
	for i, mark := range r.marks {
		objects[i] = mark
	}
	return objects
}

func (r *selectRenderer) Destroy() {}
