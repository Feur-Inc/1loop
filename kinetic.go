package main

import (
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// A touchpad stops sending events the moment the fingers lift, and Fyne's
// Scroll stops with them. kineticScroll keeps the content moving at the
// speed the fingers had, slowing down smoothly, like every native toolkit.
//
// Three things make it feel right, and each was a jerk when it was wrong:
//
//   - The glide starts when the fingers LEAVE the pad, which the display
//     says (Wayland's axis_stop, see scrollGesture). Guessing it from a
//     silence between two events made either a pause before the glide, or
//     a glide in the middle of a slow gesture.
//   - The speed is the distance over the last tenth of a second of the
//     gesture, on the display's clock. Events reach 1loop in batches, one
//     per turn of its loop: timing them on arrival gave absurd speeds
//     (two events "a microsecond apart") and threw the content away.
//   - The glide advances from the frame hook, by the time really elapsed
//     since the previous frame, not from a timer of its own.
//
// A mouse wheel has no inertia: only gestures from a touchpad glide.
const (
	kineticWindow   = 100 * time.Millisecond // the speed is measured over this much of the gesture's end
	kineticStale    = 60 * time.Millisecond  // fingers that rested this long before leaving throw nothing
	kineticSilence  = 45 * time.Millisecond  // without a release event: this silence ends the gesture
	kineticDecay    = 0.325                  // seconds for the speed to fall to 1/e
	kineticMinStart = 80.0                   // slower than this at release (points per second): no glide
	kineticMinSpeed = 12.0                   // the glide ends below this
	kineticMaxSpeed = 7000.0
	kineticMaxFrame = 0.05 // a late frame does not jump further than this many seconds
)

type kineticSample struct {
	at     time.Duration // on the display's clock when it gives one, else on ours
	heard  time.Time
	dx, dy float64
}

type kineticScroll struct {
	container.Scroll

	samples []kineticSample // the end of the gesture in progress
	vx, vy  float64         // glide speed in points per second
	gliding bool
}

// kineticActive is the scroll the fingers were last on: the only one that
// can be gliding or about to.
var kineticActive *kineticScroll

// newKineticVScroll is container.NewVScroll with inertia.
func newKineticVScroll(content fyne.CanvasObject) *kineticScroll {
	k := &kineticScroll{}
	k.Direction = container.ScrollVerticalOnly
	k.Content = content
	k.ExtendBaseWidget(k)
	return k
}

// Scrolled is called by the driver for every wheel or touchpad event.
func (k *kineticScroll) Scrolled(ev *fyne.ScrollEvent) {
	if kineticActive != nil && kineticActive != k {
		kineticActive.rest()
	}
	kineticActive = k
	k.gliding = false // fingers on the pad again: the content follows them, nothing else
	now := time.Now()
	at := time.Duration(now.UnixNano())
	if ms, ok := scrollEventTime(); ok {
		at = time.Duration(ms) * time.Millisecond
	}
	if n := len(k.samples); n > 0 && now.Sub(k.samples[n-1].heard) > 2*kineticWindow {
		k.samples = k.samples[:0] // a new gesture
	}
	k.samples = append(k.samples, kineticSample{at, now, float64(ev.Scrolled.DX), float64(ev.Scrolled.DY)})
	for len(k.samples) > 1 && at-k.samples[0].at > kineticWindow {
		k.samples = k.samples[1:]
	}
	k.Scroll.Scrolled(ev)
}

// kineticFrame runs at each turn of the loop (frame hook), after the
// events of that turn were handled.
func kineticFrame(now time.Time, elapsed time.Duration) {
	k := kineticActive
	if k == nil {
		return
	}
	if k.gliding {
		k.glide(math.Min(elapsed.Seconds(), kineticMaxFrame))
		return
	}
	if len(k.samples) == 0 {
		return
	}
	last := k.samples[len(k.samples)-1]
	finger, released, told := scrollGesture()
	switch {
	case told && !finger:
		k.samples = k.samples[:0] // a wheel: no inertia
	case told && released:
		k.release(now.Sub(last.heard) > kineticStale)
	case !told && now.Sub(last.heard) > kineticSilence:
		// This display does not say when fingers lift: a gesture of several
		// events that falls silent is taken as released.
		k.release(len(k.samples) < 4)
	}
}

// release starts the glide at the speed of the end of the gesture, unless
// the fingers had already come to rest (still).
func (k *kineticScroll) release(still bool) {
	samples := k.samples
	k.samples = k.samples[:0]
	if still || len(samples) < 3 {
		return
	}
	// What the first sample moved was covered before the window began.
	span := (samples[len(samples)-1].at - samples[0].at).Seconds()
	if span < 0.02 {
		return // too little of the gesture to tell a speed
	}
	var dx, dy float64
	for _, s := range samples[1:] {
		dx += s.dx
		dy += s.dy
	}
	k.vx, k.vy = clampSpeed(dx/span), clampSpeed(dy/span)
	if math.Hypot(k.vx, k.vy) < kineticMinStart {
		k.rest()
		return
	}
	k.gliding = true
	devf("ui", "scroll released at %.0f points/s (%d events over %.0f ms): gliding", math.Hypot(k.vx, k.vy), len(samples), span*1000)
}

func clampSpeed(v float64) float64 {
	return math.Max(-kineticMaxSpeed, math.Min(kineticMaxSpeed, v))
}

func (k *kineticScroll) rest() {
	k.gliding = false
	k.vx, k.vy = 0, 0
}

// glide moves the content for one frame of dt seconds. The speed decays
// exponentially; the distance is its exact integral over the frame, so the
// motion is the same whatever the frame rate.
func (k *kineticScroll) glide(dt float64) {
	if dt <= 0 {
		return
	}
	decay := math.Exp(-dt / kineticDecay)
	reach := kineticDecay * (1 - decay) // seconds of travel at the frame's starting speed
	mx, my := k.vx*reach, k.vy*reach
	k.vx *= decay
	k.vy *= decay
	before := k.Offset
	k.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: float32(mx), DY: float32(my)}})
	if k.Offset == before || math.Hypot(k.vx, k.vy) < kineticMinSpeed || !k.Visible() {
		k.rest() // an edge, or slow enough to stop
	}
}
