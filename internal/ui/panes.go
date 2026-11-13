// SPDX-License-Identifier: MPL-2.0

package ui

// Split views (#285). A window's chat area splits side by side or top and
// bottom, as a tree of two-way splits with a divider to drag. Each pane is
// a desktopUI of its own: its chat, scroll, composer, next-step card and
// side panel. The panes of a window share its sidebar, title bar and
// settings page: their widgets are the window's first pane's (shareWindow),
// and the sidebar shows the pane with the focus, so a chat picked there
// opens in it.
//
// The first pane is the window: it is the one in desktopApp.windows, it
// owns the fyne.Window, the tree and the focus. Another pane points to it
// by owner. A chat is shown in at most one pane of any window, as with
// windows (viewOf looks at every pane).

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

// splitNode is a window's layout: a pane, or two halves.
type splitNode struct {
	pane          *desktopUI // a leaf's
	vertical      bool       // top and bottom; else side by side
	ratio         float32    // the first half's share of the room
	first, second *splitNode
	parent        *splitNode
}

// savedSplit is a window's layout as the state file keeps it. The window's
// first pane is the leaf with Window set; its chat is the window's.
type savedSplit struct {
	Chat     string      `json:"chat,omitempty"`
	Window   bool        `json:"window,omitempty"`
	Focus    bool        `json:"focus,omitempty"` // the pane had the focus
	Vertical bool        `json:"vertical,omitempty"`
	Ratio    float32     `json:"ratio,omitempty"`
	First    *savedSplit `json:"first,omitempty"`
	Second   *savedSplit `json:"second,omitempty"`
}

const (
	paneHeadHeight = 30
	dividerSize    = 6
	minPaneSize    = 320
)

// windowOf is the first pane of ui's window.
func (ui *desktopUI) windowOf() *desktopUI {
	if ui.owner != nil {
		return ui.owner
	}
	return ui
}

// isSplit reports whether ui's window shows more than one pane.
func (ui *desktopUI) isSplit() bool {
	return ui.windowOf().tree != nil
}

// panesOf lists the panes of ui's window, left to right and top to bottom.
func (ui *desktopUI) panesOf() []*desktopUI {
	window := ui.windowOf()
	if window.tree == nil {
		return []*desktopUI{window}
	}
	return window.tree.leaves(nil)
}

func (node *splitNode) leaves(out []*desktopUI) []*desktopUI {
	if node.pane != nil {
		return append(out, node.pane)
	}
	return node.second.leaves(node.first.leaves(out))
}

// focused is the pane of ui's window with the focus: the sidebar shows its
// project and chat, the shortcuts act on it.
func (ui *desktopUI) focused() *desktopUI {
	window := ui.windowOf()
	if window.focus != nil && slices.Contains(window.panesOf(), window.focus) {
		return window.focus
	}
	return window
}

// allPanes is every pane of every window, the main window's first.
func (ui *desktopUI) allPanes() []*desktopUI {
	var out []*desktopUI
	for _, window := range ui.allWindows() {
		out = append(out, window.panesOf()...)
	}
	return out
}

// shareWindow gives pane the widgets of its window: the sidebar, the title
// bar, the settings page, the tooltips.
func (pane *desktopUI) shareWindow(window *desktopUI) {
	pane.owner = window
	pane.window = window.window
	pane.tips = window.tips
	pane.prefs, pane.curtain, pane.sideApp = window.prefs, window.curtain, window.sideApp
	pane.header, pane.headerBar, pane.parentLink, pane.expand = window.header, window.headerBar, window.parentLink, window.expand
	pane.newChat, pane.connections, pane.searchRow = window.newChat, window.connections, window.searchRow
	pane.projectList, pane.sideGlide, pane.sideLists = window.projectList, window.sideGlide, window.sideLists
	pane.pane, pane.sideHandle, pane.root = window.pane, window.sideHandle, window.root
	pane.usageRow, pane.updateRow = window.usageRow, window.updateRow
	pane.sideChats, pane.sideOrder = window.sideChats, window.sideOrder
	pane.panel, pane.panelToggle, pane.split = window.panel, window.panelToggle, window.split
}

// buildPaneBox puts ui's chat area under its pane header, shown while the
// window is split.
func (ui *desktopUI) buildPaneBox() {
	ui.paneTitle = newFitLabel("New chat", 13, colMuted)
	close := newRow("", iconClose, styleWindow, func() { ui.closePane() })
	close.onHover = func(in bool) {
		if in {
			ui.tips.show(close, "Close the pane · "+ui.shortcutLabel("shortcuts.close_window"))
		} else {
			ui.tips.hide()
		}
	}
	ui.paneMark = canvas.NewRectangle(color.Transparent)
	ui.paneMark.SetMinSize(fyne.NewSize(0, 2))
	bg := canvas.NewRectangle(colSidebar)
	row := container.NewBorder(nil, nil, nil, container.NewCenter(close), padded(0, 0, 14, 4, ui.paneTitle))
	head := container.NewStack(bg, newTapArea(ui.activate), container.NewBorder(nil, ui.paneMark, nil, nil, row))
	ui.paneHead = container.New(fixedHeight{paneHeadHeight}, head)
	ui.paneHead.Hide()
	ui.paneBox = container.NewBorder(ui.paneHead, nil, nil, nil, ui.conversation)
}

// fixedHeight lays its objects over one another, as tall as it says.
type fixedHeight struct{ height float32 }

func (l fixedHeight) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
}

func (l fixedHeight) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, l.height) }

// splitPane opens chat (nil: a new chat in the project of the pane with the
// focus) in a new pane beside ui, or under it when vertical. A chat on
// screen elsewhere moves there, so that it is never shown twice.
func (ui *desktopUI) splitPane(chat *conversation, vertical bool) *desktopUI {
	if !ui.canOpenWindow(chat) || ui.paneBox == nil {
		return nil
	}
	window := ui.windowOf()
	if chat != nil && ui.viewOf(chat).current == chat && ui.viewOf(chat) == ui {
		return ui // already there
	}
	vertical, fits := splitFits(ui.paneBox.Size(), vertical)
	if !fits {
		ui.notice("Split view", "This pane has no room for another beside it or under it. Make the window larger, or close a pane first.")
		return nil
	}
	if chat != nil {
		if from := ui.viewOf(chat); from.current == chat {
			from.startNew()
		}
	}
	cfg := ui.cfg
	if ui.server != nil {
		cfg = ui.localCfg
	}
	pane := window.newPane(cfg)
	if window.tree == nil {
		window.tree = &splitNode{pane: window}
	}
	window.tree.find(ui).splitWith(pane, vertical, 0.5)
	window.layoutPanes()
	if chat != nil {
		pane.openLocalChat(chat)
	}
	pane.activate()
	pane.window.Canvas().Focus(pane.prompt)
	ui.save()
	return pane
}

// splitFits is the way a pane of size splits: the one asked for when both
// halves keep minPaneSize, else the other one, else none. A pane not laid
// out yet (zero size) splits as asked.
func splitFits(size fyne.Size, vertical bool) (bool, bool) {
	room := func(vertical bool) bool {
		length := size.Width
		if vertical {
			length = size.Height - paneHeadHeight // the new pane's header
		}
		return length >= 2*minPaneSize+dividerSize
	}
	switch {
	case size.Width == 0 && size.Height == 0, room(vertical):
		return vertical, true
	case room(!vertical):
		return !vertical, true
	}
	return vertical, false
}

// newPane builds a pane of window on the start screen of cfg's project.
func (window *desktopUI) newPane(cfg agent.Config) *desktopUI {
	app := window.desktopApp
	app.windowSerial++
	pane := &desktopUI{desktopApp: app, cfg: cfg, draftSuffix: fmt.Sprintf("@pane%d", app.windowSerial)}
	pane.cfg.ClearSessions()
	pane.cfg.RanWith = nil
	pane.shareWindow(window)
	pane.buildChat()
	pane.buildPaneBox()
	pane.prompt.switchDraft(pane.draftKey())
	pane.updateHeader()
	pane.adoptModels(window)
	pane.syncRun()
	return pane
}

// find is the leaf of pane.
func (node *splitNode) find(pane *desktopUI) *splitNode {
	if node == nil {
		return nil
	}
	if node.pane != nil {
		if node.pane == pane {
			return node
		}
		return nil
	}
	if found := node.first.find(pane); found != nil {
		return found
	}
	return node.second.find(pane)
}

// splitWith turns the leaf into two halves: its pane first, pane second.
func (node *splitNode) splitWith(pane *desktopUI, vertical bool, ratio float32) {
	node.first = &splitNode{pane: node.pane, parent: node}
	node.second = &splitNode{pane: pane, parent: node}
	node.pane, node.vertical, node.ratio = nil, vertical, ratio
}

// layoutPanes shows the window's tree: the panes with their headers and
// the dividers between them, or the one pane alone.
func (window *desktopUI) layoutPanes() {
	if window.chatArea == nil {
		return
	}
	if window.tree != nil && window.tree.pane != nil { // one pane left
		window.tree = nil
	}
	split := window.tree != nil
	for _, pane := range window.panesOf() {
		if split {
			pane.paneHead.Show()
		} else {
			pane.paneHead.Hide()
		}
	}
	window.drawPaneFocus()
	if !split {
		window.chatArea.Objects = []fyne.CanvasObject{window.paneBox}
	} else {
		window.chatArea.Objects = []fyne.CanvasObject{window.tree.object(window)}
	}
	window.chatArea.Refresh()
}

// object is the node's widgets.
func (node *splitNode) object(window *desktopUI) fyne.CanvasObject {
	if node.pane != nil {
		return node.pane.paneBox
	}
	divider := newSplitDivider(node, window)
	divider.box = container.New(&splitBoxLayout{node: node}, node.first.object(window), node.second.object(window), divider)
	return divider.box
}

// splitBoxLayout lays a node's halves out at its ratio, the divider between
// them.
type splitBoxLayout struct{ node *splitNode }

func (l *splitBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	first, second, divider := objects[0], objects[1], objects[2]
	total := size.Width
	if l.node.vertical {
		total = size.Height
	}
	cut := splitCut(l.node.ratio, total)
	if l.node.vertical {
		first.Move(fyne.NewPos(0, 0))
		first.Resize(fyne.NewSize(size.Width, cut))
		divider.Move(fyne.NewPos(0, cut))
		divider.Resize(fyne.NewSize(size.Width, dividerSize))
		second.Move(fyne.NewPos(0, cut+dividerSize))
		second.Resize(fyne.NewSize(size.Width, size.Height-cut-dividerSize))
		return
	}
	first.Move(fyne.NewPos(0, 0))
	first.Resize(fyne.NewSize(cut, size.Height))
	divider.Move(fyne.NewPos(cut, 0))
	divider.Resize(fyne.NewSize(dividerSize, size.Height))
	second.Move(fyne.NewPos(cut+dividerSize, 0))
	second.Resize(fyne.NewSize(size.Width-cut-dividerSize, size.Height))
}

func (l *splitBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, 0) // the window's size rules: halves shrink to fit
}

// splitCut is where the first half ends in total room: at ratio, each half
// keeping minPaneSize while there is room for both.
func splitCut(ratio, total float32) float32 {
	room := total - dividerSize
	if room <= 0 {
		return 0
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	cut := room * ratio
	if room >= 2*minPaneSize {
		cut = max(minPaneSize, min(cut, room-minPaneSize))
	}
	return float32(int(cut))
}

// splitDivider is the bar between two halves: dragging it moves the split.
type splitDivider struct {
	widget.BaseWidget
	node   *splitNode
	window *desktopUI
	box    *fyne.Container // the node's halves and the divider
	line   *canvas.Rectangle
}

func newSplitDivider(node *splitNode, window *desktopUI) *splitDivider {
	divider := &splitDivider{node: node, window: window, line: canvas.NewRectangle(colBorderSoft)}
	divider.ExtendBaseWidget(divider)
	return divider
}

func (divider *splitDivider) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.New(dividerLineLayout{vertical: divider.node.vertical}, divider.line))
}

// dividerLineLayout draws a one pixel line along the divider's middle.
type dividerLineLayout struct{ vertical bool }

func (l dividerLineLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if l.vertical {
		objects[0].Move(fyne.NewPos(0, size.Height/2))
		objects[0].Resize(fyne.NewSize(size.Width, 1))
		return
	}
	objects[0].Move(fyne.NewPos(size.Width/2, 0))
	objects[0].Resize(fyne.NewSize(1, size.Height))
}

func (l dividerLineLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 0) }

func (divider *splitDivider) Cursor() desktop.Cursor {
	if divider.node.vertical {
		return desktop.VResizeCursor
	}
	return desktop.HResizeCursor
}

func (divider *splitDivider) MouseIn(*desktop.MouseEvent) {
	divider.line.FillColor = colAmber
	divider.line.Refresh()
}

func (divider *splitDivider) MouseMoved(*desktop.MouseEvent) {}

func (divider *splitDivider) MouseOut() {
	divider.line.FillColor = colBorderSoft
	divider.line.Refresh()
}

func (divider *splitDivider) Dragged(e *fyne.DragEvent) {
	box := divider.box
	if box == nil {
		return
	}
	size := box.Size()
	total, moved := size.Width, e.Dragged.DX
	if divider.node.vertical {
		total, moved = size.Height, e.Dragged.DY
	}
	room := total - dividerSize
	if room <= 0 {
		return
	}
	cut := splitCut(divider.node.ratio, total) + moved
	divider.node.ratio = min(0.9, max(0.1, cut/room))
	relayout(box)
}

func (divider *splitDivider) DragEnd() { divider.window.save() }

// drawPaneFocus marks the pane with the focus: its title in the text's
// colour over an amber line, the others muted.
func (window *desktopUI) drawPaneFocus() {
	focus := window.focused()
	for _, pane := range window.panesOf() {
		if pane.paneTitle == nil {
			continue
		}
		ink, mark := colMuted, color.Color(color.Transparent)
		if pane == focus && window.tree != nil {
			ink, mark = colText, colAmber
		}
		pane.paneTitle.setColor(ink)
		pane.paneMark.FillColor = mark
		pane.paneMark.Refresh()
	}
}

// focusChanged makes the window's sidebar and title bar follow the pane
// that took the focus.
func (ui *desktopUI) focusChanged() {
	ui.windowOf().drawPaneFocus()
	ui.followPanel() // the window's one side panel shows this pane's chat
	if ui.projectList != nil {
		ui.renderSidebar(ui.sidebarGroups())
		ui.updateHeader()
		ui.usageChanged()
	}
}

// closePane closes the pane with the focus, else the window when it is not
// split (Ctrl+Shift+W).
func (ui *desktopUI) closePaneOrWindow() {
	if pane := ui.focused(); pane.isSplit() {
		pane.closePane()
		return
	}
	ui.windowOf().closeWindow()
}

// closePane closes ui's pane; its sibling takes its room. The window's
// first pane cannot go, since it holds the window: it takes the chat of
// the pane beside it, which goes instead.
func (ui *desktopUI) closePane() {
	window := ui.windowOf()
	if window.tree == nil {
		return
	}
	gone := ui
	if ui == window {
		gone = window.tree.find(window).sibling().leaves(nil)[0]
	}
	moved := gone.current
	gone.releaseWindow() // its chat's side panel goes back to the chat
	gone.current = nil   // a chat is never shown twice
	if ui == window {
		if moved != nil {
			window.openLocalChat(moved)
		} else {
			window.startNew()
		}
	}
	window.tree.find(gone).remove()
	if window.tree.pane != nil {
		window.tree = nil
	}
	if window.focus == gone || window.focus == ui {
		window.focus = nil
	}
	window.layoutPanes()
	next := window.focused()
	next.activate()
	next.focusChanged()
	next.followPanel()
	window.window.Canvas().Focus(next.prompt)
	window.save()
}

// sibling is the other half of the leaf's parent.
func (node *splitNode) sibling() *splitNode {
	if node.parent.first == node {
		return node.parent.second
	}
	return node.parent.first
}

// remove takes the leaf out: its sibling takes the parent's place.
func (node *splitNode) remove() {
	parent := node.parent
	if parent == nil {
		return
	}
	other := node.sibling()
	parent.pane, parent.vertical, parent.ratio = other.pane, other.vertical, other.ratio
	parent.first, parent.second = other.first, other.second
	if parent.first != nil {
		parent.first.parent, parent.second.parent = parent, parent
	}
}

// focusPane moves the focus to the pane beside the focused one, in the
// direction dx, dy: the nearest whose side faces it.
func (ui *desktopUI) focusPane(dx, dy int) {
	from := ui.focused()
	if !from.isSplit() {
		return
	}
	driver := fyne.CurrentApp().Driver()
	rect := func(pane *desktopUI) (fyne.Position, fyne.Size) {
		return driver.AbsolutePositionForObject(pane.paneBox), pane.paneBox.Size()
	}
	at, size := rect(from)
	var best *desktopUI
	bestDistance := float32(0)
	for _, pane := range from.panesOf() {
		if pane == from {
			continue
		}
		p, s := rect(pane)
		if next, distance := paneToward(at, size, p, s, dx, dy); next && (best == nil || distance < bestDistance) {
			best, bestDistance = pane, distance
		}
	}
	platform.Devf("ui", "focus %d,%d from %q: %v", dx, dy, from.paneTitle.text, best != nil)
	if best != nil {
		best.activate()
		best.window.Canvas().Focus(best.prompt)
	}
}

// paneToward reports whether the rectangle p, s lies in the direction dx,
// dy of at, size and overlaps it across, and how far it is.
func paneToward(at fyne.Position, size fyne.Size, p fyne.Position, s fyne.Size, dx, dy int) (bool, float32) {
	overlapX := p.X < at.X+size.Width && p.X+s.Width > at.X
	overlapY := p.Y < at.Y+size.Height && p.Y+s.Height > at.Y
	switch {
	case dx < 0 && overlapY && p.X+s.Width <= at.X+1:
		return true, at.X - (p.X + s.Width)
	case dx > 0 && overlapY && p.X >= at.X+size.Width-1:
		return true, p.X - (at.X + size.Width)
	case dy < 0 && overlapX && p.Y+s.Height <= at.Y+1:
		return true, at.Y - (p.Y + s.Height)
	case dy > 0 && overlapX && p.Y >= at.Y+size.Height-1:
		return true, p.Y - (at.Y + size.Height)
	}
	return false, 0
}

// savedLayout is ui's window's split for the state file, nil when it is
// not split.
func (ui *desktopUI) savedLayout() *savedSplit {
	window := ui.windowOf()
	if window.tree == nil {
		return nil
	}
	return window.tree.saved(window)
}

func (node *splitNode) saved(window *desktopUI) *savedSplit {
	if node.pane != nil {
		out := &savedSplit{Window: node.pane == window, Focus: node.pane == window.focused()}
		if node.pane.current != nil && !out.Window {
			out.Chat = node.pane.current.ID
		}
		return out
	}
	return &savedSplit{Vertical: node.vertical, Ratio: node.ratio, First: node.first.saved(window), Second: node.second.saved(window)}
}

// restoreLayout splits ui's window again as it was: each pane on its chat,
// unless another pane or window shows it already. A saved split without
// the window's own pane is not restored.
func (window *desktopUI) restoreLayout(saved *savedSplit) {
	if saved == nil || saved.First == nil || window.paneBox == nil || !saved.hasWindow() {
		return
	}
	opens := map[*desktopUI]string{}
	var focus *desktopUI
	window.tree = window.restoreNode(saved, nil, opens, &focus)
	window.layoutPanes()
	// Once in the tree: a pane out of it is taken for a closed one (viewOf).
	for _, pane := range window.panesOf() {
		if chat := window.chatByID(opens[pane]); chat != nil && chat.server == nil && window.viewOf(chat).current != chat {
			pane.openLocalChat(chat) // which gives it the focus
		}
	}
	if focus == nil {
		focus = window
	}
	focus.activate()
	window.drawPaneFocus()
	focus.window.Canvas().Focus(focus.prompt)
}

func (saved *savedSplit) hasWindow() bool {
	if saved == nil {
		return false
	}
	return saved.Window || saved.First.hasWindow() || saved.Second.hasWindow()
}

func (window *desktopUI) restoreNode(saved *savedSplit, parent *splitNode, opens map[*desktopUI]string, focus **desktopUI) *splitNode {
	node := &splitNode{parent: parent}
	defer func() {
		if saved.Focus && node.pane != nil {
			*focus = node.pane
		}
	}()
	switch {
	case saved.Window:
		node.pane = window
	case saved.First == nil || saved.Second == nil:
		node.pane = window.newPane(window.cfg)
		opens[node.pane] = saved.Chat
	default:
		node.vertical, node.ratio = saved.Vertical, saved.Ratio
		node.first = window.restoreNode(saved.First, node, opens, focus)
		node.second = window.restoreNode(saved.Second, node, opens, focus)
	}
	return node
}

// paneAt is the pane of ui's window under the point at (in the window),
// else the one with the focus.
func (ui *desktopUI) paneAt(at fyne.Position) *desktopUI {
	if !ui.isSplit() {
		return ui.windowOf()
	}
	driver := fyne.CurrentApp().Driver()
	for _, pane := range ui.panesOf() {
		p, s := driver.AbsolutePositionForObject(pane.paneBox), pane.paneBox.Size()
		if at.X >= p.X && at.X < p.X+s.Width && at.Y >= p.Y && at.Y < p.Y+s.Height {
			return pane
		}
	}
	return ui.focused()
}

// paneArea is what the settings page takes the place of: every pane of the
// window.
func (ui *desktopUI) paneArea() fyne.CanvasObject {
	return ui.windowOf().split
}

// setColor gives the label another colour.
func (label *fitLabel) setColor(c color.Color) {
	if label.color == c {
		return
	}
	label.color = c
	label.Refresh()
}

// panelFollows reports whether the window's side panel shows ui's chat: ui
// is the pane with the focus (or the window is not split).
func (ui *desktopUI) panelFollows() bool {
	return ui.panel != nil && (ui.desktopApp == nil || ui.focused() == ui)
}

// showPanel puts chat's tabs in the window's side panel, when ui is the pane
// the panel follows. A split window's panel stays open, at its width, as
// the focus goes from pane to pane: it is one panel for all of them.
func (ui *desktopUI) showPanel(chat *conversation) {
	if !ui.panelFollows() {
		return
	}
	panel := ui.panel
	shown, target, width := panel.shown, panel.target, panel.width
	panel.ui = ui
	panel.switchTo(chat)
	if ui.isSplit() {
		if target > 0 {
			panel.shown, panel.target = max(shown, 0.999), 1
			panel.width = width
		}
		ui.panelToggle.SetSelected(panel.target > 0)
		relayout(ui.split)
	}
}

// followPanel makes the window's side panel follow the pane that took the
// focus.
func (ui *desktopUI) followPanel() {
	if ui.panel == nil {
		return
	}
	if ui.panel.ui == ui && ui.panel.owner == ui.current {
		ui.panel.renderStrip()
		return
	}
	ui.showPanel(ui.current)
}

// otherPanes are the panes whose tabs the panel's strip lists, in their
// order, the one the panel follows as nil; none while the window is not
// split.
func (panel *toolPanel) otherPanes() []*desktopUI {
	if panel.ui == nil || panel.ui.desktopApp == nil || !panel.ui.isSplit() {
		return nil
	}
	panes := panel.ui.panesOf()
	out := make([]*desktopUI, len(panes))
	for i, pane := range panes {
		if pane != panel.ui {
			out[i] = pane
		}
	}
	return out
}

// paneTabs are the tabs of another pane's chat, as its panel keeps them.
func (panel *toolPanel) paneTabs(pane *desktopUI) []panelTab {
	if pane.current == nil || pane.current == panel.owner {
		return nil
	}
	return panel.stateOf(pane.current).tabs
}

// addStripGroup starts a pane's tabs in the strip with its chat's title.
func (panel *toolPanel) addStripGroup(pane *desktopUI) {
	title, ink := "New chat", colFaint
	if pane.current != nil {
		title = pane.current.Title
	}
	if pane == panel.ui {
		ink = colMuted
	}
	label := textLine(fitText(title, 12, fyne.TextStyle{}, 110), 12, ink)
	panel.strip.Add(container.NewCenter(padded(0, 0, 6, 2, label)))
}

// addPaneTabs lists another pane's tabs in the strip: a click gives that
// pane the focus, and the panel shows the tab.
func (panel *toolPanel) addPaneTabs(pane *desktopUI) {
	tabs := panel.paneTabs(pane)
	if len(tabs) == 0 {
		return
	}
	panel.addStripGroup(pane)
	for _, tab := range tabs {
		tab := tab
		var frame *panelTabFrame
		frame = newPanelTabFrame(fitText(tabLabel(tab), 13, fyne.TextStyle{}, 150), tabIcon(tab), false, func() {
			pane.activate() // the panel now follows it (showPanel)
			if pane.prompt != nil {
				pane.window.Canvas().Focus(pane.prompt)
			}
			for i, shown := range panel.tabs {
				if sameTab(shown, tab) {
					panel.choose(i)
					break
				}
			}
			if panel.target <= 0 {
				panel.unfold()
			}
		})
		// The frame draws the block; its glyph and label go over it, as in tabView.
		panel.strip.Add(container.NewStack(frame, padded(4, 4, 10, 10, container.New(layout.NewCustomPaddedHBoxLayout(6),
			container.NewCenter(frame.icon), container.NewCenter(frame.label)))))
	}
}

// sameTab reports whether two tabs are the same one.
func sameTab(a, b panelTab) bool {
	return a.kind == b.kind && a.chat == b.chat && a.index == b.index && a.content == b.content &&
		a.browser == b.browser && a.terminal == b.terminal && a.ref == b.ref
}

// tabLabel is what a tab's frame says: a call's target, else its title.
func tabLabel(tab panelTab) string {
	if tab.chat != nil && tab.index >= 0 && tab.index < len(tab.chat.Items) {
		item := tab.chat.Items[tab.index]
		_, target, _ := agent.DescribeTool(item, false)
		switch item.Tool {
		case "read_file", "list_directory", "edit_file", "apply_patch":
			target = filepath.Base(target)
		}
		return target
	}
	return tab.title
}

// tabIcon is a tab's glyph: its tool's for a call.
func tabIcon(tab panelTab) fyne.Resource {
	if tab.chat != nil && tab.index >= 0 && tab.index < len(tab.chat.Items) {
		return toolGlyph(tab.chat.Items[tab.index].Tool)
	}
	return tab.icon
}
