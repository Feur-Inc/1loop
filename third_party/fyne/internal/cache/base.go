package cache

import (
	"os"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
)

var (
	ValidDuration     = 1 * time.Minute
	cleanTaskInterval = ValidDuration / 2

	lastClean                     time.Time
	skippedCleanWithCanvasRefresh = false

	// timeNow is the caches' clock. 1loop: the time read once per turn of
	// the driver's loop (Tick), not the system clock: every object walked
	// is stamped at every frame, and on machines that time with HPET each
	// reading costs microseconds (it was half of an idle window's CPU).
	// The stamps serve one-minute expiries.
	timeNow = func() time.Time { return time.Unix(0, coarseNow.Load()) }

	coarseNow atomic.Int64
)

// Tick sets the caches' clock; the driver calls it at each turn of its loop.
func Tick(now time.Time) { coarseNow.Store(now.UnixNano()) }

func init() {
	Tick(time.Now())
	if t, err := time.ParseDuration(os.Getenv("FYNE_CACHE")); err == nil {
		ValidDuration = t
		cleanTaskInterval = ValidDuration / 2
	}
}

// Clean run cache clean task, it should be called on paint events.
func Clean(canvasRefreshed bool) {
	now := timeNow()
	// do not run clean task too fast
	if now.Sub(lastClean) < 10*time.Second {
		if canvasRefreshed {
			skippedCleanWithCanvasRefresh = true
		}
		return
	}
	if skippedCleanWithCanvasRefresh {
		skippedCleanWithCanvasRefresh = false
		canvasRefreshed = true
	}
	if !canvasRefreshed && now.Sub(lastClean) < cleanTaskInterval {
		return
	}
	destroyExpiredSvgs(now)
	destroyExpiredFontMetrics(now)
	if canvasRefreshed {
		// Destroy renderers on canvas refresh to avoid flickering screen.
		destroyExpiredRenderers(now)
		// canvases cache should be invalidated only on canvas refresh, otherwise there wouldn't
		// be a way to recover them later
		destroyExpiredCanvases(now)
	}
	lastClean = timeNow()
}

// CleanCanvas performs a complete remove of all the objects that belong to the specified
// canvas. Usually used to free all objects from a closing windows.
func CleanCanvas(canvas fyne.Canvas) {
	canvases.Range(func(obj fyne.CanvasObject, cinfo *canvasInfo) bool {
		if cinfo.canvas != canvas {
			return true
		}

		canvases.Delete(obj)

		wid, ok := obj.(fyne.Widget)
		if !ok {
			return true
		}
		rinfo, ok := renderers.LoadAndDelete(wid)
		if !ok {
			return true
		}
		rinfo.renderer.Destroy()
		overrides.Delete(wid)
		return true
	})
}

// ResetThemeCaches clears all the svg and text size cache maps
func ResetThemeCaches() {
	svgs.Clear()
	fontSizeCache.Clear()
}

// destroyExpiredCanvases deletes objects from the canvases cache.
func destroyExpiredCanvases(now time.Time) {
	canvases.Range(func(obj fyne.CanvasObject, cinfo *canvasInfo) bool {
		if cinfo.isExpired(now) {
			canvases.Delete(obj)
		}
		return true
	})
}

// destroyExpiredRenderers deletes the renderer from the cache and calls
// renderer.Destroy()
func destroyExpiredRenderers(now time.Time) {
	renderers.Range(func(wid fyne.Widget, rinfo *rendererInfo) bool {
		if rinfo.isExpired(now) {
			rinfo.renderer.Destroy()
			overrides.Delete(wid)
			renderers.Delete(wid)
		}
		return true
	})
}

type expiringCache struct {
	expires time.Time
}

// isExpired check if the cache data is expired.
func (c *expiringCache) isExpired(now time.Time) bool {
	return c.expires.Before(now)
}

// setAlive updates expiration time.
func (c *expiringCache) setAlive() {
	c.expires = timeNow().Add(ValidDuration)
}
