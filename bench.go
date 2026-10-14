package main

// Benchmarks of the interface, run by the application itself so they
// measure the real window (ONELOOP_BENCH, with -dev):
//
//	ONELOOP_BENCH=scroll  scrolls the longest chat down and up for 10 s
//	ONELOOP_BENCH=idle    waits 10 s with the composer focused (caret blinking)
//
// Then it writes how many frames were drawn, how long each part of a frame
// took (sizes, paint, present) and the CPU used, to the developer log and
// to ~/.cache/1loop/bench.txt, and quits. Run it on a copy of the state
// (XDG_CONFIG_HOME), never on the one in use.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

type benchFrames struct {
	mu                    sync.Mutex
	sizes, paint, present []time.Duration
}

func cpuTime() time.Duration {
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

func percentiles(values []time.Duration) string {
	if len(values) == 0 {
		return "-"
	}
	sorted := append([]time.Duration{}, values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(p int) time.Duration { return sorted[min(len(sorted)-1, len(sorted)*p/100)].Round(10 * time.Microsecond) }
	return fmt.Sprintf("p50 %v p90 %v p99 %v max %v", at(50), at(90), at(99), sorted[len(sorted)-1].Round(10*time.Microsecond))
}

func (ui *desktopUI) bench(kind string) {
	time.Sleep(4 * time.Second) // the launch animation, the first layouts
	frames := &benchFrames{}
	switch kind {
	case "scroll":
		fyne.DoAndWait(func() {
			var longest *conversation
			for _, chat := range ui.chats {
				if longest == nil || len(chat.Items) > len(longest.Items) {
					longest = chat
				}
			}
			if longest != nil {
				ui.openChat(longest)
			}
			ui.scroll.ScrollToOffset(fyne.NewPos(0, 0))
		})
	case "idle":
		fyne.DoAndWait(func() { ui.window.Canvas().Focus(ui.prompt) })
	}
	time.Sleep(2 * time.Second)
	driver.FrameTimes = func(sizes, paint, present time.Duration) {
		frames.mu.Lock()
		frames.sizes = append(frames.sizes, sizes)
		frames.paint = append(frames.paint, paint)
		frames.present = append(frames.present, present)
		frames.mu.Unlock()
	}
	cpu, started := cpuTime(), time.Now()
	span := 10 * time.Second
	if kind == "scroll" {
		// Down then up at a steady 1500 px/s, one step per 8 ms.
		speed, offset := float32(1500), float32(0)
		tick := time.NewTicker(8 * time.Millisecond)
		last := time.Now()
		for now := range tick.C {
			if now.Sub(started) > span {
				break
			}
			step := speed * float32(now.Sub(last).Seconds())
			last = now
			fyne.DoAndWait(func() {
				// As a wheel does (Scrolled, which tells OnScrolled), without
				// the inertia: ScrollToOffset alone brings no item into view.
				bottom := ui.scroll.Content.Size().Height - ui.scroll.Size().Height
				if offset+step > bottom || offset+step < 0 {
					speed, step = -speed, -step
				}
				offset += step
				ui.scroll.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -step}})
			})
		}
		tick.Stop()
	} else {
		time.Sleep(span)
	}
	elapsed, used := time.Since(started), cpuTime()-cpu
	driver.FrameTimes = nil
	frames.mu.Lock()
	report := strings.Join([]string{
		fmt.Sprintf("bench %s: %d frames in %s (%.0f per second), CPU %.1f %% of a core", kind, len(frames.paint), elapsed.Round(time.Millisecond), float64(len(frames.paint))/elapsed.Seconds(), 100*used.Seconds()/elapsed.Seconds()),
		"  sizes   " + percentiles(frames.sizes),
		"  paint   " + percentiles(frames.paint),
		"  present " + percentiles(frames.present),
	}, "\n")
	frames.mu.Unlock()
	devf("perf", "%s", report)
	fmt.Println(report)
	if dir, err := os.UserCacheDir(); err == nil {
		os.WriteFile(filepath.Join(dir, "1loop", "bench.txt"), []byte(report+"\n"), 0600)
	}
	fyne.Do(ui.quit)
}
