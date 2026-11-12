// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

// A flick of the wheel (six notches at once) glides on past where the wheel
// aimed, slowing down smoothly, and by less than half a screen (#374).
func TestWheelFlickGlides(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	for range 6 {
		outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 25}})
	}
	now := time.Now()
	var steps []float32
	for frame := range 240 {
		before := outer.Offset.Y
		kineticFrame(now.Add(time.Duration(frame)*16*time.Millisecond), 16*time.Millisecond)
		steps = append(steps, before-outer.Offset.Y)
	}
	if outer.Offset.Y >= 850 {
		t.Fatalf("the flick stopped at %v: nothing glided past 850, where the wheel aimed", outer.Offset.Y)
	}
	if outer.Offset.Y < 850-300 {
		t.Fatalf("the flick threw the conversation to %v: more than 300 points past 850", outer.Offset.Y)
	}
	moving := 0
	for i, step := range steps {
		if i > 0 && step > steps[i-1] {
			t.Fatalf("frame %d moved %v after %v: a glide only slows down", i, step, steps[i-1])
		}
		if step > 0 {
			moving++
		}
	}
	if moving < 30 {
		t.Fatalf("the flick moved for %d frames, want a glide of half a second or more", moving)
	}
}

// A single notch never glides: it moves exactly one notch.
func TestWheelNotchDoesNotFling(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -25}})
	settle()
	if outer.Offset.Y != 1025 {
		t.Fatalf("one notch down reached %v, want 1025", outer.Offset.Y)
	}
}

// Notches turned slowly, a sixth of a second apart, stop where the wheel aimed.
func TestSlowWheelStopsWhereItAimed(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	clock := time.Now()
	wheelClock = func() time.Time { return clock }
	t.Cleanup(func() { wheelClock = time.Now })
	for range 6 {
		outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 25}})
		for range 10 {
			clock = clock.Add(16 * time.Millisecond)
			kineticFrame(clock, 16*time.Millisecond)
		}
	}
	settle()
	if outer.Offset.Y != 850 {
		t.Fatalf("six slow notches reached %v, want 850", outer.Offset.Y)
	}
}

// A notch the other way stops a glide at once: only that notch moves.
func TestWheelTurnedBackStopsTheGlide(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	for range 6 {
		outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 25}})
	}
	now := time.Now()
	for frame := range 10 {
		kineticFrame(now.Add(time.Duration(frame)*16*time.Millisecond), 16*time.Millisecond)
	}
	at := outer.Offset.Y
	outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -25}})
	settle()
	if d := outer.Offset.Y - (at + 25); d > 0.01 || d < -0.01 {
		t.Fatalf("turned back at %v, settled at %v, want %v", at, outer.Offset.Y, at+25)
	}
}

// A touchpad swipe on Windows comes in fractions of a notch: it is eased,
// never thrown (the system adds its own momentum).
func TestFractionalScrollDoesNotFling(t *testing.T) {
	if _, told := scrollFinger(); told {
		t.Skip("this display reports touchpad gestures")
	}
	outer, _ := scrollFixture(t, false)
	for range 6 {
		outer.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 12.5}})
	}
	settle()
	if outer.Offset.Y != 925 {
		t.Fatalf("six half notches reached %v, want 925", outer.Offset.Y)
	}
}
