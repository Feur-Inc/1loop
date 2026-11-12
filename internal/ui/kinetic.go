// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"math"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/platform"
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
// A wheel is eased, as browsers do: each notch's distance is travelled over
// the next frames, fast at first and slowing into place (wheelEase), and
// notches that follow add to what is left. A notch or a slow turn stops
// exactly where the wheel aimed.
//
// A flick of the wheel glides (#374): from the third notch within a tenth of
// a second (wheelFlick), what is left to travel slows down at a touchpad's
// pace (kineticDecay) instead of the wheel's, and grows so the content keeps
// the speed it had. That growth is all the flick adds to what the wheel
// turned, at most wheelMaxFling. Notches that keep coming add to the glide;
// a notch the other way stops it.
//
// Fyne turns a notch into 25 points on Linux and Windows (scrollSpeed in
// its GLFW driver): GLFW reports 1 per notch, from X11's buttons 4 to 7 and
// from Windows' WHEEL_DELTA of 120. On Wayland GLFW divides the axis value
// by 10, and a wheel click is 15 there: 37.5 points. On macOS (10 points
// per unit, after the system's own acceleration) the system already adds
// momentum to touchpads and mice, so a wheel never glides there.
//
// Only Wayland tells a wheel from a touchpad (scrollFinger). Elsewhere an
// event counts as a wheel notch for a flick only when it is a whole number
// of notches: a Windows precision touchpad sends fractions (and its own
// momentum), while X11 turns every scroll into whole notches, so a quick
// touchpad swipe there glides like a flicked wheel.
const (
	kineticWindow   = 100 * time.Millisecond // the speed is measured over this much of the gesture's end
	kineticStale    = 60 * time.Millisecond  // fingers that rested this long before leaving throw nothing
	kineticDecay    = 0.325                  // seconds for the speed to fall to 1/e
	kineticMinStart = 80.0                   // slower than this at release (points per second): no glide
	kineticMinSpeed = 12.0                   // the glide ends below this
	kineticMaxSpeed = 7000.0
	kineticMaxFrame = 0.05            // a late frame does not jump further than this many seconds
	wheelEase       = 0.07            // seconds for a wheel's remaining distance to fall to 1/e (settles in about 0.25 s)
	wheelNotch      = 25.0            // points per wheel notch on Linux and Windows (Fyne's scrollSpeed)
	wheelFlick      = 3               // notches within kineticWindow that make a flick
	wheelMaxFling   = 12 * wheelNotch // the most a flick adds to what the wheel turned
)

// wheelGlides is false where the system adds its own momentum (macOS).
var wheelGlides = runtime.GOOS != "darwin"

// wheelClock times the notches where the display gives no time (tests set it).
var wheelClock = time.Now

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

	aheadX, aheadY float64         // what the wheel turned and the content has yet to travel
	easeTime       float64         // seconds for aheadX/Y to fall to 1/e: wheelEase, or kineticDecay in a flick (0: wheelEase)
	notches        []kineticSample // the wheel's last notches, within kineticWindow
}

// kineticActive is the scroll the fingers were last on: the only one that
// can be gliding or about to.
var kineticActive *kineticScroll

// newKineticVScroll is container.NewVScroll with inertia.
func newKineticVScroll(content fyne.CanvasObject) *kineticScroll {
	scroll := &kineticScroll{}
	scroll.Direction = container.ScrollVerticalOnly
	scroll.Content = content
	scroll.ExtendBaseWidget(scroll)
	return scroll
}

// Scrolled is called by the driver for every wheel or touchpad event.
func (scroll *kineticScroll) Scrolled(ev *fyne.ScrollEvent) {
	if kineticActive != nil && kineticActive != scroll {
		kineticActive.rest()
	}
	kineticActive = scroll
	scroll.gliding = false // fingers on the pad again: the content follows them, nothing else
	if finger, told := scrollFinger(); !told || !finger {
		// A wheel, or no telling: eased over the next frames (kineticFrame).
		scroll.samples = scroll.samples[:0]
		scroll.wheel(ev, told)
		return
	}
	scroll.aheadX, scroll.aheadY, scroll.easeTime = 0, 0, 0
	scroll.notches = scroll.notches[:0]
	now := time.Now()
	at := time.Duration(now.UnixNano())
	if ms, ok := scrollEventTime(); ok {
		at = time.Duration(ms) * time.Millisecond
	}
	if n := len(scroll.samples); n > 0 && now.Sub(scroll.samples[n-1].heard) > 2*kineticWindow {
		scroll.samples = scroll.samples[:0] // a new gesture
	}
	scroll.samples = append(scroll.samples, kineticSample{at, now, float64(ev.Scrolled.DX), float64(ev.Scrolled.DY)})
	for len(scroll.samples) > 1 && at-scroll.samples[0].at > kineticWindow {
		scroll.samples = scroll.samples[1:]
	}
	scroll.Scroll.Scrolled(ev)
}

// wheel takes a notch (or anything the display cannot tell from one).
// isWheel: the display says it is a wheel.
func (scroll *kineticScroll) wheel(ev *fyne.ScrollEvent, isWheel bool) {
	dx, dy := float64(ev.Scrolled.DX), float64(ev.Scrolled.DY)
	if dx*scroll.aheadX < 0 || dy*scroll.aheadY < 0 {
		// Turned back: what was left of the way, and of a glide, is dropped.
		scroll.aheadX, scroll.aheadY, scroll.easeTime = 0, 0, 0
		scroll.notches = scroll.notches[:0]
	}
	scroll.aheadX += dx
	scroll.aheadY += dy
	if !isWheel && (!wheelGlides || !wholeNotches(dx) || !wholeNotches(dy)) {
		scroll.notches = scroll.notches[:0] // not a wheel's notch: eased, never thrown
		return
	}
	now := wheelClock()
	at := time.Duration(now.UnixNano())
	if ms, ok := scrollEventTime(); ok {
		at = time.Duration(ms) * time.Millisecond
	}
	scroll.notches = append(scroll.notches, kineticSample{at, now, dx, dy})
	for len(scroll.notches) > 1 && at-scroll.notches[0].at > kineticWindow {
		scroll.notches = scroll.notches[1:]
	}
	if scroll.easeTime == 0 && len(scroll.notches) >= wheelFlick {
		scroll.fling()
	}
	if scroll.easeTime != 0 {
		// The speed is what is left over easeTime: kept under a touchpad's.
		if most := kineticMaxSpeed * scroll.easeTime; math.Hypot(scroll.aheadX, scroll.aheadY) > most {
			k := most / math.Hypot(scroll.aheadX, scroll.aheadY)
			scroll.aheadX, scroll.aheadY = scroll.aheadX*k, scroll.aheadY*k
		}
	}
}

func wholeNotches(d float64) bool {
	return math.Mod(math.Abs(d), wheelNotch) == 0
}

// fling makes the wheel's way left glide: it slows down at a touchpad's
// pace, and grows so the content keeps the speed it has (speed is what is
// left over the time constant), by wheelMaxFling at most.
func (scroll *kineticScroll) fling() {
	grow := kineticDecay/wheelEase - 1
	ex, ey := scroll.aheadX*grow, scroll.aheadY*grow
	if extra := math.Hypot(ex, ey); extra > wheelMaxFling {
		ex, ey = ex*wheelMaxFling/extra, ey*wheelMaxFling/extra
	}
	scroll.aheadX += ex
	scroll.aheadY += ey
	scroll.easeTime = kineticDecay
	platform.Devf("ui", "wheel flicked (%d notches): gliding %.0f points past where it aimed", len(scroll.notches), math.Hypot(ex, ey))
}

// kineticFrame runs at each turn of the loop (frame hook), after the
// events of that turn were handled.
func kineticFrame(now time.Time, elapsed time.Duration) {
	scroll := kineticActive
	if scroll == nil {
		return
	}
	if scroll.aheadX != 0 || scroll.aheadY != 0 {
		scroll.ease(math.Min(elapsed.Seconds(), kineticMaxFrame))
	}
	if scroll.gliding {
		scroll.glide(math.Min(elapsed.Seconds(), kineticMaxFrame))
		return
	}
	if len(scroll.samples) == 0 {
		return
	}
	last := scroll.samples[len(scroll.samples)-1]
	finger, released, told := scrollGesture()
	switch {
	case !told || !finger:
		scroll.samples = scroll.samples[:0] // a wheel, or no telling: no inertia
	case released:
		scroll.release(now.Sub(last.heard) > kineticStale)
	}
}

// release starts the glide at the speed of the end of the gesture, unless
// the fingers had already come to rest (still).
func (scroll *kineticScroll) release(still bool) {
	samples := scroll.samples
	scroll.samples = scroll.samples[:0]
	if still || len(samples) < 3 {
		return
	}
	// What the first sample moved was covered before the window began.
	span := (samples[len(samples)-1].at - samples[0].at).Seconds()
	if span < 0.02 {
		return // too little of the gesture to tell a speed
	}
	var dx, dy float64
	for _, kineticSample := range samples[1:] {
		dx += kineticSample.dx
		dy += kineticSample.dy
	}
	scroll.vx, scroll.vy = clampSpeed(dx/span), clampSpeed(dy/span)
	if math.Hypot(scroll.vx, scroll.vy) < kineticMinStart {
		scroll.rest()
		return
	}
	scroll.gliding = true
	platform.Devf("ui", "scroll released at %.0f points/s (%d events over %.0f ms): gliding", math.Hypot(scroll.vx, scroll.vy), len(samples), span*1000)
}

func clampSpeed(v float64) float64 {
	return math.Max(-kineticMaxSpeed, math.Min(kineticMaxSpeed, v))
}

func (scroll *kineticScroll) rest() {
	scroll.gliding = false
	scroll.vx, scroll.vy = 0, 0
	scroll.aheadX, scroll.aheadY, scroll.easeTime = 0, 0, 0
	scroll.notches = scroll.notches[:0]
}

// ease moves the content for one frame of dt seconds towards where the
// wheel sent it: the same share of the distance left in every 70 ms (325 ms
// in a flick), whatever the frame rate, and the last half point at once (dropped in
// a flick's glide).
func (scroll *kineticScroll) ease(dt float64) {
	if dt <= 0 {
		return
	}
	tau := scroll.easeTime
	if tau == 0 {
		tau = wheelEase
	}
	share := 1 - math.Exp(-dt/tau)
	mx, my := scroll.aheadX*share, scroll.aheadY*share
	last := math.Hypot(scroll.aheadX-mx, scroll.aheadY-my) < 0.5
	gliding := scroll.easeTime != 0
	if last && !gliding {
		mx, my = scroll.aheadX, scroll.aheadY
	}
	scroll.aheadX -= mx
	scroll.aheadY -= my
	before := scroll.Offset
	scroll.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: float32(mx), DY: float32(my)}})
	edge := scroll.Offset == before || !scroll.Visible()
	if edge || last {
		// Arrived, or an edge: nothing left to travel, and the next notch
		// starts afresh. A glide has no exact aim: it ends where it is
		// rather than jump its last half point.
		scroll.aheadX, scroll.aheadY, scroll.easeTime = 0, 0, 0
		scroll.notches = scroll.notches[:0]
	}
	if last && !edge && !gliding {
		// The steps' float32 rounding adds up to a few millionths of a
		// point: the motion ends on the whole point the wheel aimed at.
		at := scroll.Offset
		x, y := float32(math.Round(float64(at.X))), float32(math.Round(float64(at.Y)))
		if math.Abs(float64(x-at.X)) < 0.01 && math.Abs(float64(y-at.Y)) < 0.01 && (x != at.X || y != at.Y) {
			scroll.ScrollToOffset(fyne.NewPos(x, y))
		}
	}
}

// glide moves the content for one frame of dt seconds. The speed decays
// exponentially; the distance is its exact integral over the frame, so the
// motion is the same whatever the frame rate.
func (scroll *kineticScroll) glide(dt float64) {
	if dt <= 0 {
		return
	}
	decay := math.Exp(-dt / kineticDecay)
	reach := kineticDecay * (1 - decay) // seconds of travel at the frame's starting speed
	mx, my := scroll.vx*reach, scroll.vy*reach
	scroll.vx *= decay
	scroll.vy *= decay
	before := scroll.Offset
	scroll.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: float32(mx), DY: float32(my)}})
	if scroll.Offset == before || math.Hypot(scroll.vx, scroll.vy) < kineticMinSpeed || !scroll.Visible() {
		scroll.rest() // an edge, or slow enough to stop
	}
}

// sideScroll is container.NewHScroll for a block inside a vertical scroll
// (code in an answer). Fyne gives every wheel event to the innermost scroll
// under the pointer, and its horizontal scroll turned a vertical wheel
// sideways: over a code block the conversation stopped, even when the block
// had nothing to scroll. Here the block keeps only the sideways motion it
// can make, and the rest goes on to the scroll around it.
type sideScroll struct {
	container.Scroll
	outer fyne.Scrollable // found on the first event that needs it
}

func newSideScroll(content fyne.CanvasObject) *sideScroll {
	scroll := &sideScroll{}
	scroll.Direction = container.ScrollHorizontalOnly
	scroll.Content = content
	scroll.ExtendBaseWidget(scroll)
	return scroll
}

func (scroll *sideScroll) Scrolled(ev *fyne.ScrollEvent) {
	dx, dy := ev.Scrolled.DX, ev.Scrolled.DY
	if scroll.canScroll(dx) {
		if kineticActive != nil {
			kineticActive.rest()
		}
		scroll.Scroll.Scrolled(&fyne.ScrollEvent{PointEvent: ev.PointEvent, Scrolled: fyne.Delta{DX: dx}})
		dx = 0
	}
	if dx == 0 && dy == 0 {
		return
	}
	if scroll.outer == nil {
		if c := fyne.CurrentApp().Driver().CanvasForObject(scroll); c != nil {
			scroll.outer, _ = scrollAround(c.Content(), scroll, nil)
		}
	}
	if scroll.outer != nil {
		scroll.outer.Scrolled(&fyne.ScrollEvent{PointEvent: ev.PointEvent, Scrolled: fyne.Delta{DX: dx, DY: dy}})
	}
}

// canScroll tells whether the content can move by dx (positive: towards
// its start).
func (scroll *sideScroll) canScroll(dx float32) bool {
	switch {
	case dx > 0:
		return scroll.Offset.X > 0
	case dx < 0:
		return scroll.Offset.X+scroll.Size().Width < scroll.Content.MinSize().Width
	}
	return false
}

// scrollAround finds the innermost scroll that holds target, below object
// (outer: the one around object).
func scrollAround(object, target fyne.CanvasObject, outer fyne.Scrollable) (fyne.Scrollable, bool) {
	if object == target {
		return outer, true
	}
	switch object := object.(type) {
	case *fyne.Container:
		for _, child := range object.Objects {
			if found, ok := scrollAround(child, target, outer); ok {
				return found, true
			}
		}
	case *container.ThemeOverride:
		return scrollAround(object.Content, target, outer)
	case *container.Scroll:
		return scrollAround(object.Content, target, object)
	case *kineticScroll:
		return scrollAround(object.Content, target, object)
	case *sideScroll:
		return scrollAround(object.Content, target, object)
	}
	return nil, false
}
