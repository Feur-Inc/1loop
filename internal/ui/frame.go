package ui

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

	"1loop/internal/platform"
)

const (
	knownEvery   = 5 * time.Second        // 1. well under the registry's one minute
	redrawEvery  = 2 * time.Second        // 3.
	stallLogged  = 120 * time.Millisecond // a longer gap between two turns is reported (-dev)
	stallHealed  = 2 * time.Second        // after a longer one everything is refreshed
	framesLogged = 10 * time.Second       // how often the frame statistics are logged (-dev)
)

type frameHook struct {
	ui      *desktopUI
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
func (ui *desktopUI) startFrames() {
	hook := &frameHook{ui: ui, at: time.Now(), since: time.Now()}
	// Fyne notes the time of each redraw in the window; reading it tells
	// whether the last turn drew (the pacing waits for that frame only).
	if w := reflect.ValueOf(ui.window); w.Kind() == reflect.Pointer && w.Elem().Kind() == reflect.Struct {
		if field := w.Elem().FieldByName("lastWalkedTime"); field.IsValid() && field.CanAddr() && field.Type() == reflect.TypeOf(time.Time{}) {
			hook.walked = (*time.Time)(unsafe.Pointer(field.UnsafeAddr()))
			hook.seen = *hook.walked
		}
	}
	platform.Devf("frame", "frame hook on, redraw record %v", hook.walked != nil)
	framePaceSetup(ui.window)
	hook.anim = fyne.NewAnimation(time.Hour, func(float32) { hook.turn() })
	hook.anim.RepeatCount = fyne.AnimationRepeatForever
	hook.anim.Start()
	ui.frames = hook
}

func (hook *frameHook) turn() {
	ui, now := hook.ui, time.Now()
	gap := now.Sub(hook.at)
	hook.at = now
	platform.FrameBeat.Store(now.UnixNano())

	painted := false
	if hook.walked != nil {
		painted = !hook.walked.Equal(hook.seen)
		hook.seen = *hook.walked
	}
	if platform.Dev != nil {
		hook.turns++
		if painted {
			hook.painted++
		}
		if gap > hook.worst {
			hook.worst = gap
		}
		if gap > stallLogged {
			platform.Devf("perf", "the interface thread was busy for %s", gap.Round(time.Millisecond))
		}
		if span := now.Sub(hook.since); span > framesLogged {
			platform.Devf("perf", "%.0f s: %d turns, %d frames drawn, longest turn %s", span.Seconds(), hook.turns, hook.painted, hook.worst.Round(100*time.Microsecond))
			hook.since, hook.turns, hook.painted, hook.worst = now, 0, 0, 0
		}
	}

	framePaceWait(painted)
	kineticFrame(now, gap)

	canvas := ui.window.Canvas()
	driver := fyne.CurrentApp().Driver()
	if now.Sub(hook.known) > knownEvery {
		hook.known = now
		keepKnown(canvas)
	}
	lost := driver.CanvasForObject(ui.root) == nil
	switch {
	case lost:
		platform.Devf("frame", "the window no longer knew its content: redrawing and refreshing everything")
		hook.heal()
	case gap > stallHealed:
		platform.Devf("frame", "after a %s stall: redrawing and refreshing everything", gap.Round(time.Millisecond))
		hook.heal()
	case now.Sub(hook.redrawn) > redrawEvery:
		hook.redrawn = now
		setDirty(canvas)
	}
	if hook.refresh > 0 {
		if hook.refresh--; hook.refresh == 0 {
			// The redraw asked by heal has registered every object again:
			// now a refresh reaches them all, and drops what they had drawn
			// from the state they were in when their refreshes were lost.
			ui.window.Content().Refresh()
		}
	}
	ui.advanceChatLoad()
}

// heal redraws now and refreshes everything two turns later.
func (hook *frameHook) heal() {
	setDirty(hook.ui.window.Canvas())
	hook.redrawn = time.Now()
	if hook.refresh == 0 {
		hook.refresh = 2
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
	defer platform.DevSlow("renewing the window's objects", 4*time.Millisecond, time.Now())
	walk := reflect.ValueOf(canvas).MethodByName("WalkTrees")
	if !walk.IsValid() || walk.Type().NumIn() != 2 || walk.Type().In(1).Kind() != reflect.Func {
		return
	}
	driver := fyne.CurrentApp().Driver()
	visit := reflect.MakeFunc(walk.Type().In(1), func(args []reflect.Value) []reflect.Value {
		if object := args[0].MethodByName("Obj"); object.IsValid() {
			if canvasObjectValue, ok := object.Call(nil)[0].Interface().(fyne.CanvasObject); ok && canvasObjectValue != nil {
				driver.CanvasForObject(canvasObjectValue)
			}
		}
		return nil
	})
	walk.Call([]reflect.Value{reflect.Zero(walk.Type().In(0)), visit})
}
