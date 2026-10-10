package main

// The frame hook: one function that runs on the interface thread at every
// turn of Fyne's loop (about sixty times a second, drawing or not), just
// before a frame may be drawn. It carries what must follow the loop itself:
//
//   - frame pacing on Wayland (see framePaceWait in chrome_wayland.go);
//   - the scroll's inertia (kinetic.go);
//   - three safety nets against a window that stops showing what the
//     application does, described below;
//   - the developer log's frame statistics and stall reports.
//
// The safety nets. Fyne only redraws a window when one of its objects is
// refreshed, and it finds the window of an object in a registry whose
// entries expire after a minute. An entry is renewed when its own object is
// refreshed, or by a walk of the whole window that Fyne only does while the
// window is NOT being redrawn. So during a long animation (the work ring of
// a running agent) objects nobody refreshes can drop out of the registry,
// and from then on refreshing them does nothing: the application goes on,
// the screen stays as it was until the window is resized. 1loop therefore
//
//  1. renews every object's entry itself, every few seconds;
//  2. checks at each frame that the window's root is still registered, and
//     if it is not, redraws and refreshes everything;
//  3. redraws the window every two seconds whatever happens, so that no
//     cause, known or not, can leave a stale picture on screen for long.

import (
	"reflect"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
)

const (
	knownEvery   = 5 * time.Second        // 1. well under the registry's one minute
	redrawEvery  = 2 * time.Second        // 3.
	stallLogged  = 120 * time.Millisecond // a longer gap between two turns is reported (-dev)
	stallHealed  = 2 * time.Second        // after a longer one everything is refreshed
	framesLogged = 10 * time.Second       // how often the frame statistics are logged (-dev)
)

type frameHook struct {
	u       *desktopUI
	anim    *fyne.Animation
	at      time.Time  // the previous turn
	known   time.Time  // when the registry was last renewed
	redrawn time.Time  // when the window was last redrawn on principle
	walked  *time.Time // Fyne's own record of its last redraw, nil when unavailable
	seen    time.Time  // its value at the previous turn
	refresh int        // turns left before everything is refreshed, 0 when idle

	// developer log
	since   time.Time
	turns   int
	painted int
	worst   time.Duration
}

// startFrames installs the hook. A Fyne animation is ticked at every turn
// of the loop; this one never ends and animates nothing.
func (u *desktopUI) startFrames() {
	h := &frameHook{u: u, at: time.Now(), since: time.Now()}
	// Fyne notes the time of each redraw in the window; reading it tells
	// whether the last turn drew (the pacing waits for that frame only).
	if w := reflect.ValueOf(u.window); w.Kind() == reflect.Pointer && w.Elem().Kind() == reflect.Struct {
		if field := w.Elem().FieldByName("lastWalkedTime"); field.IsValid() && field.CanAddr() && field.Type() == reflect.TypeOf(time.Time{}) {
			h.walked = (*time.Time)(unsafe.Pointer(field.UnsafeAddr()))
			h.seen = *h.walked
		}
	}
	devf("frame", "frame hook on, redraw record %v", h.walked != nil)
	framePaceSetup(u.window)
	h.anim = fyne.NewAnimation(time.Hour, func(float32) { h.turn() })
	h.anim.RepeatCount = fyne.AnimationRepeatForever
	h.anim.Start()
	u.frames = h
}

func (h *frameHook) turn() {
	u, now := h.u, time.Now()
	gap := now.Sub(h.at)
	h.at = now
	frameBeat.Store(now.UnixNano())

	painted := false
	if h.walked != nil {
		painted = !h.walked.Equal(h.seen)
		h.seen = *h.walked
	}
	if dev != nil {
		h.turns++
		if painted {
			h.painted++
		}
		if gap > h.worst {
			h.worst = gap
		}
		if gap > stallLogged {
			devf("perf", "the interface thread was busy for %s", gap.Round(time.Millisecond))
		}
		if span := now.Sub(h.since); span > framesLogged {
			devf("perf", "%.0f s: %d turns, %d frames drawn, longest turn %s", span.Seconds(), h.turns, h.painted, h.worst.Round(100*time.Microsecond))
			h.since, h.turns, h.painted, h.worst = now, 0, 0, 0
		}
	}

	framePaceWait(painted)
	kineticFrame(now, gap)

	canvas := u.window.Canvas()
	driver := fyne.CurrentApp().Driver()
	if now.Sub(h.known) > knownEvery {
		h.known = now
		keepKnown(canvas)
	}
	lost := driver.CanvasForObject(u.root) == nil
	switch {
	case lost:
		devf("frame", "the window no longer knew its content: redrawing and refreshing everything")
		h.heal()
	case gap > stallHealed:
		devf("frame", "after a %s stall: redrawing and refreshing everything", gap.Round(time.Millisecond))
		h.heal()
	case now.Sub(h.redrawn) > redrawEvery:
		h.redrawn = now
		setDirty(canvas)
	}
	if h.refresh > 0 {
		if h.refresh--; h.refresh == 0 {
			// The redraw asked by heal has registered every object again:
			// now a refresh reaches them all, and drops what they had drawn
			// from the state they were in when their refreshes were lost.
			u.window.Content().Refresh()
		}
	}
}

// heal redraws now and refreshes everything two turns later.
func (h *frameHook) heal() {
	setDirty(h.u.window.Canvas())
	h.redrawn = time.Now()
	if h.refresh == 0 {
		h.refresh = 2
	}
}

// setDirty makes Fyne draw the window at this turn. The method belongs to
// Fyne's canvas implementation, not to its public interface.
func setDirty(canvas fyne.Canvas) {
	if c, ok := canvas.(interface{ SetDirty() }); ok {
		c.SetDirty()
	}
}

// keepKnown renews the registry entry of every object of the window, by
// asking for each one's canvas (which is what renews it). The walk is
// Fyne's own, the one it uses for the same purpose on an idle window; its
// callback type is internal to Fyne, hence the reflection.
func keepKnown(canvas fyne.Canvas) {
	defer devSlow("renewing the window's objects", 4*time.Millisecond, time.Now())
	walk := reflect.ValueOf(canvas).MethodByName("WalkTrees")
	if !walk.IsValid() || walk.Type().NumIn() != 2 || walk.Type().In(1).Kind() != reflect.Func {
		return
	}
	driver := fyne.CurrentApp().Driver()
	visit := reflect.MakeFunc(walk.Type().In(1), func(args []reflect.Value) []reflect.Value {
		if object := args[0].MethodByName("Obj"); object.IsValid() {
			if o, ok := object.Call(nil)[0].Interface().(fyne.CanvasObject); ok && o != nil {
				driver.CanvasForObject(o)
			}
		}
		return nil
	})
	walk.Call([]reflect.Value{reflect.Zero(walk.Type().In(0)), visit})
}
