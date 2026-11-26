package container_test

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
)

// 1loop's bar (#633): what a scroll's bar areas take and draw.

// barTheme is the test theme with 1loop's bar sizes and hover colour.
type barTheme struct{ fyne.Theme }

func (th barTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar:
		return scrollbar.Wide
	case theme.SizeNameScrollBarSmall:
		return scrollbar.Thin
	}
	return th.Theme.Size(name)
}

func (th barTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == scrollbar.ColorNameHover {
		return color.White
	}
	return th.Theme.Color(name, v)
}

// barArea is what a scroll's bar is to the pointer.
type barArea interface {
	fyne.Widget
	fyne.Tappable
	fyne.Draggable
	desktop.Hoverable
}

func tallScroll(t *testing.T, direction fyne.ScrollDirection, content fyne.Size) (scroll *container.Scroll, vert, horiz barArea) {
	t.Helper()
	test.NewTempApp(t).Settings().SetTheme(barTheme{test.Theme()})
	tall := canvas.NewRectangle(color.Black)
	tall.SetMinSize(content)
	scroll = container.NewScroll(tall)
	scroll.Direction = direction
	scroll.Resize(fyne.NewSize(200, 300))
	r := test.TempWidgetRenderer(t, scroll)
	r.Layout(scroll.Size())
	var areas []barArea
	for _, object := range r.Objects() {
		if area, ok := object.(barArea); ok {
			areas = append(areas, area)
		}
	}
	if len(areas) != 2 {
		t.Fatalf("%d bar areas, want 2", len(areas))
	}
	return scroll, areas[0], areas[1]
}

// parts are the track and the thumb an area draws.
func parts(t *testing.T, area barArea) (track, thumb *canvas.Rectangle) {
	objects := test.TempWidgetRenderer(t, area).Objects()
	return objects[0].(*canvas.Rectangle), objects[1].(*canvas.Rectangle)
}

// The area takes the pointer over scrollbar.Hit at the scroll's edge; the
// thumb is drawn inside it, scrollbar.Edge from that edge, never shorter
// than scrollbar.MinThumb, and widens over a track under the pointer.
func TestScrollBarHitAreaWiderThanDrawn(t *testing.T) {
	scroll, area, _ := tallScroll(t, fyne.ScrollVerticalOnly, fyne.NewSize(100, 100000))
	if !area.Visible() {
		t.Fatal("a long content shows no bar")
	}
	if area.Size().Width != scrollbar.Hit || area.Position().X != scroll.Size().Width-scrollbar.Hit {
		t.Fatalf("hit area %v at %v, want %v wide at the right edge", area.Size(), area.Position(), scrollbar.Hit)
	}
	track, thumb := parts(t, area)
	if thumb.Size().Width != scrollbar.Thin || thumb.Size().Height != scrollbar.MinThumb {
		t.Fatalf("thumb at rest %v, want %v wide and %v long", thumb.Size(), scrollbar.Thin, scrollbar.MinThumb)
	}
	if right := thumb.Position().X + thumb.Size().Width; right != area.Size().Width-scrollbar.Edge {
		t.Fatalf("thumb ends at x=%v in its area, want %v", right, area.Size().Width-scrollbar.Edge)
	}
	if thumb.Position().Y != scrollbar.Edge || !track.Hidden {
		t.Fatalf("thumb at the top starts at y=%v (track hidden: %v)", thumb.Position().Y, track.Hidden)
	}
	area.MouseIn(nil)
	if track, thumb = parts(t, area); thumb.Size().Width != scrollbar.Wide || track.Hidden {
		t.Fatalf("under the pointer: thumb %v wide, track hidden %v", thumb.Size().Width, track.Hidden)
	}
	area.MouseOut()
	if track, _ = parts(t, area); !track.Hidden {
		t.Fatal("the track stays after the pointer left")
	}
}

// Content that fits shows no bar.
func TestScrollBarHiddenWhenItFits(t *testing.T) {
	_, area, _ := tallScroll(t, fyne.ScrollVerticalOnly, fyne.NewSize(100, 100))
	if area.Visible() {
		t.Fatal("a content that fits shows a bar")
	}
}

// Turned 90°: the horizontal bar lies along the bottom edge, and pages
// sideways.
func TestScrollBarHorizontal(t *testing.T) {
	scroll, _, area := tallScroll(t, fyne.ScrollHorizontalOnly, fyne.NewSize(100000, 100))
	if area.Size().Height != scrollbar.Hit || area.Position().Y != scroll.Size().Height-scrollbar.Hit {
		t.Fatalf("hit area %v at %v", area.Size(), area.Position())
	}
	if _, thumb := parts(t, area); thumb.Size().Height != scrollbar.Thin || thumb.Size().Width != scrollbar.MinThumb {
		t.Fatalf("thumb %v", thumb.Size())
	}
	area.Tapped(&fyne.PointEvent{Position: fyne.NewPos(150, 8)})
	if scroll.Offset.X != scrollbar.Page(200) {
		t.Fatalf("a tap right of the thumb scrolled to %v, want a page", scroll.Offset.X)
	}
}

// Where both bars show, neither runs into the other's corner.
func TestScrollBarsBothKeepTheCorner(t *testing.T) {
	scroll, vert, horiz := tallScroll(t, fyne.ScrollBoth, fyne.NewSize(100000, 100000))
	if got := vert.Size().Height; got != scroll.Size().Height-scrollbar.Hit {
		t.Fatalf("vertical area %v high, want the corner left", got)
	}
	if got := horiz.Size().Width; got != scroll.Size().Width-scrollbar.Hit {
		t.Fatalf("horizontal area %v wide, want the corner left", got)
	}
}

// A tap beside the thumb pages, keeping scrollbar.PageKeep of the view; a
// tap on the thumb does nothing.
func TestScrollBarTapPages(t *testing.T) {
	scroll, area, _ := tallScroll(t, fyne.ScrollVerticalOnly, fyne.NewSize(100, 100000))
	scrolled := 0
	scroll.OnScrolled = func(fyne.Position) { scrolled++ }
	area.Tapped(&fyne.PointEvent{Position: fyne.NewPos(8, 20)})
	if scroll.Offset.Y != 0 || scrolled != 0 {
		t.Fatalf("a tap on the thumb scrolled to %v", scroll.Offset.Y)
	}
	area.Tapped(&fyne.PointEvent{Position: fyne.NewPos(8, 200)})
	if scroll.Offset.Y != 260 || scrolled != 1 {
		t.Fatalf("a tap below the thumb scrolled to %v (%d calls), want 260", scroll.Offset.Y, scrolled)
	}
	area.Tapped(&fyne.PointEvent{Position: fyne.NewPos(8, 2)})
	if scroll.Offset.Y != 0 {
		t.Fatalf("a tap above the thumb scrolled to %v, want 0", scroll.Offset.Y)
	}
}

// Dragging the thumb keeps it under the pointer where it was taken; a
// drag from the track takes it by its middle.
func TestScrollBarDrag(t *testing.T) {
	scroll, area, _ := tallScroll(t, fyne.ScrollVerticalOnly, fyne.NewSize(100, 3000))
	track := area.Size().Height - 2*scrollbar.Edge
	area.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(8, 60)}, Dragged: fyne.NewDelta(0, 50)})
	_, thumb := parts(t, area)
	if thumb.Size().Width != scrollbar.Wide {
		t.Fatalf("dragged thumb %v wide", thumb.Size().Width)
	}
	if thumb.Position().Y != scrollbar.Edge+50 {
		t.Fatalf("thumb at %v after a 50 px drag, want %v", thumb.Position().Y, scrollbar.Edge+50)
	}
	area.DragEnd()
	if _, thumb = parts(t, area); thumb.Size().Width != scrollbar.Thin {
		t.Fatalf("thumb %v wide after the drag, the pointer away", thumb.Size().Width)
	}
	length := thumb.Size().Height
	area.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(8, 250)}, Dragged: fyne.NewDelta(0, 0)})
	area.DragEnd()
	want := scrollbar.OffsetFor(track, 300, 3000, length, 250-scrollbar.Edge-length/2)
	if scroll.Offset.Y != want {
		t.Fatalf("drag from the track scrolled to %v, want %v", scroll.Offset.Y, want)
	}
}
