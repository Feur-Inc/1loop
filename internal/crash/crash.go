// SPDX-License-Identifier: MPL-2.0

// Package crash notices that 1loop did not end cleanly and turns what it
// left into an anonymous report the user may file on GitHub. Nothing is
// ever sent by 1loop itself.
//
// Two files in the config folder do it: crash.log, where the Go runtime
// writes a fatal panic's trace (debug.SetCrashOutput), and running.json,
// written at start, touched every minute and removed on every clean quit.
// A marker left behind without a trace is a crash in native code or a
// kill (out of memory, SIGKILL).
package crash

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

// MaxAge is how old a crash may be and still be offered.
const MaxAge = 5 * 24 * time.Hour

// logLines is how many of the developer log's last lines are kept.
const logLines = 40

type marker struct {
	Env     Env       `json:"env"`
	Started time.Time `json:"started"`
	DevLog  string    `json:"devLog,omitempty"` // the developer log's file, when it was on
}

var (
	mu      sync.Mutex
	current string // this run's marker, "" once ended cleanly
	beat    *time.Ticker
)

// Start watches this run and returns the previous one's crash, if it
// ended in one not long ago. devLog is the developer log's file when this
// run writes one. Call it before the developer log starts, which moves the
// previous session's log away.
func Start(dir string, env Env, devLog string) *Crash {
	if os.MkdirAll(dir, 0700) != nil {
		return nil
	}
	last := previous(dir)
	output := filepath.Join(dir, "crash.log")
	if file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
		debug.SetCrashOutput(file, debug.CrashOptions{})
		file.Close() // SetCrashOutput keeps its own copy
	}
	path := filepath.Join(dir, "running.json")
	data, _ := json.Marshal(marker{Env: env, Started: time.Now(), DevLog: devLog})
	if os.WriteFile(path, data, 0600) == nil {
		mu.Lock()
		current = path
		beat = time.NewTicker(time.Minute)
		ticker := beat
		mu.Unlock()
		go func() {
			for now := range ticker.C {
				os.Chtimes(path, now, now)
			}
		}()
	}
	return last
}

// previous reads what the last run left, and clears it: each crash is
// offered once.
func previous(dir string) *Crash {
	trace := filepath.Join(dir, "crash.log")
	path := filepath.Join(dir, "running.json")
	defer os.Remove(path)
	var crash Crash
	if data, err := os.ReadFile(trace); err == nil && len(data) > 0 {
		crash.Trace = string(data)
		if info, err := os.Stat(trace); err == nil {
			crash.Ended = info.ModTime()
		}
	}
	data, err := os.ReadFile(path)
	if err != nil && crash.Trace == "" {
		return nil // ended cleanly
	}
	var mark marker
	if json.Unmarshal(data, &mark) == nil {
		crash.Env, crash.Started = mark.Env, mark.Started
		if crash.Ended.IsZero() {
			if info, err := os.Stat(path); err == nil {
				crash.Ended = info.ModTime()
			}
		}
		if mark.DevLog != "" {
			crash.Log = lastLines(mark.DevLog, logLines)
		}
	}
	if crash.Ended.IsZero() || time.Since(crash.Ended) > MaxAge {
		return nil
	}
	return &crash
}

// lastLines reads a file's last lines.
func lastLines(path string, count int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > count {
			lines = lines[1:]
		}
	}
	return lines
}

// Clean says this run ends as it should: there is nothing to report.
// Every clean quit calls it, last.
func Clean() {
	mu.Lock()
	defer mu.Unlock()
	if beat != nil {
		beat.Stop()
	}
	if current != "" {
		os.Remove(current)
		current = ""
	}
}
