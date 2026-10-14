package main

// A cheap clock for Fyne's caches.
//
// Fyne stamps every object it walks with an expiry time, at every frame
// drawn: hundreds of clock readings per frame. On machines whose kernel
// times with HPET (it set TSC aside as unstable, common on laptops), a
// reading costs microseconds, and these stamps were half of the interface's
// CPU while idle. The stamps only serve one-minute expiries: a clock read
// once per turn of the loop is exact enough, and costs an atomic load.

import (
	"sync/atomic"
	"time"
	_ "unsafe" // go:linkname
)

//go:linkname fyneCacheNow fyne.io/fyne/v2/internal/cache.timeNow
var fyneCacheNow func() time.Time

var coarseNow atomic.Int64

// cheapCacheClock installs the clock; call it before Fyne starts.
func cheapCacheClock() {
	coarseNow.Store(time.Now().UnixNano())
	fyneCacheNow = func() time.Time { return time.Unix(0, coarseNow.Load()) }
	go func() { // the frame hook keeps it fresh; this, should the loop stall
		for range time.Tick(50 * time.Millisecond) {
			coarseNow.Store(time.Now().UnixNano())
		}
	}()
}
