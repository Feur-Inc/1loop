// SPDX-License-Identifier: MPL-2.0

package ui

// Drag and drop for split views and windows (#577). A chat row dragged out
// of the sidebar, or a pane dragged by its header, shows over the pane under
// the pointer where it would go (split_drop.go): a tinted, rounded
// rectangle over the part it would take, with what a drop does written in
// it. Letting go splits that pane, opens the chat in it, or moves the pane
// there; let go off the window, the chat opens in a new window. Escape
// cancels.
//
// A chat is shown in at most one pane (viewOf). A chat already in a pane of
// the window is that pane: dragging its row moves the pane, as dragging its
// header does, and on another pane's middle the two swap. A chat shown in
// another window leaves it: its pane closes there when that window is
// split, else the window goes to a new chat, as with Open in split view.

import (
	"image/color"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// paneDrag is a chat or a pane being carried towards the panes.
type paneDrag struct {
	window    *desktopUI    // the window it happens in (its first pane)
	chat      *conversation // a sidebar row's chat, nil for a pane
	pane      *desktopUI    // the pane carried by its header, nil for a row
	title     string        // what the label beside the pointer says
	target    *desktopUI    // the pane a drop lands in, nil for none
	zone      dropZone      // and where in it
	outside   bool          // the pointer is off the window: a new window
	cancelled bool          // Escape: the rest of the gesture does nothing
}

const (
	carriedAlpha = 0.45 // the opacity of a sidebar row carried out of the sidebar
	dropChipTop  = 18   // the space above what a drop does, in its preview
)

// carryTo moves a drag to the point at of ui's window (canvas coordinates):
// chat, a sidebar row's, or pane, by its header. The first call starts it.
func (ui *desktopUI) carryTo(chat *conversation, pane *desktopUI, title string, at fyne.Position) {
	if ui.desktopApp == nil || ui.windowOf().drops == nil {
		return
	}
	drag := ui.carry
	if drag == nil || drag.chat != chat || drag.pane != pane {
		ui.endCarry() // a drag whose end never came
		drag = &paneDrag{window: ui.windowOf(), chat: chat, pane: pane, title: title}
		ui.carry = drag
		ui.tips.hide()
	}
	if drag.cancelled {
		return
	}
	drag.window.showDrop(drag, at)
}

// dropCarried does what the drag shows, and ends it.
func (ui *desktopUI) dropCarried() {
	if ui.desktopApp == nil {
		return
	}
	drag := ui.carry
	ui.carry = nil
	if drag == nil {
		return
	}
	drag.window.drops.hide()
	if !drag.cancelled {
		drag.window.land(drag)
	}
}

// endCarry stops a drag without a drop: its row went back to the sidebar.
func (ui *desktopUI) endCarry() {
	if ui.desktopApp == nil || ui.carry == nil {
		return
	}
	drag := ui.carry
	ui.carry = nil
	drag.window.drops.hide()
}

// cancelDrag cancels the drag under way, a split's or a sidebar row's, and
// reports whether there was one. The row goes back to its place; the rest
// of the gesture does nothing.
func (ui *desktopUI) cancelDrag() bool {
	cancelled := false
	if drag := ui.carry; drag != nil && !drag.cancelled {
		drag.cancelled = true
		drag.window.drops.hide()
		cancelled = true
	}
	for _, pane := range ui.allPanes() {
		if drag := pane.sideDrag; drag != nil && !drag.cancelled {
			drag.cancelled = true
			pane.sideMarker.Hide()
			fadeRows(drag.object, 1)
			relayout(pane.projectList) // the row glides no more: back to its place
			cancelled = true
		}
	}
	return cancelled
}

// dragging reports whether a chat, a pane or a sidebar row of ui's window is
// being dragged: the pointer passing over a pane does not give it the focus
// then, which would draw the sidebar again from that pane under the drag.
func (ui *desktopUI) dragging() bool {
	if ui == nil || ui.desktopApp == nil {
		return false
	}
	if ui.carry != nil {
		return true
	}
	for _, pane := range ui.panesOf() {
		if pane.sideDrag != nil {
			return true
		}
	}
	return false
}

// catchDragEscape makes Escape cancel a drag, whatever has the focus.
func (ui *desktopUI) catchDragEscape() {
	desktop.OnKeyDown = func(_ fyne.Canvas, key *fyne.KeyEvent) bool {
		return key.Name == fyne.KeyEscape && ui.cancelDrag()
	}
}

// carriedPane is the pane of the window a drag moves: the pane dragged by
// its header, or the one showing the chat dragged; nil when the chat is not
// in this window.
func (window *desktopUI) carriedPane(drag *paneDrag) *desktopUI {
	panes := window.panesOf()
	if drag.pane != nil {
		if slices.Contains(panes, drag.pane) {
			return drag.pane
		}
		return nil
	}
	for _, pane := range panes {
		if drag.chat != nil && pane.current == drag.chat {
			return pane
		}
	}
	return nil
}

// carriedChat is the chat a drag takes to a new window: the row's, or the
// pane's (nil for a new chat).
func (drag *paneDrag) carriedChat() *conversation {
	if drag.pane != nil {
		return drag.pane.current
	}
	return drag.chat
}

// paneRect is a pane's place in the window, in canvas coordinates.
func paneRect(pane *desktopUI) rect {
	at, size := fyne.CurrentApp().Driver().AbsolutePositionForObject(pane.paneBox), pane.paneBox.Size()
	return rect{at, fyne.NewPos(size.Width, size.Height)}
}

func (r rect) fyneSize() fyne.Size { return fyne.NewSize(r.size.X, r.size.Y) }

// paneUnder is the pane of the window under the point at, nil for none (the
// sidebar, the side panel, the settings page).
func (window *desktopUI) paneUnder(at fyne.Position) *desktopUI {
	if window.prefs != nil && window.prefs.open {
		return nil
	}
	for _, pane := range window.panesOf() {
		if pane.paneBox != nil && pane.paneBox.Visible() {
			if r := paneRect(pane); at.X >= r.pos.X && at.Y >= r.pos.Y && at.X < r.pos.X+r.size.X && at.Y < r.pos.Y+r.size.Y {
				return pane
			}
		}
	}
	return nil
}

// planDrop works out where a drop at the point at lands (drag.target,
// drag.zone, drag.outside), and returns the area to preview, in canvas
// coordinates, with what the drop does; "" for nothing.
func (window *desktopUI) planDrop(drag *paneDrag, at fyne.Position) (rect, string) {
	drag.target, drag.zone, drag.outside = nil, dropNone, false
	moved := window.carriedPane(drag)
	if drag.pane != nil && moved == nil || drag.pane == nil && !slices.Contains(window.chats, drag.chat) {
		return rect{}, "" // the pane closed, or the chat was deleted, meanwhile
	}
	if size := window.window.Canvas().Size(); outsideCanvas(size, at) {
		if !window.canOpenWindow(drag.carriedChat()) || drag.pane != nil && !window.isSplit() {
			return rect{}, ""
		}
		drag.outside = true
		return insetRect(rect{size: fyne.NewPos(size.Width, size.Height)}, dropInset), "Open in new window"
	}
	target := window.paneUnder(at)
	if target == nil || target == moved {
		return rect{}, ""
	}
	box := paneRect(target)
	room := box // the room it will have: a pane moved off its side leaves it its place
	if leaf := window.tree.find(moved); leaf != nil && leaf.parent != nil && leaf.sibling().pane == target {
		room = unionRect(box, paneRect(moved))
	}
	zone := dropTarget(box.fyneSize(), room.fyneSize(), at.Subtract(box.pos))
	if zone == dropNone || moved != nil && window.moveKeeps(moved, target, zone) {
		return rect{}, ""
	}
	drag.target, drag.zone = target, zone
	if zone == dropHere {
		room = box
	}
	area := dropRect(room.fyneSize(), zone)
	area.pos = area.pos.Add(room.pos)
	return area, dropLabel(zone, moved != nil)
}

// showDrop draws a drag at the point at: its preview, and its label beside
// the pointer.
func (window *desktopUI) showDrop(drag *paneDrag, at fyne.Position) {
	view := window.drops
	view.canvas = window.window.Canvas()
	view.reveal(view.layer) // first: a hidden layer has no position
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(view.layer)
	area, text := window.planDrop(drag, at)
	if text == "" {
		view.hidePreview()
	} else {
		area.pos = area.pos.Subtract(origin)
		view.show(area, text)
	}
	if moved := window.carriedPane(drag); moved != nil && window.isSplit() {
		lifted := paneRect(moved)
		lifted.pos = lifted.pos.Subtract(origin)
		view.lift(lifted)
	} else {
		view.lift(rect{})
	}
	if drag.outside {
		view.unfollow()
	} else {
		view.follow(at.Subtract(origin), drag.title)
	}
	view.flush()
}

// land does what a drag dropped shows.
func (window *desktopUI) land(drag *paneDrag) {
	moved := window.carriedPane(drag)
	chat := drag.carriedChat()
	switch {
	case drag.outside:
		window.tearOff(moved, chat)
	case drag.target == nil || drag.zone == dropNone || !slices.Contains(window.panesOf(), drag.target):
	case moved != nil:
		if moved != drag.target && !window.moveKeeps(moved, drag.target, drag.zone) {
			window.movePane(moved, drag.target, drag.zone)
		}
	case drag.pane != nil || chat == nil || !slices.Contains(window.chats, chat):
	case drag.zone == dropHere:
		window.releaseChat(chat)
		target := drag.target
		target.openLocalChat(chat)
		target.activate()
		target.window.Canvas().Focus(target.prompt)
		target.save()
	default:
		window.releaseChat(chat)
		vertical, before := splitSide(drag.zone)
		window.addPane(drag.target, chat, vertical, before)
	}
}

// releaseChat takes chat off the screen, from another window than ui's:
// its pane closes when that window is split, else the window goes to a new
// chat.
func (ui *desktopUI) releaseChat(chat *conversation) {
	view := ui.viewOf(chat)
	if view.current != chat {
		return
	}
	if view.isSplit() {
		view.closePane()
		return
	}
	view.startNew()
}

// tearOff opens a drag's chat (nil: a new chat) in a new window: moved, its
// pane in this window, closes. A chat alone in a window of its own already
// only comes forward.
func (window *desktopUI) tearOff(moved *desktopUI, chat *conversation) {
	source := moved
	if chat != nil && source == nil {
		if view := window.viewOf(chat); view.current == chat {
			if !view.isSplit() && view.windowOf() != view.mainView() {
				view.raise()
				return
			}
			source = view
		}
	}
	opener := window.focused()
	if source != nil {
		opener = source
		if source.isSplit() {
			source.closePane() // the chat is on screen nowhere then
		}
	}
	opener.openWindow(chat) // else the chat leaves source, which goes to a new chat
}

// moveKeeps reports whether moving pane to zone of target leaves the
// layout as it is: target is the pane beside it, on the side it is on.
func (window *desktopUI) moveKeeps(pane, target *desktopUI, zone dropZone) bool {
	from := window.tree.find(pane)
	if from == nil || from.parent == nil || zone == dropHere || zone == dropNone || from.sibling().pane != target {
		return false
	}
	vertical, before := splitSide(zone)
	return from.parent.vertical == vertical && (from.parent.first == from) == before
}

// dropView draws a drag over a window: the preview of where it lands, what
// a drop does written in its middle, the pane it lifts dimmed, and beside
// the pointer what is carried. It is a layer over the window's content.
type dropView struct {
	layer      *fyne.Container
	veil       *canvas.Rectangle // over the pane carried
	box        *canvas.Rectangle
	chip       *canvas.Rectangle
	label      *canvas.Text
	ghost      *canvas.Rectangle
	ghostLabel *canvas.Text
	shown      rect // the preview as drawn now
	from, to   rect
	alpha      float32 // its opacity now
	fade       [2]float32
	anim       *fyne.Animation
	canvas     fyne.Canvas // the window's
	dirty      bool        // something was shown or hidden since the last flush
}

func newDropView() *dropView {
	view := &dropView{veil: canvas.NewRectangle(color.Transparent), box: canvas.NewRectangle(color.Transparent),
		chip: canvas.NewRectangle(color.Transparent), label: textLine("", 13, colInk),
		ghost: canvas.NewRectangle(color.Transparent), ghostLabel: textLine("", 13, colText)}
	view.label.TextStyle.Bold = true
	view.box.CornerRadius = rounded(10)
	view.box.StrokeWidth = 1.5
	view.chip.CornerRadius = rounded(8)
	view.veil.CornerRadius = rounded(6)
	view.ghost.CornerRadius = rounded(8)
	view.ghost.StrokeWidth = 1
	view.layer = container.NewWithoutLayout(view.veil, view.box, view.chip, view.label, view.ghost, view.ghostLabel)
	for _, object := range view.layer.Objects {
		object.Hide()
	}
	view.layer.Hide()
	return view
}

// show eases the preview to r with text in it: in from a little smaller
// where it was hidden, else sliding from where it is.
func (view *dropView) show(r rect, text string) {
	if view.label.Text != text {
		view.label.Text = text
		view.label.Refresh()
	}
	if view.fade[1] == 1 && view.to == r {
		view.draw() // a change of text, or nothing
		return
	}
	from := view.shown
	if view.alpha < 0.05 {
		from = scaleRect(r, 0.94)
	}
	view.animate(from, r, view.alpha, 1, 160*time.Millisecond)
}

// hidePreview fades the preview out where it is.
func (view *dropView) hidePreview() {
	if view.fade[1] == 0 {
		return
	}
	view.animate(view.shown, scaleRect(view.shown, 0.98), view.alpha, 0, 120*time.Millisecond)
}

// hide ends a drag's drawing: the preview fades out, the rest goes.
func (view *dropView) hide() {
	view.hidePreview()
	view.lift(rect{})
	view.unfollow()
	view.flush()
}

// reveal and conceal show and hide one of the layer's objects; flush then
// has the window drawn again: Fyne redraws for a refresh, not for a change
// of visibility alone (Container.Show).
func (view *dropView) reveal(object fyne.CanvasObject) {
	if !object.Visible() {
		object.Show()
		view.dirty = true
	}
}

func (view *dropView) conceal(object fyne.CanvasObject) {
	if object.Visible() {
		object.Hide()
		view.dirty = true
	}
}

func (view *dropView) flush() {
	if view.dirty && view.canvas != nil {
		view.dirty = false
		view.canvas.Refresh(view.layer)
	}
}

func (view *dropView) animate(from, to rect, alpha, target float32, length time.Duration) {
	stopMotion(&view.anim)
	view.from, view.to, view.fade = from, to, [2]float32{alpha, target}
	view.anim = motion(length, easeOutCubic, view.step)
}

func (view *dropView) step(p float32) {
	view.shown = lerpRect(view.from, view.to, p)
	view.alpha = view.fade[0] + (view.fade[1]-view.fade[0])*p
	view.draw()
}

// draw puts the preview where it is now, at its opacity now.
func (view *dropView) draw() {
	view.dirty = true // each step: the objects may be new to the canvas yet
	defer view.flush()
	if view.alpha <= 0.01 {
		view.conceal(view.box)
		view.conceal(view.chip)
		view.conceal(view.label)
		view.settle()
		return
	}
	r, alpha := view.shown, view.alpha
	view.box.FillColor = withAlpha(colAmber, 0.11*alpha)
	view.box.StrokeColor = withAlpha(colAmber, 0.75*alpha)
	view.box.Move(r.pos)
	view.box.Resize(r.fyneSize())
	view.reveal(view.box)
	view.box.Refresh()
	text := view.label.MinSize()
	chip := fyne.NewSize(text.Width+24, text.Height+10)
	if chip.Width > r.size.X-12 || chip.Height > r.size.Y-2*dropChipTop {
		view.conceal(view.chip)
		view.conceal(view.label)
		return
	}
	// At the top of the area, clear of the pointer's label in its middle.
	at := fyne.NewPos(r.pos.X+(r.size.X-chip.Width)/2, r.pos.Y+dropChipTop)
	view.chip.FillColor = withAlpha(colAmber, alpha)
	view.chip.Move(at)
	view.chip.Resize(chip)
	view.reveal(view.chip)
	view.chip.Refresh()
	view.label.Color = withAlpha(colInk, quantize(alpha, 4)) // Fyne rasterises a glyph per colour
	view.label.Move(at.AddXY(12, 5))
	view.label.Resize(text)
	view.reveal(view.label)
	view.label.Refresh()
}

// lift dims the pane carried at r; an empty r takes the dimming off.
func (view *dropView) lift(r rect) {
	if r.size.X <= 0 || r.size.Y <= 0 {
		if view.veil.Visible() {
			view.conceal(view.veil)
			view.settle()
		}
		return
	}
	view.veil.FillColor = withAlpha(colBackground, 0.55)
	view.veil.Move(r.pos)
	view.veil.Resize(r.fyneSize())
	view.reveal(view.veil)
	view.veil.Refresh()
}

// follow puts what is carried, title, beside the pointer at, within the
// window.
func (view *dropView) follow(at fyne.Position, title string) {
	if title == "" {
		title = "New chat"
	}
	if text := fitText(title, 13, fyne.TextStyle{}, 220); view.ghostLabel.Text != text {
		view.ghostLabel.Text = text
		view.ghostLabel.Refresh()
	}
	text := view.ghostLabel.MinSize()
	size := fyne.NewSize(text.Width+20, text.Height+10)
	limit := view.layer.Size()
	x := min(at.X+14, limit.Width-size.Width-4)
	y := min(at.Y+18, limit.Height-size.Height-4)
	view.ghost.FillColor = withAlpha(colRaised, 0.96)
	view.ghost.StrokeColor = colPopBorder
	view.ghost.Move(fyne.NewPos(x, y))
	view.ghost.Resize(size)
	view.ghostLabel.Color = colText
	view.ghostLabel.Move(fyne.NewPos(x+10, y+5))
	view.ghostLabel.Resize(text)
	view.reveal(view.ghost)
	view.reveal(view.ghostLabel)
	view.ghost.Refresh()
	view.ghostLabel.Refresh()
}

// unfollow takes the label beside the pointer off.
func (view *dropView) unfollow() {
	if view.ghost.Visible() {
		view.conceal(view.ghost)
		view.conceal(view.ghostLabel)
		view.settle()
	}
}

// settle hides the layer once nothing in it is shown.
func (view *dropView) settle() {
	for _, object := range view.layer.Objects {
		if object.Visible() {
			return
		}
	}
	view.conceal(view.layer)
}

// paneGrip is a pane header's ground: a click gives the pane the focus, a
// drag carries the pane to another place in the window, or out of it.
type paneGrip struct {
	widget.BaseWidget
	pane *desktopUI
}

func newPaneGrip(pane *desktopUI) *paneGrip {
	grip := &paneGrip{pane: pane}
	grip.ExtendBaseWidget(grip)
	return grip
}

func (grip *paneGrip) Tapped(*fyne.PointEvent) { grip.pane.activate() }

func (grip *paneGrip) Dragged(e *fyne.DragEvent) {
	title := "New chat"
	if chat := grip.pane.current; chat != nil {
		title = chat.Title
	}
	grip.pane.carryTo(nil, grip.pane, title, e.AbsolutePosition)
}

func (grip *paneGrip) DragEnd() { grip.pane.dropCarried() }

func (grip *paneGrip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
