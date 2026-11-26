// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The conversation's scrollbar (#609, #610). Fyne's own bar lives inside
// the scroll, under the composer's fade, and is a 6 px strip whose thumb
// shrinks with the chat; the side panel's resize handle also covers the
// chat's last pixels, where that strip is. This bar is drawn over the
// conversation instead: its track runs from under the side panel's toggle
// down to the composer, its thumb is never shorter than chatBarMin, and
// its hit area is wider than what it draws and ends clear of the handle.

const (
	chatBarHit   = 16 // the width that takes the pointer
	chatBarThin  = 6  // the thumb's width at rest
	chatBarWide  = 10 // its width under the pointer or while dragged
	chatBarMin   = 40 // the shortest thumb
	chatBarRight = 6  // from the conversation's right edge: the panel's handle reaches 4 px in
	chatBarTop   = 52 // the side panel's toggle sits above
	chatBarGap   = 8  // above the composer
	chatBarPage  = 40 // what a page keeps of the view it leaves
)

// chatScrollTheme is flatScrollTheme without the scroll's own bar: no
// colour and no room, so nothing under the conversation's bar takes the
// pointer.
type chatScrollTheme struct{ flatScrollTheme }

func (themeSpec chatScrollTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameScrollBar, theme.ColorNameScrollBarBackground:
		return color.Transparent
	}
	return themeSpec.flatScrollTheme.Color(name, v)
}

func (themeSpec chatScrollTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar, theme.SizeNameScrollBarSmall:
		return 0
	}
	return themeSpec.flatScrollTheme.Size(name)
}

// scrollThumb is the thumb on a track of length track, for a view of
// length view over content of length content scrolled to offset: its
// length and its position from the track's start. ok is false when the
// content fits the view: no thumb then.
func scrollThumb(track, view, content, offset float32) (length, pos float32, ok bool) {
	if content <= view || track <= 0 {
		return 0, 0, false
	}
	length = fyne.Min(track, fyne.Max(chatBarMin, track*view/content))
	offset = fyne.Min(fyne.Max(0, offset), content-view)
	if room := track - length; room > 0 {
		pos = room * offset / (content - view)
	}
	return length, pos, true
}

// scrollOffsetFor is the offset that puts a thumb of length length at pos.
func scrollOffsetFor(track, view, content, length, pos float32) float32 {
	room := track - length
	if content <= view || room <= 0 {
		return 0
	}
	pos = fyne.Min(fyne.Max(0, pos), room)
	return pos / room * (content - view)
}

type chatScrollBar struct {
	widget.BaseWidget
	ui       *desktopUI
	hovered  bool
	dragging bool
	grab     float32 // where in the thumb the drag holds it
}

func newChatScrollBar(ui *desktopUI) *chatScrollBar {
	bar := &chatScrollBar{ui: ui}
	bar.ExtendBaseWidget(bar)
	bar.Hide()
	return bar
}

// metrics are the scroll's view, content and offset along the bar.
func (bar *chatScrollBar) metrics() (view, content, offset float32) {
	scroll := bar.ui.scroll
	return scroll.Size().Height, scroll.Content.Size().Height, scroll.Offset.Y
}

// thumb is the thumb's length and position on the bar as it is now.
func (bar *chatScrollBar) thumb() (length, pos float32, ok bool) {
	view, content, offset := bar.metrics()
	return scrollThumb(bar.Size().Height, view, content, offset)
}

// sync shows the bar when the conversation is longer than its view, and
// draws its thumb where the view is. Called wherever the view or the
// conversation's height changes (syncFooterFade, OnScrolled).
func (bar *chatScrollBar) sync() {
	if bar == nil || bar.ui.scroll == nil {
		return
	}
	view, content, _ := bar.metrics()
	if !bar.ui.scroll.Visible() || content <= view+1 {
		if bar.Visible() {
			bar.hovered, bar.dragging = false, false
			bar.Hide()
		}
		return
	}
	if !bar.Visible() {
		bar.Show()
	}
	bar.Refresh()
}

// scrollTo moves the view so the thumb's top is at pos.
func (bar *chatScrollBar) scrollTo(pos float32) {
	length, _, ok := bar.thumb()
	if !ok {
		return
	}
	view, content, _ := bar.metrics()
	bar.setOffset(scrollOffsetFor(bar.Size().Height, view, content, length, pos))
}

func (bar *chatScrollBar) setOffset(offset float32) {
	scroll := bar.ui.scroll
	scroll.gliding = false // a hand on the bar stops the glide
	view, content, _ := bar.metrics()
	offset = fyne.Min(fyne.Max(0, offset), fyne.Max(0, content-view))
	scroll.ScrollToOffset(fyne.NewPos(scroll.Offset.X, offset))
	if scroll.OnScrolled != nil { // ScrollToOffset does not tell it: followTail and the fade follow the view
		scroll.OnScrolled(scroll.Offset)
	}
	bar.Refresh()
}

func (bar *chatScrollBar) Dragged(e *fyne.DragEvent) {
	length, pos, ok := bar.thumb()
	if !ok {
		return
	}
	if !bar.dragging {
		bar.dragging = true
		y := e.Position.Y - e.Dragged.DY // where the press was
		bar.grab = length / 2            // a press on the track takes the thumb by its middle
		if y >= pos && y <= pos+length {
			bar.grab = y - pos
		}
	}
	bar.scrollTo(e.Position.Y - bar.grab)
}

func (bar *chatScrollBar) DragEnd() {
	bar.dragging = false
	bar.Refresh()
}

// Tapped on the track pages towards the tap, keeping a little of the view.
func (bar *chatScrollBar) Tapped(e *fyne.PointEvent) {
	length, pos, ok := bar.thumb()
	if !ok {
		return
	}
	view, _, offset := bar.metrics()
	page := fyne.Max(view-chatBarPage, view/2)
	switch {
	case e.Position.Y < pos:
		bar.setOffset(offset - page)
	case e.Position.Y > pos+length:
		bar.setOffset(offset + page)
	}
}

// Scrolled: the wheel over the bar scrolls the conversation.
func (bar *chatScrollBar) Scrolled(e *fyne.ScrollEvent) {
	bar.ui.scroll.Scrolled(e)
}

func (bar *chatScrollBar) MouseIn(*desktop.MouseEvent) {
	bar.hovered = true
	bar.Refresh()
}

func (bar *chatScrollBar) MouseMoved(*desktop.MouseEvent) {}

func (bar *chatScrollBar) MouseOut() {
	bar.hovered = false
	bar.Refresh()
}

func (bar *chatScrollBar) CreateRenderer() fyne.WidgetRenderer {
	renderer := &chatScrollBarRenderer{bar: bar, track: canvas.NewRectangle(color.Transparent), thumb: canvas.NewRectangle(colScrollBar)}
	renderer.Refresh()
	return renderer
}

type chatScrollBarRenderer struct {
	bar   *chatScrollBar
	track *canvas.Rectangle // shown faintly under the pointer: where a tap pages
	thumb *canvas.Rectangle
}

func (renderer *chatScrollBarRenderer) Layout(size fyne.Size) {
	bar := renderer.bar
	width := float32(chatBarThin)
	if bar.hovered || bar.dragging {
		width = chatBarWide
	}
	x := size.Width - width - 2
	renderer.track.Move(fyne.NewPos(x, 0))
	renderer.track.Resize(fyne.NewSize(width, size.Height))
	view, content, offset := bar.metricsOrZero()
	length, pos, ok := scrollThumb(size.Height, view, content, offset)
	renderer.thumb.Hidden = !ok
	renderer.thumb.Move(fyne.NewPos(x, pos))
	renderer.thumb.Resize(fyne.NewSize(width, length))
}

// metricsOrZero is metrics for the renderer, which may run before the
// scroll exists.
func (bar *chatScrollBar) metricsOrZero() (view, content, offset float32) {
	if bar.ui == nil || bar.ui.scroll == nil {
		return 0, 0, 0
	}
	return bar.metrics()
}

func (renderer *chatScrollBarRenderer) MinSize() fyne.Size {
	return fyne.NewSize(chatBarHit, chatBarMin)
}

func (renderer *chatScrollBarRenderer) Refresh() {
	bar := renderer.bar
	active := bar.hovered || bar.dragging
	renderer.thumb.FillColor = colScrollBar
	renderer.track.FillColor = color.Transparent
	if active {
		renderer.thumb.FillColor = withAlpha(colMuted, 0.85)
		renderer.track.FillColor = overlay(0x0c)
	}
	radius := float32(chatBarThin) / 2
	if active {
		radius = chatBarWide / 2
	}
	renderer.thumb.CornerRadius, renderer.track.CornerRadius = radius, radius
	renderer.Layout(bar.Size())
	renderer.track.Refresh()
	renderer.thumb.Refresh()
}

func (renderer *chatScrollBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.track, renderer.thumb}
}

func (renderer *chatScrollBarRenderer) Destroy() {}

// chatBarLayout lays the conversation out, and its scrollbar over it on the
// right: under the side panel's toggle, down to the composer.
type chatBarLayout struct {
	ui     *desktopUI
	footer fyne.CanvasObject // the composer and what sits on it (buildFooter)
}

func (layoutSpec chatBarLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (layoutSpec chatBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.Position{})
	objects[0].Resize(size)
	bottom := size.Height - chatBarGap
	if layoutSpec.footer != nil {
		bottom -= layoutSpec.footer.MinSize().Height
	}
	objects[1].Move(fyne.NewPos(size.Width-chatBarRight-chatBarHit, chatBarTop))
	objects[1].Resize(fyne.NewSize(chatBarHit, fyne.Max(0, bottom-chatBarTop)))
	if bar, ok := objects[1].(*chatScrollBar); ok {
		bar.sync()
	}
}
