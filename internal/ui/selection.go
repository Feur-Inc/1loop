// SPDX-License-Identifier: MPL-2.0

package ui

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
//
// The gestures are those of any program: a click places the caret, a
// double click selects a word, a triple click a paragraph (a list item, a
// table cell, a line of code), and a drag after them extends by the same
// unit; Shift+click extends to the click, Ctrl+A selects the conversation
// (chat_selection.go), a right click offers Copy (selection_menu.go). The
// units themselves are in selection_units.go.

import (
	"math"
	"runtime"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/platform"
)

// textSelection is something that holds the application's one selection.
type textSelection interface {
	selectedText() string
	selectedMarkdown() string
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

// selectionKey takes the keys typed with no field focused that are the
// selection's: Escape lets it go.
func (ui *desktopUI) selectionKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		ui.focused().clearSelection()
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
	platform.Devf("ui", "copied %d selected characters", len([]rune(text)))
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
	mark     bool   // a list item's bullet or number: not part of its paragraph
	span     *span  // the Markdown span it draws (Copy as Markdown), nil for plain text
	block    int    // the answer's block it is in (set by the layer)
	after    string // what separates it from the next piece in copied text
	// clipped: the piece is inside a side scroll (a code block), which shows
	// it only between left and right, in the layer's coordinates.
	clipped     bool
	left, right float32
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
	ui     *desktopUI
	under  fyne.CanvasObject // what the layer covers
	pieces []selPiece
	text   selText  // the pieces' text as copied (selection_units.go)
	taps   []selTap // what can be clicked or hovered below, the topmost last
	stale  bool     // what is below was laid out again since it was collected
	// The selection is [lo, hi) of text, hi < 0 up to its end while the text
	// is not known (a message out of sight). kept is what it covers: when
	// the text is laid out again (a new width, an answer still being
	// written), the selection is found again by it, and goes away only when
	// it is no longer there.
	active  bool
	lo, hi  int
	kept    []rune
	dragged bool
	plain   bool // the last press was a plain click: its release goes to what is below
	over    *selTap
	hand    bool    // the pointer is on a link
	onBar   bool    // the pointer is on a scrollbar (#633)
	grabbed *selTap // the scrollbar the press was on: it gets the drag
}

func newSelectLayer(ui *desktopUI, under fyne.CanvasObject) *selectLayer {
	selection := &selectLayer{ui: ui, under: under, stale: true}
	selection.ExtendBaseWidget(selection)
	return selection
}

// gather collects the text drawn under the layer and what reacts to the
// mouse there, and finds the selection again in it.
func (selection *selectLayer) gather() {
	before := selection.pieces
	selection.pieces, selection.taps, selection.stale, selection.over = nil, selection.taps[:0], false, nil
	if box, ok := selection.under.(*fyne.Container); ok && box.Visible() {
		// An answer's blocks: each piece notes which it is in.
		for block, child := range box.Objects {
			first := len(selection.pieces)
			selection.collect(child, child.Position(), nil)
			for i := first; i < len(selection.pieces); i++ {
				selection.pieces[i].block = block
			}
		}
	} else {
		selection.collect(selection.under, fyne.Position{}, nil)
	}
	if len(selection.pieces) == 0 && len(before) > 0 && selection.active {
		// A message out of sight drops its words (flowText.release): its
		// selection stays as it was until it is laid out again.
		selection.pieces = before
		return
	}
	selection.text = textOf(selection.pieces)
	selection.refind()
}

// refind finds the selection again in the text just collected: where it
// was, else where its text now is (text was added or changed before it).
func (selection *selectLayer) refind() {
	if !selection.active {
		return
	}
	runes := selection.text.runes
	if selection.hi < 0 { // selected whole before its text was known
		selection.lo, selection.hi = 0, len(runes)
		selection.kept = append(selection.kept[:0], runes...)
		return
	}
	if selection.hi <= len(runes) && slices.Equal(runes[selection.lo:selection.hi], selection.kept) {
		return
	}
	at := nearestRunes(runes, selection.kept, selection.lo)
	if at < 0 {
		selection.active = false
		return
	}
	shift := at - selection.lo
	selection.lo, selection.hi = at, at+len(selection.kept)
	if chatSel := &selection.ui.chatSel; chatSel.anchor == selection {
		chatSel.lo, chatSel.hi = chatSel.lo+shift, chatSel.hi+shift
	}
}

// nearestRunes is where needle is in text nearest to near, -1 if nowhere.
func nearestRunes(text, needle []rune, near int) int {
	best := -1
	for at := 0; at+len(needle) <= len(text); at++ {
		if slices.Equal(text[at:at+len(needle)], needle) && (best < 0 || abs(at-near) < abs(best-near)) {
			best = at
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// known makes sure the layer has its text, if need be by laying out what
// is out of sight for a moment (a whole message copied, scrolled away).
func (selection *selectLayer) known() {
	if selection.stale || len(selection.pieces) == 0 {
		selection.gather()
	}
	if len(selection.pieces) > 0 {
		return
	}
	var flows []*flowText
	eachFlow(selection.under, func(flow *flowText) {
		if flow.pieces == nil && !flow.measuring && flow.width > 0 {
			flow.flow(flow.width)
			flows = append(flows, flow)
		}
	})
	selection.gather()
	for _, flow := range flows {
		flow.release()
	}
}

// eachFlow calls f for each block of text under object.
func eachFlow(object fyne.CanvasObject, f func(*flowText)) {
	switch o := object.(type) {
	case *flowText:
		f(o)
	case *markdownTable:
		for _, row := range o.cells {
			for _, cell := range row {
				f(cell)
			}
		}
	case *fyne.Container:
		for _, child := range o.Objects {
			eachFlow(child, f)
		}
	case *kineticScroll:
		eachFlow(o.Content, f)
	case *container.Scroll:
		eachFlow(o.Content, f)
	case *sideScroll:
		eachFlow(o.Content, f)
	}
}

// collect walks what is drawn under the layer. clip is the horizontal span
// a side scroll shows of what is inside it, nil outside one.
func (selection *selectLayer) collect(object fyne.CanvasObject, origin fyne.Position, clip *[2]float32) {
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
			if clip != nil {
				piece.clipped, piece.left, piece.right = true, clip[0], clip[1]
			}
			selection.pieces = append(selection.pieces, piece)
		}
		return
	}
	switch o := object.(type) {
	case *fyne.Container:
		for _, child := range o.Objects {
			selection.collect(child, origin.Add(child.Position()), clip)
		}
	case *kineticScroll:
		selection.collect(o.Content, origin.Add(o.Content.Position()), clip)
	case *container.Scroll:
		selection.collect(o.Content, origin.Add(o.Content.Position()), clip)
	case *sideScroll: // a code block: its lines scroll sideways
		shown := [2]float32{origin.X, origin.X + o.Size().Width}
		if clip != nil {
			shown = [2]float32{fyne.Max(shown[0], clip[0]), fyne.Min(shown[1], clip[1])}
		}
		selection.collect(o.Content, origin.Add(o.Content.Position()), &shown)
		for _, bar := range o.Bars() { // drawn over its lines (#633)
			selection.taps = append(selection.taps, selTap{bar, origin.Add(bar.Position())})
		}
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

// runeAt is the index of the rune under x (from the piece's left).
func (piece selPiece) runeAt(x float32) int {
	if piece.solid || x <= 0 || len(piece.runes) == 0 {
		return 0
	}
	low, high := 0, len(piece.runes)-1 // the rune is in [low, high]
	for low < high {
		mid := (low + high + 1) / 2
		if piece.width(mid) <= x {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return low
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

// caretAt is the place in the text nearest to a point of the layer.
func (selection *selectLayer) caretAt(at fyne.Position) int {
	return selection.text.at(selection.locate(at), false)
}

// hitAt is the index in the text of the rune under a point of the layer
// (the nearest one when the point is beside the text).
func (selection *selectLayer) hitAt(at fyne.Position) int {
	point := selection.locate(at)
	if len(selection.pieces) == 0 {
		return 0
	}
	piece := selection.pieces[point.index]
	return selection.text.starts[point.index] + piece.runeAt(at.X-piece.at.X)
}

// unitAt is the unit (selection_units.go) around the rune at index i. A
// paragraph leaves out a list item's bullet; an empty line of code is empty.
func (selection *selectLayer) unitAt(unit, i int) (int, int) {
	text := selection.text
	if point := text.point(i, false); point.index < len(selection.pieces) && len(selection.pieces[point.index].runes) == 0 {
		return text.starts[point.index], text.starts[point.index]
	}
	start, end := text.unitAt(unit, i)
	if unit == byParagraph {
		for n, piece := range selection.pieces {
			if piece.mark && text.starts[n] == start && text.ends[n] < end {
				start = text.ends[n]
			}
		}
	}
	return start, end
}

// reach is where a selection extended to a point of the layer ends: the
// caret, or the far end of the unit under the pointer going forward, its
// near end going back.
func (selection *selectLayer) reach(at fyne.Position, unit int, forward bool) int {
	if unit == byChar {
		return selection.caretAt(at)
	}
	start, end := selection.unitAt(unit, selection.hitAt(at))
	if forward {
		return end
	}
	return start
}

// span is the selection's ends among the pieces.
func (selection *selectLayer) span() (start, end selPoint) {
	if selection.hi < 0 {
		return selection.text.point(selection.lo, false), selection.edge(true)
	}
	return selection.text.point(selection.lo, false), selection.text.point(selection.hi, true)
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
	selection.ui.activate()
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
	selection.hand, selection.onBar = false, false
	if tap != nil {
		_, selection.onBar = tap.object.(fyne.Draggable)
	}
	link := ""
	if tap != nil && !selection.onBar {
		if links, ok := tap.object.(linkSource); ok {
			link = links.linkAt(e.Position.Subtract(tap.at))
			selection.hand = link != ""
		} else if _, ok := tap.object.(fyne.Tappable); ok {
			selection.hand = true
		}
	}
	hoverReference(link, e.AbsolutePosition)
}

func (selection *selectLayer) MouseOut() {
	selection.leave()
	selection.hand = false
	hoverReference("", fyne.Position{})
}

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
	if selection.onBar {
		return desktop.DefaultCursor
	}
	if selection.hand {
		return desktop.PointerCursor
	}
	return desktop.TextCursor
}

func (selection *selectLayer) MouseDown(e *desktop.MouseEvent) {
	selection.ui.activate()
	selection.plain = false
	switch e.Button {
	case desktop.MouseButtonSecondary:
		selection.ui.chatSel.menu(e.AbsolutePosition)
		return
	case desktop.MouseButtonPrimary:
	default:
		return
	}
	selection.dragged, selection.grabbed = false, nil
	if tap := selection.below(e.Position); tap != nil {
		if _, ok := tap.object.(fyne.Draggable); ok { // a scrollbar: it scrolls, nothing is selected
			selection.grabbed = tap
			return
		}
	}
	selection.gather()
	chatSel := &selection.ui.chatSel
	count := chatSel.clicks.press(time.Now(), e.AbsolutePosition, clickInterval())
	if e.Modifier&fyne.KeyModifierShift != 0 && chatSel.anchor != nil {
		chatSel.clicks.reset() // Shift+click extends: no double click with the next press
		chatSel.pressed = selection
		if chatSel.extend(e.AbsolutePosition) {
			return
		}
	}
	selection.plain = count == 1
	chatSel.press(selection, e.Position, count-1) // byChar, byWord, byParagraph
}

// clickInterval is how soon a press must follow the last to make a double
// or triple click: the system's setting where Fyne knows it (macOS), else
// 400 ms, a little more than most desktops' default.
func clickInterval() time.Duration {
	if runtime.GOOS == "darwin" {
		if app := fyne.CurrentApp(); app != nil {
			if delay := app.Driver().DoubleTapDelay(); delay > 0 {
				return delay
			}
		}
	}
	return 400 * time.Millisecond
}

func distance(position, positionValue fyne.Position) float64 {
	return math.Hypot(float64(position.X-positionValue.X), float64(position.Y-positionValue.Y))
}

// MouseUp passes a plain click on to what is under the layer, and offers
// what a gesture selected as the primary selection (Linux).
func (selection *selectLayer) MouseUp(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	if selection.grabbed != nil {
		if selection.dragged {
			return // DragEnd lets the bar go
		}
		selection.grabbed = nil // a tap on the bar: it pages
	} else {
		selection.ui.chatSel.offer()
		if selection.dragged || !selection.plain {
			return
		}
	}
	if tap := selection.below(e.Position); tap != nil {
		if target, ok := tap.object.(fyne.Tappable); ok {
			target.Tapped(&fyne.PointEvent{Position: e.Position.Subtract(tap.at), AbsolutePosition: e.AbsolutePosition})
		}
	}
}

// Dragged follows the pointer, into the other messages too (chatSelection).
func (selection *selectLayer) Dragged(d *fyne.DragEvent) {
	if tap := selection.grabbed; tap != nil {
		selection.dragged = true
		local := *d
		local.Position = d.Position.Subtract(tap.at)
		tap.object.(fyne.Draggable).Dragged(&local)
		return
	}
	chatSel := &selection.ui.chatSel
	if chatSel.anchor == nil || chatSel.pressed != selection {
		return
	}
	selection.dragged = true
	chatSel.follow(d.AbsolutePosition)
	chatSel.extend(d.AbsolutePosition)
}

// edge is where the layer's text starts, or ends.
func (selection *selectLayer) edge(end bool) selPoint {
	if !end || len(selection.pieces) == 0 {
		return selPoint{}
	}
	last := len(selection.pieces) - 1
	return selPoint{last, len(selection.pieces[last].runes)}
}

// mark selects [from, to) of the text, in either order (to < 0: up to its
// end), and shows it.
func (selection *selectLayer) mark(from, to int) {
	if to >= 0 && to < from {
		from, to = to, from
	}
	selection.active, selection.lo, selection.hi = true, from, to
	selection.kept = selection.kept[:0]
	if len(selection.pieces) > 0 {
		n := len(selection.text.runes)
		if selection.hi < 0 || selection.hi > n {
			selection.hi = n
		}
		selection.lo = min(selection.lo, selection.hi)
		selection.kept = append(selection.kept, selection.text.runes[selection.lo:selection.hi]...)
	}
	selection.Refresh()
}

// markPoints selects from one place among the pieces to another.
func (selection *selectLayer) markPoints(from, to selPoint) {
	selection.mark(selection.text.at(from, false), selection.text.at(to, false))
}

func (selection *selectLayer) DragEnd() {
	if tap := selection.grabbed; tap != nil {
		selection.grabbed = nil
		tap.object.(fyne.Draggable).DragEnd()
	}
}

// changed is called when the text below was laid out again (an answer
// still being written): the selection keeps its place in it.
func (selection *selectLayer) changed() {
	selection.stale = true
	if selection.active {
		selection.gather()
		selection.Refresh()
	}
}

func (selection *selectLayer) clearSelection() {
	if selection.active {
		selection.active = false
		selection.Refresh()
	}
}

// selectedText is the selection as text. Each piece says what separates it
// from the next: nothing inside a row, a space where a line wraps, a line
// break or a blank line at the end of a block, a tab between table cells.
func (selection *selectLayer) selectedText() string {
	if !selection.active {
		return ""
	}
	if selection.hi < 0 {
		selection.known()
	}
	if !selection.active || selection.hi < 0 || len(selection.pieces) == 0 {
		return ""
	}
	start, end := selection.span()
	// A selection starting at the end of a piece starts with the next one.
	from, to := selection.text.at(start, true), selection.text.at(end, false)
	if from >= to {
		return ""
	}
	return strings.TrimRight(string(selection.text.runes[from:to]), "\n")
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

// Layout: a new width wraps the text below differently, so the selection
// is found again in it and drawn where it now is.
func (renderer *selectRenderer) Layout(size fyne.Size) {
	renderer.layer.stale = true
	if size != renderer.size && renderer.layer.active {
		renderer.layer.gather()
		renderer.Refresh()
	}
	renderer.size = size
}

func (renderer *selectRenderer) Refresh() {
	selection := renderer.layer
	used := 0
	if selection.active && len(selection.pieces) > 0 {
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
			left, right := piece.at.X+piece.width(a), piece.at.X+piece.width(z)
			if piece.clipped { // only what the code block shows
				left, right = fyne.Max(left, piece.left), fyne.Min(right, piece.right)
			}
			mark.FillColor = ink
			mark.Move(fyne.NewPos(left, piece.at.Y-1))
			mark.Resize(fyne.NewSize(fyne.Max(0, right-left), piece.size.Height+2))
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
