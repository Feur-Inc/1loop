// Package platform holds what depends on the operating system (processes,
// shells, signals) and the developer log.
package platform

// Developer log (-dev, or ONELOOP_DEV=1). It records what 1loop does and how
// long the interface takes to do it, to the terminal and to a file that
// survives the session, so a freeze or a slowdown can be explained after
// the fact:
//
//	~/.cache/1loop/dev.log        this session
//	~/.cache/1loop/dev.log.1      the one before
//	~/.cache/1loop/cpu.pprof      a CPU profile, after kill -USR2
//
// Every line carries the time and an area: app (lifecycle), ui (what the
// user did), run (an agent loop's events), claude (the Claude Code
// process), tool, perf (frames, stalls, slow operations), frame (the
// interface thread's safety nets), remote (the phone).
//
// A watchdog follows the interface thread: when it stops turning it writes
// every goroutine's stack, which says where it is stuck. `kill -USR1 <pid>`
// writes the same dump on demand.
//
// Off, devf costs one comparison; nothing is opened or written.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type devLog struct {
	mu   sync.Mutex
	out  io.Writer
	path string
}

var Dev *devLog // nil unless the developer log is on

// DevWanted is the -dev flag; the log starts once the right binary runs
// (bin/1loop may first hand over to bin/1loop-wayland).
var DevWanted bool

// FrameBeat is when the interface thread last turned (see frame), in
// nanoseconds since the Unix epoch.
var FrameBeat atomic.Int64

func StartDevLog() {
	out := io.Writer(os.Stderr)
	path := ""
	if dir, err := os.UserCacheDir(); err == nil {
		dir = filepath.Join(dir, "1loop")
		path = filepath.Join(dir, "dev.log")
		if os.MkdirAll(dir, 0700) == nil {
			os.Rename(path, path+".1")
			if file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
				out = io.MultiWriter(os.Stderr, file)
			} else {
				path = ""
			}
		}
	}
	Dev = &devLog{out: out, path: path}
	revision, modified := "unknown", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
				if len(revision) > 7 {
					revision = revision[:7]
				}
			case "vcs.modified":
				if setting.Value == "true" {
					modified = " + uncommitted changes"
				}
			}
		}
	}
	exe, _ := os.Executable()
	Devf("app", "1loop %s%s, %s, pid %d, %s", revision, modified, runtime.Version(), os.Getpid(), exe)
	Devf("app", "session %s, display %q, log file %s", os.Getenv("XDG_SESSION_TYPE"), os.Getenv("WAYLAND_DISPLAY")+os.Getenv("DISPLAY"), path)
	go watchInterface()
	watchDevSignals()
}

// devProfile records where the CPU goes for a while, beside the log
// (cpu.pprof; read it with go tool pprof). Asked with SIGUSR2.
func devProfile(span time.Duration) {
	if Dev.path == "" {
		return
	}
	path := filepath.Join(filepath.Dir(Dev.path), "cpu.pprof")
	file, err := os.Create(path)
	if err != nil {
		return
	}
	defer file.Close()
	if pprof.StartCPUProfile(file) != nil {
		return
	}
	Devf("perf", "CPU profile for %s (SIGUSR2)", span)
	time.Sleep(span)
	pprof.StopCPUProfile()
	Devf("perf", "CPU profile written to %s", path)
}

// Devf writes one line of the developer log.
func Devf(area, format string, args ...any) {
	if Dev == nil {
		return
	}
	line := fmt.Sprintf("%s %-6s %s\n", time.Now().Format("15:04:05.000"), area, fmt.Sprintf(format, args...))
	Dev.mu.Lock()
	io.WriteString(Dev.out, line)
	Dev.mu.Unlock()
}

// DevSlow logs an operation that took longer than its budget; use as
//
//	defer devSlow("open chat", 8*time.Millisecond, time.Now())
func DevSlow(what string, budget time.Duration, started time.Time) {
	if Dev == nil {
		return
	}
	if took := time.Since(started); took > budget {
		Devf("perf", "slow: %s took %s", what, took.Round(100*time.Microsecond))
	}
}

// DevText shortens a text for one log line.
func DevText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

func devStacks(why string) {
	if Dev == nil {
		return
	}
	buffer := make([]byte, 4<<20)
	buffer = buffer[:runtime.Stack(buffer, true)]
	Devf("perf", "%s:\n%s", why, buffer)
}

// watchInterface reports when the interface thread stops turning: once
// with every stack while it is stuck, then how long it lasted.
func watchInterface() {
	const limit = 1500 * time.Millisecond
	stuck := false
	for range time.Tick(250 * time.Millisecond) {
		beat := FrameBeat.Load()
		if beat == 0 {
			continue // not started yet
		}
		silent := time.Since(time.Unix(0, beat))
		switch {
		case silent > limit && !stuck:
			stuck = true
			devStacks(fmt.Sprintf("the interface thread has not turned for %s; goroutines", silent.Round(time.Millisecond)))
		case silent < limit && stuck:
			stuck = false
			Devf("perf", "the interface thread turns again")
		}
	}
}
