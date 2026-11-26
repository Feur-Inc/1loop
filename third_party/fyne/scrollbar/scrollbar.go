// Package scrollbar is 1loop's scrollbar: its sizes, its geometry and its
// look, shared by every scroll (internal/widget's scroller, so
// container.Scroll, List, Table, Tree and Entry) and by 1loop's own bar over
// the conversation, so the two never drift apart (1loop #625, #633).
package scrollbar

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

const (
	// Hit is the width that takes the pointer, wider than what is drawn.
	Hit = 16
	// Thin is the thumb's width at rest; a theme gives it as
	// theme.SizeNameScrollBarSmall.
	Thin = 6
	// Wide is its width under the pointer or while dragged; a theme gives
	// it as theme.SizeNameScrollBar. A theme giving 0 has no bar at all.
	Wide = 10
	// MinThumb is the shortest thumb.
	MinThumb = 40
	// PageKeep is what a page keeps of the view it leaves.
	PageKeep = 40
	// Edge is how far a scroll's bar is drawn from the scroll's edge, and
	// from the ends of its track: 1loop's resize handles and the window's
	// resize edges reach 4 px over what lies beside them, and what is drawn
	// must be what takes the pointer.
	Edge = 4
)

// ColorNameHover is the thumb's colour under the pointer or while dragged.
// A theme without it gets theme.ColorNameScrollBar.
const ColorNameHover fyne.ThemeColorName = "scrollBarHover"

// Thumb is the thumb on a track of length track, for a view of length view
// over content of length content scrolled to offset: its length and its
// position from the track's start. ok is false when the content fits the
// view: no thumb then.
func Thumb(track, view, content, offset float32) (length, pos float32, ok bool) {
	if content <= view || track <= 0 {
		return 0, 0, false
	}
	length = fyne.Min(track, fyne.Max(MinThumb, track*view/content))
	offset = fyne.Min(fyne.Max(0, offset), content-view)
	if room := track - length; room > 0 {
		pos = room * offset / (content - view)
	}
	return length, pos, true
}

// OffsetFor is the offset that puts a thumb of length length at pos.
func OffsetFor(track, view, content, length, pos float32) float32 {
	room := track - length
	if content <= view || room <= 0 {
		return 0
	}
	pos = fyne.Min(fyne.Max(0, pos), room)
	return pos / room * (content - view)
}

// Page is how far a tap on the track moves a view of length view.
func Page(view float32) float32 {
	return fyne.Max(view-PageKeep, view/2)
}

// Grab is where in the thumb a drag holds it, for a press at press on the
// track: where it was pressed, or its middle for a press beside it.
func Grab(press, length, pos float32) float32 {
	if press >= pos && press <= pos+length {
		return press - pos
	}
	return length / 2
}

// Look is how a bar is drawn, at rest or active (under the pointer or
// dragged): its width, the thumb's colour and the track's. The track only
// shows while active, faintly: where a tap pages.
func Look(th fyne.Theme, active bool) (width float32, thumb, track color.Color) {
	v := fyne.CurrentApp().Settings().ThemeVariant()
	thumb = th.Color(theme.ColorNameScrollBar, v)
	track = color.Transparent
	width = th.Size(theme.SizeNameScrollBarSmall)
	if active {
		width = th.Size(theme.SizeNameScrollBar)
		if hover := th.Color(ColorNameHover, v); visible(hover) {
			thumb = hover
		}
		track = th.Color(theme.ColorNameScrollBarBackground, v)
	}
	return width, thumb, track
}

func visible(c color.Color) bool {
	if c == nil {
		return false
	}
	_, _, _, a := c.RGBA()
	return a > 0
}
