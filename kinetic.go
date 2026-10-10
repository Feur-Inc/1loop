package main

import (
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// A touchpad stops sending events the moment the fingers lift, and Fyne's
// Scroll stops with them. kineticScroll measures how fast the fingers were
// moving and keeps the content gliding afterwards, slowing down smoothly.
const (
	kineticGap      = 80 * time.Millisecond // events closer than this belong to one gesture
	kineticRelease  = 70 * time.Millisecond // silence this long means the fingers are up
	kineticTick     = 16 * time.Millisecond // one animation frame
	kineticDecay    = 0.38                  // seconds for the speed to fall to 1/e
	kineticMinSpeed = 30.0                  // below this many points per second the glide ends
	kineticMinSteps = 4                     // fewer events than this is a mouse wheel, not a touchpad
	kineticMaxSpeed = 9000.0
)

type kineticScroll struct {
	container.Scroll

	last   time.Time
	vx, vy float64 // points per second, smoothed over the gesture
	steps  int     // events in the current gesture
	gen    int     // bumped to cancel a pending release or a running glide
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
	k.gen++ // fingers are on the pad again: stop any glide
	now := time.Now()
	dx, dy := float64(ev.Scrolled.DX), float64(ev.Scrolled.DY)
	dt := now.Sub(k.last)
	if k.last.IsZero() || dt > kineticGap || dt <= 0 || k.reversed(dx, dy) {
		k.vx, k.vy, k.steps = 0, 0, 0
	} else {
		s := dt.Seconds()
		k.vx = 0.55*k.vx + 0.45*dx/s
		k.vy = 0.55*k.vy + 0.45*dy/s
	}
	k.last = now
	k.steps++
	k.Scroll.Scrolled(ev)

	gen := k.gen
	time.AfterFunc(kineticRelease, func() {
		fyne.Do(func() {
			if gen == k.gen {
				k.glide()
			}
		})
	})
}

// reversed reports a change of direction, which starts a new gesture.
func (k *kineticScroll) reversed(dx, dy float64) bool {
	return dx*k.vx < 0 || dy*k.vy < 0
}

// glide animates the content from the release speed down to rest.
func (k *kineticScroll) glide() {
	if k.steps < kineticMinSteps {
		return
	}
	vx := math.Max(-kineticMaxSpeed, math.Min(kineticMaxSpeed, k.vx))
	vy := math.Max(-kineticMaxSpeed, math.Min(kineticMaxSpeed, k.vy))
	if math.Hypot(vx, vy) < kineticMinSpeed*3 {
		return
	}
	k.gen++
	gen := k.gen
	prev := time.Now()
	go func() {
		tick := time.NewTicker(kineticTick)
		defer tick.Stop()
		for now := range tick.C {
			dt := now.Sub(prev).Seconds()
			prev = now
			done := make(chan bool, 1)
			fyne.Do(func() {
				if gen != k.gen {
					done <- true
					return
				}
				decay := math.Exp(-dt / kineticDecay)
				vx *= decay
				vy *= decay
				before := k.Offset
				k.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DX: float32(vx * dt), DY: float32(vy * dt)}})
				stuck := k.Offset == before // reached an edge
				done <- stuck || math.Hypot(vx, vy) < kineticMinSpeed
			})
			if <-done {
				return
			}
		}
	}()
}
