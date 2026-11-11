package glfw

import (
	"fyne.io/fyne/v2/driver"
	"runtime"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/app"
	"fyne.io/fyne/v2/internal/async"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/internal/driver/common"
	"fyne.io/fyne/v2/internal/painter"
	"fyne.io/fyne/v2/internal/scale"
)

const (
	idleRate  = 60                     // turns a second while nothing moves
	busyFor   = 500 * time.Millisecond // how long the loop keeps the screen's rate after input or motion
	movingGap = 50 * time.Millisecond  // frames this close together are something moving
	motionMin = 150 * time.Millisecond // how long it must move to count without input
)

// inputAt is when the user last moved the pointer, scrolled or typed; set
// by the window's callbacks, on the loop's own goroutine.
var inputAt time.Time

type funcData struct {
	f    func()
	done chan struct{} // Zero allocation signalling channel
}

// channel for queuing functions on the main thread
var (
	funcQueue        = async.NewUnboundedChan[funcData]()
	running, drained atomic.Bool
)

// Arrange that main.main runs on main thread.
func init() {
	runtime.LockOSThread()
	async.SetMainGoroutine()
}

// force a function f to run on the main thread
func runOnMain(f func()) {
	runOnMainWithWait(f, true)
}

// force a function f to run on the main thread and specify if we should wait for it to return
func runOnMainWithWait(f func(), wait bool) {
	// If we are on main before app run just execute - otherwise add it to the main queue and wait.
	// We also need to run it as-is if the app is in the process of shutting down as the queue will be stopped.
	if (!running.Load() && async.IsMainGoroutine()) || drained.Load() {
		f()
		return
	}

	if wait {
		done := common.DonePool.Get()
		defer common.DonePool.Put(done)

		funcQueue.In() <- funcData{f: f, done: done}
		<-done
	} else {
		funcQueue.In() <- funcData{f: f}
	}
}

// drawSingleFrame tells whether it drew a window (1loop: the loop's pace).
func (d *gLDriver) drawSingleFrame() bool {
	refreshed := false
	for _, win := range d.windowList() {
		w := win.(*window)
		if w.closing {
			continue
		}

		// CheckDirtyAndClear must be checked after visibility,
		// because when a window becomes visible, it could be
		// showing old content without a dirty flag set to true.
		// Do the clear if and only if the window is visible.
		if !w.visible || !w.canvas.CheckDirtyAndClear() {
			// Window hidden or not being redrawn, mark canvasForObject
			// cache alive if it hasn't been done recently
			// n.b. we need to make sure threshold is a bit *after*
			// time.Now() - CacheDuration()
			threshold := time.Now().Add(10*time.Second - cache.ValidDuration)
			if w.lastWalkedTime.Before(threshold) {
				w.canvas.WalkTrees(nil, func(node *common.RenderCacheNode, _ fyne.Position) {
					// marks canvas for object cache entry alive
					_ = cache.GetCanvasForObject(node.Obj())
					// marks renderer cache entry alive
					if wid, ok := node.Obj().(fyne.Widget); ok {
						_, _ = cache.CachedRenderer(wid)
					}
				})
				w.lastWalkedTime = time.Now()
			}
			continue
		}

		w.RunWithContext(func() {
			if w.driver.repaintWindow(w) {
				refreshed = true
			}
		})
	}
	cache.Clean(refreshed)
	return refreshed
}

func (d *gLDriver) runGL() {
	if !running.CompareAndSwap(false, true) {
		return // Run was called twice.
	}

	d.init()
	if d.trayStart != nil {
		d.trayStart()
	}

	fyne.CurrentApp().Settings().AddListener(func(set fyne.Settings) {
		painter.ClearFontCache()
		cache.ResetThemeCaches()
		app.ApplySettingsWithCallback(set, fyne.CurrentApp(), func(w fyne.Window) {
			c, ok := w.Canvas().(*glCanvas)
			if !ok {
				return
			}
			c.applyThemeOutOfTreeObjects()
			c.reloadScale()
		})
	})

	if f := fyne.CurrentApp().Lifecycle().(*app.Lifecycle).OnStarted(); f != nil {
		f()
	}

	// The loop turns at the screen's refresh rate while the user acts
	// (pointer, wheel, keys: hovers, presses, scrolling follow) or
	// something has been moving for a while (frames drawn back to back
	// for longer than motionMin: the work ring, a long transition), and at
	// 60 Hz otherwise (1loop). The caret's 100 ms fade, twice a second,
	// stays at 60: turning faster for it cost ten times the idle CPU.
	rate := idleRate
	var drawn, moving, busy time.Time
	eventTick := time.NewTicker(time.Second / time.Duration(rate))
	for {
		select {
		case <-d.done:
			eventTick.Stop()
			d.Terminate()
			l := fyne.CurrentApp().Lifecycle().(*app.Lifecycle)
			if f := l.OnStopped(); f != nil {
				l.QueueEvent(f)
			}

			// as we are shutting down make sure we drain the pending funcQueue and close it out.
			for len(funcQueue.Out()) > 0 {
				f := <-funcQueue.Out()
				if f.done != nil {
					f.done <- struct{}{}
				}
			}
			drained.Store(true)
			funcQueue.Close()
			return
		case f := <-funcQueue.Out():
			cache.Tick(time.Now())
			f.f()
			if f.done != nil {
				f.done <- struct{}{}
			}
		case <-eventTick.C:
			cache.Tick(time.Now())
			d.pollEvents()

			for i := 0; i < len(d.windows); i++ {
				w := d.windows[i].(*window)
				if w.viewport == nil {
					continue
				}

				if w.viewport.ShouldClose() {
					d.destroyWindow(w, i)
					i-- // Trailing windows are moved forward one step.
					continue
				}

				expand := w.shouldExpand
				fullScreen := w.fullScreen

				if expand && !fullScreen {
					w.fitContent()
					shouldExpand := w.shouldExpand
					w.shouldExpand = false
					view := w.viewport

					if shouldExpand && runtime.GOOS != "js" {
						view.SetSize(w.shouldWidth, w.shouldHeight)
					}
				}
			}

			d.animation.TickAnimations()
			now := time.Now()
			if d.drawSingleFrame() {
				switch {
				case now.Sub(drawn) >= movingGap:
					moving = time.Time{}
				case moving.IsZero():
					moving = drawn
				case now.Sub(moving) >= motionMin:
					busy = now
				}
				drawn = now
			}
			if inputAt.After(busy) {
				busy = inputAt
			}
			want := idleRate
			if now.Sub(busy) < busyFor {
				want = d.refreshRate()
			}
			if want != rate {
				rate = want
				eventTick.Reset(time.Second / time.Duration(rate))
			}
		}
	}
}

func (d *gLDriver) destroyWindow(w *window, index int) {
	w.visible = false
	w.viewport.Destroy()
	w.destroy(d)

	if index < len(d.windows)-1 {
		copy(d.windows[index:], d.windows[index+1:])
	}
	d.windows[len(d.windows)-1] = nil
	d.windows = d.windows[:len(d.windows)-1]

	if len(d.windows) == 0 {
		d.Quit()
	}
}

func (d *gLDriver) repaintWindow(w *window) bool {
	canvas := w.canvas
	freed := false
	started := time.Now()
	if canvas.EnsureMinSize() {
		w.shouldExpand = true
	}
	freed = canvas.FreeDirtyTextures() > 0
	sized := time.Now()

	updateGLContext(w)
	canvas.paint(canvas.Size())
	painted := time.Now()

	view := w.viewport
	visible := w.visible

	if view != nil && visible {
		view.SwapBuffers()
	}
	if driver.FrameTimes != nil {
		driver.FrameTimes(sized.Sub(started), painted.Sub(sized), time.Since(painted))
	}

	// mark that we have walked the window and don't
	// need to walk it again to mark caches alive
	w.lastWalkedTime = time.Now()
	return freed
}

// refreshWindow requests that the specified window be redrawn
func refreshWindow(w *window) {
	w.canvas.SetDirty()
}

func updateGLContext(w *window) {
	canvas := w.canvas
	size := canvas.Size()

	// w.width and w.height are not correct if we are maximised, so figure from canvas
	winWidth := float32(scale.ToScreenCoordinate(canvas, size.Width)) * canvas.texScale
	winHeight := float32(scale.ToScreenCoordinate(canvas, size.Height)) * canvas.texScale

	canvas.Painter().SetFrameBufferScale(canvas.texScale)
	canvas.Painter().SetOutputSize(int(winWidth), int(winHeight))
}
