// SPDX-License-Identifier: MPL-2.0

package ui

// Benchmarks of the interface, run by the application itself so they
// measure the real window (ONELOOP_BENCH, with -dev):
//
//	ONELOOP_BENCH=scroll  scrolls the longest chat down and up for 10 s
//	ONELOOP_BENCH=idle    waits 10 s with the composer focused (caret blinking)
//	ONELOOP_BENCH=browser a browser tab in the panel, on a page that animates
//	ONELOOP_BENCH=idleblur  waits with nothing focused (does anything redraw?)
//	ONELOOP_BENCH=stream  streams an answer with tool calls into the longest chat
//	ONELOOP_BENCH=type    types into the composer, 12 characters a second
//	ONELOOP_BENCH=sidebar scrolls the sidebar down and up
//	ONELOOP_BENCH=split   splits the window in two on the longest chat, scrolls
//	ONELOOP_BENCH=files   opens the Files panel and waits
//	ONELOOP_BENCH=startup reports the launch's milestones and memory, idle
//	ONELOOP_BENCH=soak    streams for ONELOOP_BENCH_SECONDS, heap every 30 s
//
// ONELOOP_BENCH_SECONDS sets the span (10 s by default). With
// ONELOOP_BENCH_PROFILE=<dir> the span is CPU-profiled (cpu.pprof), and
// heap.pprof, allocs.pprof and goroutine.txt are written after it. The
// report adds the Go heap, RSS, goroutines, GC cycles and allocations per
// frame. scripts/perf/ builds a synthetic state and runs every mode.
//
// Then it writes how many frames were drawn, how long each part of a frame
// took (sizes, paint, present) and the CPU used, to the developer log and
// to ~/.cache/1loop/bench.txt, and quits. Run it on a copy of the state
// (XDG_CONFIG_HOME), never on the one in use.

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

type benchFrames struct {
	mu                    sync.Mutex
	sizes, paint, present []time.Duration
}

func percentiles(values []time.Duration) string {
	if len(values) == 0 {
		return "-"
	}
	sorted := append([]time.Duration{}, values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(p int) time.Duration {
		return sorted[min(len(sorted)-1, len(sorted)*p/100)].Round(10 * time.Microsecond)
	}
	return fmt.Sprintf("p50 %v p90 %v p99 %v max %v", at(50), at(90), at(99), sorted[len(sorted)-1].Round(10*time.Microsecond))
}

// benchStarted is about when the process started (package initialisation);
// benchMarks are the launch's milestones, kept only while benchmarking.
var (
	benchStarted = time.Now()
	benchOn      = os.Getenv("ONELOOP_BENCH") != ""
	benchMu      sync.Mutex
	benchMarks   []string
)

// benchMark notes a milestone of the launch, when benchmarking.
func benchMark(what string) {
	if !benchOn {
		return
	}
	benchMu.Lock()
	benchMarks = append(benchMarks, fmt.Sprintf("  %-22s %7.1f ms", what, float64(time.Since(benchStarted).Microseconds())/1000))
	benchMu.Unlock()
}

// benchMemory is the process's memory now, in one line.
func benchMemory() string {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	rss := "?"
	if status, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				rss = strings.Join(strings.Fields(line)[1:], " ")
			}
		}
	}
	return fmt.Sprintf("heap %.1f MB (sys %.1f MB), RSS %s, %d goroutines, %d GC cycles",
		float64(stats.HeapAlloc)/1e6, float64(stats.Sys)/1e6, rss, runtime.NumGoroutine(), stats.NumGC)
}

func (ui *desktopUI) longestChat() *conversation {
	var longest *conversation
	for _, chat := range ui.chats {
		if longest == nil || len(chat.Items) > len(longest.Items) {
			longest = chat
		}
	}
	return longest
}

// sideScroller is the sidebar's scrolling list.
func (ui *desktopUI) sideScroller() *kineticScroll {
	if box, ok := ui.sideApp.(*fyne.Container); ok {
		for _, object := range box.Objects {
			if scroll, ok := object.(*kineticScroll); ok {
				return scroll
			}
		}
	}
	return nil
}

// benchScroll scrolls down and up at a steady 1500 px/s, one step per 8 ms.
func benchScroll(scroll *kineticScroll, until time.Time) {
	if scroll == nil {
		time.Sleep(time.Until(until))
		return
	}
	speed, offset := float32(1500), float32(0)
	tick := time.NewTicker(8 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	for now := range tick.C {
		if now.After(until) {
			return
		}
		step := speed * float32(now.Sub(last).Seconds())
		last = now
		fyne.DoAndWait(func() {
			// As a wheel does (Scrolled, which tells OnScrolled), without
			// the inertia: ScrollToOffset alone brings no item into view.
			bottom := scroll.Content.Size().Height - scroll.Size().Height
			if offset+step > bottom || offset+step < 0 {
				speed, step = -speed, -step
			}
			offset += step
			scroll.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -step}})
		})
	}
}

var benchWords = strings.Fields("the layout of each item is measured again when its width changes and the " +
	"transcript keeps only what is near the view alive so a long chat should cost no more than a short one " +
	"`code` **bold** [link](https://example.com) and some more words to wrap")

// benchStream writes an answer the way a model does: a few words every
// 16 ms, a code block now and then, a tool call every 2 s, a new answer
// every 20 s.
func (ui *desktopUI) benchStream(until time.Time, sample func()) {
	var chat *conversation
	fyne.DoAndWait(func() {
		chat = ui.current
		if chat == nil {
			chat = ui.longestChat()
		}
	})
	if chat == nil {
		time.Sleep(time.Until(until))
		return
	}
	current := ui.newTurn(chat, chat.Config)
	random := rand.New(rand.NewSource(1))
	started, lastTool, lastAnswer, lastSample := time.Now(), time.Now(), time.Now(), time.Now()
	calls := 0
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	for now := range tick.C {
		if now.After(until) {
			break
		}
		var events []uiEvent
		var text strings.Builder
		for range 3 + random.Intn(5) {
			text.WriteString(benchWords[random.Intn(len(benchWords))] + " ")
		}
		if random.Intn(40) == 0 {
			text.WriteString("\n\n```go\nfunc measure(width float32) fyne.Size {\n\treturn fyne.NewSize(width, 42)\n}\n```\n\n")
		} else if random.Intn(15) == 0 {
			text.WriteString("\n\n")
		}
		events = append(events, uiEvent{"delta", text.String()})
		if now.Sub(lastTool) > 2*time.Second {
			lastTool = now
			calls++
			id := "bench-" + strconv.Itoa(calls)
			outcome := agent.ToolOutcome{ID: id, Name: "shell", Args: `{"command":"go test ./internal/ui -run TestBench` + strconv.Itoa(calls) + `"}`}
			events = append(events, uiEvent{"message_end", nil}, uiEvent{"tool", outcome})
			outcome.Output, outcome.Elapsed = strings.Repeat("ok  \t1loop/internal/ui\t0.42s\n", 20), 420*time.Millisecond
			events = append(events, uiEvent{"tool_result", outcome})
		}
		if now.Sub(lastAnswer) > 20*time.Second {
			lastAnswer = now
			events = append(events, uiEvent{"message_end", nil})
		}
		fyne.DoAndWait(func() {
			for _, event := range events {
				current.handle(event)
			}
		})
		if sample != nil && now.Sub(lastSample) > 30*time.Second {
			lastSample = now
			line := fmt.Sprintf("  soak %4.0f s: %d items, %s", now.Sub(started).Seconds(), len(chat.Items), benchMemory())
			platform.Devf("perf", "%s", line)
			fmt.Println(line)
			sample()
		}
	}
	fyne.DoAndWait(func() { current.finish() })
}

func (ui *desktopUI) bench(kind string) {
	benchMark("bench goroutine")
	firstFrame := make(chan struct{})
	var once sync.Once
	driver.FrameTimes = func(time.Duration, time.Duration, time.Duration) {
		once.Do(func() { benchMark("first frame drawn"); close(firstFrame) })
	}
	<-firstFrame
	fyne.DoAndWait(func() {}) // the interface thread answers
	benchMark("interactive")
	afterLaunch := benchMemory()
	time.Sleep(4 * time.Second) // the launch animation, the first layouts
	frames := &benchFrames{}
	switch kind {
	case "scroll", "stream", "soak":
		fyne.DoAndWait(func() {
			if longest := ui.longestChat(); longest != nil {
				opened := time.Now()
				ui.openChat(longest)
				benchMark(fmt.Sprintf("open %d items: %.0f ms", len(longest.Items), float64(time.Since(opened).Microseconds())/1000))
			}
			ui.scroll.ScrollToOffset(fyne.NewPos(0, 0))
		})
		time.Sleep(3 * time.Second) // the chat's items arrive over a few frames (chat_loading.go)
		if kind != "scroll" {
			fyne.DoAndWait(func() {
				ui.followTail = true
				ui.scroll.ScrollToOffset(fyne.NewPos(0, ui.scroll.Content.Size().Height))
			})
		}
	case "split":
		fyne.DoAndWait(func() {
			if longest := ui.longestChat(); longest != nil {
				ui.openChat(longest)
				ui.splitPane(longest, false)
			}
		})
		time.Sleep(3 * time.Second)
	case "idle", "type":
		fyne.DoAndWait(func() { ui.window.Canvas().Focus(ui.prompt) })
	case "idleblur":
		fyne.DoAndWait(func() { ui.window.Canvas().Unfocus() })
	case "files":
		fyne.DoAndWait(func() { ui.panel.openFiles() })
	case "browser":
		fyne.DoAndWait(func() { ui.panel.openBrowser() })
		time.Sleep(2 * time.Second)
		page := `data:text/html,<body style="margin:0;font:28px sans-serif"><div id=b style="width:200px;height:200px;background:orange"></div><p>` +
			strings.Repeat("Lorem ipsum dolor sit amet. ", 60) + `</p><script>let t=0;(function f(){t++;b.style.transform="translateX("+(200+180*Math.sin(t/20))+"px)";requestAnimationFrame(f)})()</script>`
		fyne.DoAndWait(func() {
			tab := ui.panel.tabs[len(ui.panel.tabs)-1]
			tab.browser.View.(*browserView).address.SetText(page)
			tab.browser.View.(*browserView).address.OnSubmitted(page)
		})
	}
	time.Sleep(2 * time.Second)
	span := 10 * time.Second
	if seconds, err := strconv.Atoi(os.Getenv("ONELOOP_BENCH_SECONDS")); err == nil && seconds > 0 {
		span = time.Duration(seconds) * time.Second
	}
	profileDir := os.Getenv("ONELOOP_BENCH_PROFILE")
	if profileDir != "" {
		os.MkdirAll(profileDir, 0o755)
		if file, err := os.Create(filepath.Join(profileDir, "cpu.pprof")); err == nil {
			pprof.StartCPUProfile(file)
			defer file.Close()
		}
	}
	before := benchMemory()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	driver.FrameTimes = func(sizes, paint, present time.Duration) {
		frames.mu.Lock()
		frames.sizes = append(frames.sizes, sizes)
		frames.paint = append(frames.paint, paint)
		frames.present = append(frames.present, present)
		frames.mu.Unlock()
	}
	cpu, started := platform.CpuTime(), time.Now()
	until := started.Add(span)
	var reloads []time.Duration // theme: read, themeChanged, setAccent of each round
	switch kind {
	case "scroll", "split":
		benchScroll(ui.scroll, until)
	case "sidebar":
		var side *kineticScroll
		fyne.DoAndWait(func() { side = ui.sideScroller() })
		benchScroll(side, until)
	case "stream":
		ui.benchStream(until, nil)
	case "soak":
		ui.benchStream(until, func() {})
	case "type":
		text := []rune("The quick brown fox jumps over the lazy dog, again and again. ")
		for i := 0; time.Now().Before(until); i++ {
			r := text[i%len(text)]
			fyne.DoAndWait(func() {
				if focused := ui.window.Canvas().Focused(); focused != nil {
					focused.TypedRune(r)
				}
			})
			time.Sleep(83 * time.Millisecond)
		}
	case "theme":
		// A theme file saved again and again, as while editing it: read
		// back, the palette rebuilt and the whole window refreshed.
		// Timed as watchThemes does it, then with the accent switched.
		accents := []string{"#ff8800", "#3388ff"}
		for i := 0; time.Now().Before(until); i++ {
			fyne.DoAndWait(func() {
				at := time.Now()
				loadThemeFiles()
				loaded := time.Now()
				ui.themeChanged()
				changed := time.Now()
				ui.setAccent(accents[i%2])
				reloads = append(reloads, loaded.Sub(at), changed.Sub(loaded), time.Since(changed))
			})
			time.Sleep(250 * time.Millisecond)
		}
	default:
		time.Sleep(span)
	}
	elapsed, used := time.Since(started), platform.CpuTime()-cpu
	driver.FrameTimes = nil
	if profileDir != "" {
		pprof.StopCPUProfile()
	}
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	frames.mu.Lock()
	count := max(1, len(frames.paint))
	benchMu.Lock()
	marks := strings.Join(benchMarks, "\n")
	benchMu.Unlock()
	report := strings.Join([]string{
		fmt.Sprintf("bench %s: %d frames in %s (%.0f per second), CPU %.1f %% of a core", kind, len(frames.paint), elapsed.Round(time.Millisecond), float64(len(frames.paint))/elapsed.Seconds(), 100*used.Seconds()/elapsed.Seconds()),
		"  sizes   " + percentiles(frames.sizes),
		"  paint   " + percentiles(frames.paint),
		"  present " + percentiles(frames.present),
		"  frame   " + percentiles(sumFrames(frames)),
		fmt.Sprintf("  allocs  %d per frame, %.1f KB per frame, %d GC cycles in the span", (memAfter.Mallocs-memBefore.Mallocs)/uint64(count), float64(memAfter.TotalAlloc-memBefore.TotalAlloc)/1024/float64(count), memAfter.NumGC-memBefore.NumGC),
		themeTimes(reloads),
		"  memory after launch  " + afterLaunch,
		"  memory before span   " + before,
		"  memory after span    " + benchMemory(),
		fmt.Sprintf("  launch (since package init at unix %d ms):", benchStarted.UnixMilli()),
		marks,
	}, "\n")
	frames.mu.Unlock()
	if profileDir != "" {
		for _, name := range []string{"heap", "allocs"} {
			if file, err := os.Create(filepath.Join(profileDir, name+".pprof")); err == nil {
				pprof.Lookup(name).WriteTo(file, 0)
				file.Close()
			}
		}
		if file, err := os.Create(filepath.Join(profileDir, "goroutine.txt")); err == nil {
			pprof.Lookup("goroutine").WriteTo(file, 1)
			file.Close()
		}
	}
	platform.Devf("perf", "%s", report)
	fmt.Println(report)
	if dir, err := os.UserCacheDir(); err == nil {
		os.WriteFile(filepath.Join(dir, "1loop", "bench.txt"), []byte(report+"\n"), 0600)
	}
	fyne.Do(ui.quit)
}

// sumFrames is each frame's whole time.
func sumFrames(frames *benchFrames) []time.Duration {
	total := make([]time.Duration, len(frames.paint))
	for i := range total {
		total[i] = frames.sizes[i] + frames.paint[i] + frames.present[i]
	}
	return total
}

// themeTimes sums up the theme mode's reloads.
func themeTimes(reloads []time.Duration) string {
	if len(reloads) == 0 {
		return "  theme   -"
	}
	var read, changed, accent []time.Duration
	for i := 0; i+2 < len(reloads); i += 3 {
		read, changed, accent = append(read, reloads[i]), append(changed, reloads[i+1]), append(accent, reloads[i+2])
	}
	return "  theme read     " + percentiles(read) + "\n  themeChanged   " + percentiles(changed) + "\n  setAccent      " + percentiles(accent)
}
