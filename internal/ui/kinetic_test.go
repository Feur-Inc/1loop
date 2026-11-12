// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// A conversation 2000 points tall holding a code block, 400 wide in a
// 300-wide column when wide is set.
func scrollFixture(t *testing.T, wide bool) (*kineticScroll, *sideScroll) {
	test.NewTempApp(t)
	codeWidth := float32(200)
	if wide {
		codeWidth = 400
	}
	code := canvas.NewRectangle(nil)
	code.SetMinSize(fyne.NewSize(codeWidth, 60))
	side := newSideScroll(code)
	tall := canvas.NewRectangle(nil)
	tall.SetMinSize(fyne.NewSize(300, 2000))
	outer := newKineticVScroll(container.NewVBox(side, tall))
	window := test.NewTempWindow(t, outer)
	window.Resize(fyne.NewSize(300, 500))
	outer.Resize(fyne.NewSize(300, 500))
	outer.ScrollToOffset(fyne.NewPos(0, 1000))
	kineticActive = nil
	return outer, side
}

// settle runs four seconds of frames: a wheel's motion is eased over them,
// and a flick's glide slows down over a few seconds.
func settle() {
	now := time.Now()
	for frame := range 240 {
		kineticFrame(now.Add(time.Duration(frame)*16*time.Millisecond), 16*time.Millisecond)
	}
}

func TestSideScrollGivesTheWheelToTheConversation(t *testing.T) {
	for _, wide := range []bool{false, true} {
		outer, side := scrollFixture(t, wide)
		side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 50}}) // up
		settle()
		if outer.Offset.Y != 950 {
			t.Errorf("wide %v: the conversation is at %v after a wheel up over the code, want 950", wide, outer.Offset.Y)
		}
		if side.Offset.X != 0 {
			t.Errorf("wide %v: the wheel moved the code sideways to %v", wide, side.Offset.X)
		}
	}
}

func TestSideScrollKeepsTheSidewaysMotionItCanMake(t *testing.T) {
	outer, side := scrollFixture(t, true)
	side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: -60, DY: -20}}) // a diagonal swipe
	settle()
	if side.Offset.X != 60 || outer.Offset.Y != 1020 {
		t.Fatalf("code at %v, conversation at %v; want 60 and 1020", side.Offset.X, outer.Offset.Y)
	}
	// At its start the code cannot go further left: nothing moves sideways.
	side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: 200}})
	if side.Offset.X != 0 {
		t.Fatalf("code at %v, want 0", side.Offset.X)
	}
	side.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: 10}})
	settle()
	if side.Offset.X != 0 || outer.Offset.Y != 1020 {
		t.Fatalf("code at %v, conversation at %v after a swipe past the start", side.Offset.X, outer.Offset.Y)
	}
}

// Without the display saying when fingers lift (X11, Windows, macOS), two
// notches are eased into place and stop where the wheel did.
func TestWheelEasesAndStopsWhereTheWheelDid(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	for range 2 {
		outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 25}})
	}
	if outer.Offset.Y != 1000 {
		t.Fatalf("the conversation jumped to %v before any frame", outer.Offset.Y)
	}
	now := time.Now()
	kineticFrame(now, 16*time.Millisecond)
	first := outer.Offset.Y
	if first >= 1000 || first <= 950 {
		t.Fatalf("after one frame the conversation is at %v, want part of the way to 950", first)
	}
	kineticFrame(now.Add(16*time.Millisecond), 16*time.Millisecond)
	if step := first - outer.Offset.Y; step <= 0 || step >= 1000-first {
		t.Fatalf("the second frame moved %v after %v: the motion should slow down", step, 1000-first)
	}
	settle()
	if outer.gliding || outer.Offset.Y != 950 {
		t.Fatalf("the conversation settled at %v, want 950", outer.Offset.Y)
	}
}

// At an edge, what the wheel still owed is dropped: turning back moves at once.
func TestWheelAtAnEdgeDropsTheRest(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 5000}}) // far past the top
	settle()
	if outer.Offset.Y != 0 || outer.aheadY != 0 {
		t.Fatalf("at the top with %v left to travel (offset %v)", outer.aheadY, outer.Offset.Y)
	}
	outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -100}})
	settle()
	if outer.Offset.Y != 100 {
		t.Fatalf("turning back down reached %v, want 100", outer.Offset.Y)
	}
}
