package scrollbar

import (
	"math"
	"testing"
)

// A long content still has a thumb one can grab, and it runs the whole
// track.
func TestThumbNeverTooShort(t *testing.T) {
	length, pos, ok := Thumb(600, 600, 60000, 0)
	if !ok || length != MinThumb || pos != 0 {
		t.Fatalf("top of a long content: length %v, pos %v, ok %v", length, pos, ok)
	}
	length, pos, _ = Thumb(600, 600, 60000, 60000-600)
	if pos+length != 600 {
		t.Fatalf("end of a long content: the thumb ends at %v, not the track's end", pos+length)
	}
	if length, _, _ := Thumb(600, 600, 1200, 0); length != 300 {
		t.Fatalf("half the content in view: thumb %v, want half the track", length)
	}
	if length, _, _ := Thumb(30, 30, 3000, 0); length != 30 {
		t.Fatalf("a track shorter than the shortest thumb: thumb %v, want the track", length)
	}
	if _, _, ok := Thumb(600, 600, 500, 0); ok {
		t.Fatal("a content that fits its view has a thumb")
	}
}

// Moving the thumb and reading it back agree.
func TestOffsetForIsTheInverse(t *testing.T) {
	for _, offset := range []float32{0, 1234, 30000, 59400} {
		length, pos, _ := Thumb(600, 600, 60000, offset)
		if back := OffsetFor(600, 600, 60000, length, pos); math.Abs(float64(back-offset)) > 1 {
			t.Fatalf("offset %v: thumb at %v reads back %v", offset, pos, back)
		}
	}
	if got := OffsetFor(600, 600, 60000, 40, -50); got != 0 {
		t.Fatalf("a thumb before the track's start: offset %v", got)
	}
}

// A page keeps PageKeep of the view, or half of a short one.
func TestPage(t *testing.T) {
	if got := Page(600); got != 560 {
		t.Fatalf("page of 600: %v", got)
	}
	if got := Page(60); got != 30 {
		t.Fatalf("page of 60: %v", got)
	}
}

// A press on the thumb holds it where it was pressed; beside it, by its
// middle.
func TestGrab(t *testing.T) {
	if got := Grab(110, 40, 100); got != 10 {
		t.Fatalf("press 10 into the thumb: grab %v", got)
	}
	if got := Grab(300, 40, 100); got != 20 {
		t.Fatalf("press on the track: grab %v, want the middle", got)
	}
}
