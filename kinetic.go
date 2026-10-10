package main

import (
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// A touchpad stops sending events the moment the fingers lift, and Fyne's
// Scroll stops with them. kineticScroll keeps the content moving at the speed
// the fingers had, slowing down smoothly.
//
// There is no "fingers lifted" event to wait for, and waiting for a silence
// shows as a stop followed by a jump. So the glide starts on the first frame
// that brings no event, and whatever it moved is owed back (debt) to the next
// event if the fingers were in fact still down: the content never moves
// further than the fingers asked, and never pauses when they leave.
const (
	kineticGap      = 100 * time.Millisecond // events further apart start a new gesture
	kineticWindow   = 90 * time.Millisecond  // the speed is measured over this much of the gesture
	kineticDecay    = 0.325                  // seconds for the speed to fall to 1/e
	kineticMinSpeed = 12.0                   // below this many points per second the glide ends
	kineticMinSteps = 4                      // fewer events than this is a mouse wheel, not a touchpad
	kineticMaxSpeed = 8000.0
	kineticMaxFrame = 0.05 // a late frame does not jump further than this many seconds
)

type kineticSample struct {
	at     time.Time
	dx, dy float64
}

type kineticScroll struct {
	container.Scroll

	samples    []kineticSample // the last kineticWindow of the gesture
	steps      int             // events in the current gesture
	vx, vy     float64         // points per second
	debtX      float64         // glided ahead of the events, see above
	debtY      float64
	lastEvent  time.Time
	lastFrame  time.Time
	frames     *fyne.Animation
	frameAlive bool
}

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
	now := time.Now()
	dx, dy := float64(ev.Scrolled.DX), float64(ev.Scrolled.DY)
	if now.Sub(k.lastEvent) > kineticGap || dx*k.vx < 0 || dy*k.vy < 0 {
		// A new gesture, or a change of direction: forget the old speed.
		k.samples, k.steps = k.samples[:0], 0
		k.vx, k.vy, k.debtX, k.debtY = 0, 0, 0, 0
	}
	k.lastEvent = now
	k.steps++
	k.samples = append(k.samples, kineticSample{now, dx, dy})
	for len(k.samples) > 1 && now.Sub(k.samples[0].at) > kineticWindow {
		k.samples = k.samples[1:]
	}
	if span := now.Sub(k.samples[0].at).Seconds(); len(k.samples) > 2 && span > 0 {
		// The first sample's distance was covered before the window began.
		var sx, sy float64
		for _, s := range k.samples[1:] {
			sx += s.dx
			sy += s.dy
		}
		k.vx = clampSpeed(sx / span)
		k.vy = clampSpeed(sy / span)
	}
	dx, k.debtX = settle(dx, k.debtX)
	dy, k.debtY = settle(dy, k.debtY)
	if dx != 0 || dy != 0 {
		k.Scroll.Scrolled(&fyne.ScrollEvent{PointEvent: ev.PointEvent, Scrolled: fyne.Delta{DX: float32(dx), DY: float32(dy)}})
	}
	if k.steps >= kineticMinSteps {
		k.run()
	}
}

// settle pays the debt out of a new movement and returns what is left of both.
func settle(move, debt float64) (float64, float64) {
	if move*debt <= 0 {
		return move, 0
	}
	if math.Abs(debt) >= math.Abs(move) {
		return 0, debt - move
	}
	return move - debt, 0
}

func clampSpeed(v float64) float64 {
	return math.Max(-kineticMaxSpeed, math.Min(kineticMaxSpeed, v))
}

// run starts the frame loop; it stops by itself when the content rests.
func (k *kineticScroll) run() {
	if k.frameAlive {
		return
	}
	k.frameAlive = true
	k.lastFrame = time.Now()
	k.frames = fyne.NewAnimation(time.Hour, func(float32) { k.frame() })
	k.frames.Curve = fyne.AnimationLinear
	k.frames.Start()
}

func (k *kineticScroll) rest() {
	k.frameAlive = false
	k.vx, k.vy, k.debtX, k.debtY = 0, 0, 0, 0
	if k.frames != nil {
		k.frames.Stop()
	}
}

// frame moves the content for one frame in which no event did.
func (k *kineticScroll) frame() {
	if !k.frameAlive {
		return
	}
	now := time.Now()
	dt := math.Min(now.Sub(k.lastFrame).Seconds(), kineticMaxFrame)
	fresh := !k.lastEvent.Before(k.lastFrame) // an event already moved this frame
	k.lastFrame = now
	if fresh || dt <= 0 {
		return
	}
	decay := math.Exp(-dt / kineticDecay)
	k.vx *= decay
	k.vy *= decay
	if math.Hypot(k.vx, k.vy) < kineticMinSpeed {
		k.rest()
		return
	}
	mx, my := k.vx*dt, k.vy*dt
	before := k.Offset
	k.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: float32(mx), DY: float32(my)}})
	if k.Offset == before { // reached an edge
		k.rest()
		return
	}
	k.debtX += mx
	k.debtY += my
}
