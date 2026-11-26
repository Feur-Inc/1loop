// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
)

// Scrolled up, the arrow shows above the composer, in the middle; a click
// brings the conversation to its end, follows it again and hides the arrow
// (#605).
func TestJumpToEnd(t *testing.T) {
	window := scrolledWindow(t)
	jump := window.jumpEnd
	window.scroll.ScrollToBottom()
	window.scroll.OnScrolled(window.scroll.Offset)
	if jump.Visible() {
		t.Fatal("the arrow shows at the conversation's end")
	}
	window.scroll.ScrollToOffset(fyne.NewPos(0, 1000))
	window.scroll.OnScrolled(window.scroll.Offset)
	if !jump.Visible() || window.followTail {
		t.Fatalf("scrolled up: arrow shown %v, following %v", jump.Visible(), window.followTail)
	}
	size := window.conversation.Size()
	composerTop := size.Height - window.composer.Size().Height
	if bottom := jump.Position().Y + jump.Size().Height; bottom > composerTop || bottom < composerTop-80 {
		t.Errorf("the arrow ends at y=%v, the composer starts at %v", bottom, composerTop)
	}
	if mid := jump.Position().X + jump.Size().Width/2; mid < size.Width/2-1 || mid > size.Width/2+1 {
		t.Errorf("the arrow's middle at x=%v, the chat's at %v", mid, size.Width/2)
	}
	window.jumpToEnd()
	bottom := window.scroll.Content.Size().Height - window.scroll.Size().Height
	if !window.followTail || bottom-window.scroll.Offset.Y > 2 || jump.Visible() {
		t.Fatalf("after the click: following %v, %v from the end, arrow shown %v", window.followTail, bottom-window.scroll.Offset.Y, jump.Visible())
	}
	// Following again: what the conversation gains keeps the end in view.
	window.relayoutColumn()
	if bottom := window.scroll.Content.Size().Height - window.scroll.Size().Height; bottom-window.scroll.Offset.Y > 2 {
		t.Fatal("the end left the view after a layout")
	}
}
