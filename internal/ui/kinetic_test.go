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

// settle runs a second of frames: nothing a wheel did should move in them.
func settle() {
	now := time.Now()
	for frame := range 60 {
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

// Without the display saying when fingers lift (X11, Windows, macOS), each
// wheel notch moves the content by its distance as it arrives, so the
// content's speed is the spin's, and nothing moves after the last notch.
func TestWheelFollowsTheSpinAndStopsWithIt(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	for _, gap := range []time.Duration{16 * time.Millisecond, 50 * time.Millisecond, 150 * time.Millisecond} {
		outer, _ := scrollFixture(t, false)
		now := time.Now()
		var frames time.Duration
		for notch := 1; notch <= 4; notch++ {
			outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 25}})
			// The frames until the next notch, 16 ms apart.
			for ; frames < time.Duration(notch)*gap; frames += 16 * time.Millisecond {
				kineticFrame(now.Add(frames), 16*time.Millisecond)
				if want := float32(1000 - 25*notch); outer.Offset.Y != want {
					t.Fatalf("notches %v apart: after notch %d the conversation is at %v, want %v", gap, notch, outer.Offset.Y, want)
				}
			}
		}
		settle()
		if outer.gliding || outer.Offset.Y != 900 {
			t.Fatalf("notches %v apart: the conversation moved on to %v after the wheel stopped at 900", gap, outer.Offset.Y)
		}
	}
}

// Past an edge the wheel stops there: turning back moves at once.
func TestWheelStopsAtAnEdge(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 5000}}) // far past the top
	settle()
	if outer.Offset.Y != 0 {
		t.Fatalf("at %v past the top, want 0", outer.Offset.Y)
	}
	outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -100}})
	settle()
	if outer.Offset.Y != 100 {
		t.Fatalf("turning back down reached %v, want 100", outer.Offset.Y)
	}
}
