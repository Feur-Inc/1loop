// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
)

// A long chat still has a thumb one can grab, and it runs the whole track
// (#610).
func TestScrollThumbNeverTooShort(t *testing.T) {
	length, pos, ok := scrollThumb(600, 600, 60000, 0)
	if !ok || length != chatBarMin || pos != 0 {
		t.Fatalf("top of a long chat: length %v, pos %v, ok %v", length, pos, ok)
	}
	length, pos, _ = scrollThumb(600, 600, 60000, 60000-600)
	if pos+length != 600 {
		t.Fatalf("end of a long chat: the thumb ends at %v, not the track's end", pos+length)
	}
	if length, _, _ := scrollThumb(600, 600, 1200, 0); length != 300 {
		t.Fatalf("half the chat in view: thumb %v, want half the track", length)
	}
	if _, _, ok := scrollThumb(600, 600, 500, 0); ok {
		t.Fatal("a chat that fits its view has a thumb")
	}
}

// Moving the thumb and reading it back agree.
func TestScrollOffsetForIsTheInverse(t *testing.T) {
	for _, offset := range []float32{0, 1234, 30000, 59400} {
		length, pos, _ := scrollThumb(600, 600, 60000, offset)
		if back := scrollOffsetFor(600, 600, 60000, length, pos); math.Abs(float64(back-offset)) > 1 {
			t.Fatalf("offset %v: thumb at %v reads back %v", offset, pos, back)
		}
	}
}

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
	if bar.Size().Width < chatBarHit {
		t.Fatalf("hit area %v wide, want %v", bar.Size().Width, chatBarHit)
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
