package test

// 1loop (issue #476): under the test driver, fyne.Do used to run its
// function on the goroutine that called it. App code that hands work to the
// UI thread from a timer or a goroutine (go fyne.Do(...), time.AfterFunc)
// then ran at the same time as the test, and tests failed with data races
// and "concurrent map writes". Now the test goroutine (the one that made the
// app) is the UI thread: work from other goroutines waits in a queue and
// runs there, the next time the test calls fyne.Do or DoAndWait, Drain, or
// makes a new app. A test waiting for such work calls Drain while it waits.
// Only fyne.DoAndWait from another goroutine, which would block forever if
// the test goroutine is itself waiting for that goroutine, gets a helper: if
// the queue is not drained for a moment, the helper runs it.

import (
	"sync"
	"time"

	"fyne.io/fyne/v2/internal/async"
)

// drainIdle is how long queued work waits for the test goroutine.
const drainIdle = 20 * time.Millisecond

type queuedCall struct {
	f    func()
	done chan struct{}
}

var ui struct {
	sync.Mutex
	owner   uint64 // the test goroutine
	queue   []queuedCall
	running sync.Mutex // held while queued work or the owner's fyne.Do runs
	runner  uint64     // the goroutine holding running, for nested calls
	timer   *time.Timer
}

// setOwner makes the calling goroutine the UI thread.
func setOwner() {
	Drain()
	ui.Lock()
	ui.owner = async.GoroutineID()
	ui.Unlock()
}

// Drain runs the work other goroutines handed to the UI thread, on the
// calling goroutine, until the queue is empty.
func Drain() {
	for {
		ui.Lock()
		if len(ui.queue) == 0 {
			ui.Unlock()
			return
		}
		call := ui.queue[0]
		ui.queue = ui.queue[1:]
		ui.Unlock()
		runUI(call.f)
		if call.done != nil {
			close(call.done)
		}
	}
}

// runUI runs f as the UI thread: one at a time, nested calls inline.
func runUI(f func()) {
	id := async.GoroutineID()
	ui.Lock()
	nested := ui.runner == id
	ui.Unlock()
	if nested {
		f()
		return
	}
	ui.running.Lock()
	ui.Lock()
	ui.runner = id
	ui.Unlock()
	defer func() {
		ui.Lock()
		ui.runner = 0
		ui.Unlock()
		ui.running.Unlock()
	}()
	f()
}

func doOnUI(f func(), wait bool) {
	id := async.GoroutineID()
	ui.Lock()
	owner, runner := ui.owner, ui.runner
	ui.Unlock()
	if id == owner || id == runner {
		Drain()
		runUI(f)
		return
	}
	call := queuedCall{f: f}
	if wait {
		call.done = make(chan struct{})
	}
	ui.Lock()
	ui.queue = append(ui.queue, call)
	if wait {
		if ui.timer == nil {
			ui.timer = time.AfterFunc(drainIdle, drainLater)
		} else {
			ui.timer.Reset(drainIdle)
		}
	}
	ui.Unlock()
	if wait {
		<-call.done
	}
}

// drainLater runs the queue when the test goroutine left it waiting.
func drainLater() {
	Drain()
}
