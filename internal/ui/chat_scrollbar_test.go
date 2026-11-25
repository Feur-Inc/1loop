// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/scrollbar"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The chat's scroll draws no bar of its own; the settings page keeps its.
func TestChatScrollThemeHidesOnlyTheChatsBar(t *testing.T) {
	chat := chatScrollTheme{flatScrollTheme{loopTheme{theme.DefaultTheme()}}}
	if chat.Size(theme.SizeNameScrollBar) != 0 || chat.Size(theme.SizeNameScrollBarSmall) != 0 {
		t.Fatal("the chat's scroll still makes room for its own bar")
	}
	flat := flatScrollTheme{loopTheme{theme.DefaultTheme()}}
	if flat.Size(theme.SizeNameScrollBar) == 0 {
		t.Fatal("the settings page lost its scrollbar")
	}
}

// scrolledWindow is a window whose open chat is far longer than its view.
func scrolledWindow(t *testing.T) *desktopUI {
	t.Helper()
	window, _ := deleteTreeWindow(t)
	window.openLocalChat(window.chatByID("root"))
	tall := canvas.NewRectangle(nil)
	tall.SetMinSize(fyne.NewSize(10, 20000))
	window.column.Add(tall)
	window.showTranscript(true)
	window.window.Resize(fyne.NewSize(1200, 800))
	window.scroll.Content.Resize(window.scroll.Content.MinSize())
	window.conversation.Refresh()
	window.chatBar.sync()
	return window
}

// The bar sits over the conversation: above the composer and its fade
// (#609), clear of the side panel's resize handle, which reaches 4 px into
// the chat (#610).
func TestChatScrollBarPlacement(t *testing.T) {
	window := scrolledWindow(t)
	bar := window.chatBar
	if !bar.Visible() {
		t.Fatal("a long chat shows no scrollbar")
	}
	size := window.conversation.Size()
	if right := bar.Position().X + bar.Size().Width; right > size.Width-chatBarRight {
		t.Fatalf("the bar reaches x=%v, under the panel's handle (conversation %v wide)", right, size.Width)
	}
	composerTop := size.Height - window.composer.Size().Height
	if bottom := bar.Position().Y + bar.Size().Height; bottom > composerTop {
		t.Fatalf("the bar ends at y=%v, below the composer's top at %v", bottom, composerTop)
	}
	if bar.Size().Width < scrollbar.Hit {
		t.Fatalf("hit area %v wide, want %v", bar.Size().Width, scrollbar.Hit)
	}
}

// Dragging the thumb moves the conversation and stops following its end;
// a tap below the thumb pages down.
func TestChatScrollBarDragAndPage(t *testing.T) {
	window := scrolledWindow(t)
	bar := window.chatBar
	window.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	window.scroll.OnScrolled(window.scroll.Offset)
	length, pos, ok := bar.thumb()
	if !ok {
		t.Fatal("no thumb")
	}
	start := pos + length/2
	bar.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(8, start+100)}, Dragged: fyne.NewDelta(0, 100)})
	bar.DragEnd()
	if window.scroll.Offset.Y <= 0 {
		t.Fatal("dragging the thumb down did not scroll")
	}
	if window.followTail {
		t.Fatal("still following the end after dragging away from it")
	}
	before := window.scroll.Offset.Y
	_, pos, _ = bar.thumb()
	bar.Tapped(&fyne.PointEvent{Position: fyne.NewPos(8, pos+length+20)})
	if got := window.scroll.Offset.Y - before; got < window.scroll.Size().Height/2 {
		t.Fatalf("a tap below the thumb scrolled %v, want about a page", got)
	}
}

// The conversation's bar looks as it did before it shared its parts
// (#625): a 6 px thumb in colScrollBar 2 px inside its hit area, 10 px in
// the muted colour over a faint track under the pointer.
func TestChatScrollBarLooksAsBefore(t *testing.T) {
	window := scrolledWindow(t)
	fyne.CurrentApp().Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	bar := window.chatBar
	renderer := test.TempWidgetRenderer(t, bar).(*chatScrollBarRenderer)
	renderer.Refresh()
	width := bar.Size().Width
	if renderer.thumb.Size().Width != 6 || renderer.thumb.Position().X != width-8 || renderer.thumb.FillColor != colScrollBar {
		t.Fatalf("thumb at rest: %v wide at x=%v in %v", renderer.thumb.Size().Width, renderer.thumb.Position().X, renderer.thumb.FillColor)
	}
	if renderer.track.FillColor != color.Transparent {
		t.Fatalf("track at rest: %v", renderer.track.FillColor)
	}
	bar.MouseIn(nil)
	if renderer.thumb.Size().Width != 10 || renderer.thumb.Position().X != width-12 {
		t.Fatalf("thumb under the pointer: %v wide at x=%v", renderer.thumb.Size().Width, renderer.thumb.Position().X)
	}
	if renderer.thumb.FillColor != withAlpha(colMuted, 0.85) || renderer.track.FillColor != overlay(0x0c) {
		t.Fatalf("colours under the pointer: thumb %v, track %v", renderer.thumb.FillColor, renderer.track.FillColor)
	}
	if renderer.thumb.CornerRadius != 5 {
		t.Fatalf("radius %v", renderer.thumb.CornerRadius)
	}
	bar.MouseOut()
}

// scrollBarIn finds the bar area of the scroll inside object, a widget or
// container.
func scrollBarIn(t *testing.T, object fyne.CanvasObject) (area interface {
	fyne.Widget
	fyne.Tappable
	fyne.Draggable
	desktop.Hoverable
}, scroll *container.Scroll) {
	t.Helper()
	var walk func(fyne.CanvasObject) bool
	walk = func(object fyne.CanvasObject) bool {
		var children []fyne.CanvasObject
		switch o := object.(type) {
		case *container.Scroll:
			scroll = o
			children = test.TempWidgetRenderer(t, o).Objects()
		case *fyne.Container:
			children = o.Objects
		case fyne.Widget:
			children = test.TempWidgetRenderer(t, o).Objects()
		}
		for _, child := range children {
			if a, ok := child.(interface {
				fyne.Widget
				fyne.Tappable
				fyne.Draggable
				desktop.Hoverable
			}); ok && scroll != nil && child.Visible() {
				area = a
				return true
			}
			if walk(child) {
				return true
			}
		}
		return false
	}
	if !walk(object) {
		t.Fatal("no visible scrollbar")
	}
	return area, scroll
}

// barCheck: the bar of the scroll in object takes the pointer over 16 px,
// draws a thumb of at least 40 px and pages on a tap below it.
func barCheck(t *testing.T, object fyne.CanvasObject) {
	t.Helper()
	area, scroll := scrollBarIn(t, object)
	if area.Size().Width != scrollbar.Hit {
		t.Fatalf("hit area %v wide, want %v", area.Size().Width, scrollbar.Hit)
	}
	objects := test.TempWidgetRenderer(t, area).Objects()
	thumb := objects[1].(*canvas.Rectangle)
	if thumb.Size().Height < scrollbar.MinThumb || thumb.Size().Width != scrollbar.Thin {
		t.Fatalf("thumb %v", thumb.Size())
	}
	if thumb.FillColor != colScrollBar {
		t.Fatalf("thumb colour %v, want colScrollBar", thumb.FillColor)
	}
	area.MouseIn(nil)
	if thumb.Size().Width != scrollbar.Wide || thumb.FillColor != withAlpha(colMuted, 0.85) {
		t.Fatalf("thumb under the pointer: %v wide in %v", thumb.Size().Width, thumb.FillColor)
	}
	area.MouseOut()
	before := scroll.Offset.Y
	area.Tapped(&fyne.PointEvent{Position: fyne.NewPos(8, thumb.Position().Y+thumb.Size().Height+20)})
	if got := scroll.Offset.Y - before; got != scrollbar.Page(scroll.Size().Height) {
		t.Fatalf("a tap below the thumb scrolled %v, want a page of %v", got, scrollbar.Page(scroll.Size().Height))
	}
}

// Every scroll gets the bar (#633): a plain container.Scroll and a List.
func TestEveryScrollGetsTheBar(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	t.Run("scroll", func(t *testing.T) {
		tall := canvas.NewRectangle(nil)
		tall.SetMinSize(fyne.NewSize(10, 20000))
		scroll := container.NewVScroll(tall)
		window := test.NewTempWindow(t, scroll)
		window.Resize(fyne.NewSize(300, 400))
		barCheck(t, scroll)
	})
	t.Run("list", func(t *testing.T) {
		list := widget.NewList(func() int { return 1000 }, func() fyne.CanvasObject { return widget.NewLabel("row") },
			func(widget.ListItemID, fyne.CanvasObject) {})
		window := test.NewTempWindow(t, list)
		window.Resize(fyne.NewSize(300, 400))
		barCheck(t, list)
	})
}

// A strip of controls that scrolls sideways draws no bar, so its controls
// keep the pointer; it still scrolls (#633).
func TestStripScrollHasNoBar(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	wide := canvas.NewRectangle(nil)
	wide.SetMinSize(fyne.NewSize(2000, 30))
	scroll, object := stripScroll(wide)
	window := test.NewTempWindow(t, object)
	window.Resize(fyne.NewSize(300, 30))
	for _, child := range test.TempWidgetRenderer(t, scroll).Objects() {
		if _, ok := child.(fyne.Draggable); ok && child.Visible() {
			t.Fatalf("a strip shows a bar %v", child.Size())
		}
	}
	scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(-40, 0)})
	if scroll.Offset.X <= 0 {
		t.Fatal("the wheel no longer scrolls the strip")
	}
}
