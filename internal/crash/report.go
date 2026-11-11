// SPDX-License-Identifier: MPL-2.0

package crash

// The report: what the dialog shows and what the GitHub link carries.

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"1loop/internal/update"
)

// NewIssue is where a report is filed, by the user, in their browser.
const NewIssue = "https://github.com/Feur-Inc/1loop/issues/new"

// MaxURL is how long a link GitHub (and browsers) still open whole.
const MaxURL = 8000

// Env is what the report says about the program and the computer it ran
// on: nothing that tells one user from another.
type Env struct {
	App     string `json:"app"` // "desktop" or "server"
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Install string `json:"install"`
	OS      string `json:"os"`
	Desktop string `json:"desktop,omitempty"` // the desktop environment
	Session string `json:"session,omitempty"` // wayland or x11
	Backend string `json:"backend,omitempty"` // the build that ran: Wayland or X11
	Go      string `json:"go"`
	Arch    string `json:"arch"`
}

// Environment is this process's Env.
func Environment(app string) Env {
	env := Env{App: app, Version: update.Version, Install: installKind(), OS: systemName(),
		Desktop: os.Getenv("XDG_CURRENT_DESKTOP"), Session: os.Getenv("XDG_SESSION_TYPE"),
		Go: runtime.Version(), Arch: runtime.GOARCH}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				env.Commit = setting.Value
				if len(env.Commit) > 7 {
					env.Commit = env.Commit[:7]
				}
			case "vcs.modified":
				if setting.Value == "true" {
					env.Commit += "+changes"
				}
			}
		}
	}
	return env
}

func installKind() string {
	switch {
	case update.Packaged != "":
		return update.Packaged
	case os.Getenv("FLATPAK_ID") != "":
		return "Flatpak"
	case os.Getenv("APPIMAGE") != "":
		return "AppImage"
	case update.Version == "dev":
		return "built from source"
	case runtime.GOOS == "windows":
		return "Windows installer"
	}
	return "release archive"
}

// systemName is the system and its version: "Linux 6.9.1 (Fedora Linux
// 40)". The distribution's name, never the computer's.
func systemName() string {
	name := runtime.GOOS
	switch runtime.GOOS {
	case "linux":
		name = "Linux"
		if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			name += " " + strings.TrimSpace(string(data))
		}
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if value, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
					name += " (" + strings.Trim(value, `"'`) + ")"
				}
			}
		}
	case "windows":
		name = "Windows"
	case "darwin":
		name = "macOS"
	}
	return name
}

// Crash is one run that did not end cleanly.
type Crash struct {
	Env     Env       `json:"env"`
	Started time.Time `json:"started"`
	// Ended is when the trace was written, or the run's last sign of life
	// when there is none.
	Ended time.Time `json:"ended"`
	Trace string    `json:"trace,omitempty"` // as the runtime wrote it
	Log   []string  `json:"log,omitempty"`   // the developer log's last lines, unscrubbed
}

// Report is a crash made fit to post.
type Report struct {
	Title string
	head  string
	trace []string
	log   []string
}

// NewReport scrubs a crash into its report.
func NewReport(crash Crash, scrubber Scrubber) Report {
	env := crash.Env
	var head strings.Builder
	version := env.Version
	if env.Commit != "" {
		version += " (" + env.Commit + ")"
	}
	app := env.App
	if app == "" {
		app = "desktop"
	}
	fmt.Fprintf(&head, "- **1loop:** %s %s, %s\n", app, version, env.Install)
	fmt.Fprintf(&head, "- **System:** %s, %s\n", scrubber.Text(env.OS), env.Arch)
	if env.Desktop != "" || env.Session != "" || env.Backend != "" {
		display := []string{}
		for _, part := range []string{env.Desktop, env.Session} {
			if part != "" {
				display = append(display, part)
			}
		}
		if env.Backend != "" {
			display = append(display, "the "+env.Backend+" build")
		}
		fmt.Fprintf(&head, "- **Display:** %s\n", strings.Join(display, ", "))
	}
	fmt.Fprintf(&head, "- **Go:** %s\n", env.Go)
	if !crash.Started.IsZero() && crash.Ended.After(crash.Started) {
		fmt.Fprintf(&head, "- **Ran for:** %s\n", duration(crash.Ended.Sub(crash.Started)))
	}
	report := Report{head: head.String()}
	trace := scrubber.Trace(crash.Trace)
	if trace == "" {
		report.Title = "Crash: 1loop closed without a trace"
		report.head += "\nNo Go trace: a native crash, or killed by the system.\n"
	} else {
		report.trace = strings.Split(trace, "\n")
		report.Title = "Crash: " + title(report.trace)
	}
	if len(crash.Log) > 0 {
		report.log = scrubber.Log(crash.Log)
	}
	return report
}

// title is the trace's first line that says what happened.
func title(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "goroutine ") {
			continue
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "panic: "), "fatal error: ")
		if runes := []rune(line); len(runes) > 90 {
			line = string(runes[:90]) + "…"
		}
		return line
	}
	return "1loop closed unexpectedly"
}

func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// HasLog says whether the developer log's lines can be added.
func (r Report) HasLog() bool { return len(r.log) > 0 }

// Body is the issue's text, with the developer log's lines or not, its
// trace cut to traceLines (all of it when negative).
func (r Report) Body(withLog bool, traceLines int) string {
	var body strings.Builder
	body.WriteString(r.head)
	if len(r.trace) > 0 {
		lines := r.trace
		cut := 0
		if traceLines >= 0 && traceLines < len(lines) {
			cut = len(lines) - traceLines
			lines = lines[:traceLines]
		}
		body.WriteString("\n### Trace\n\n```text\n" + strings.Join(lines, "\n") + "\n```\n")
		if cut > 0 {
			fmt.Fprintf(&body, "\n_%d lines cut._\n", cut)
		}
	}
	if withLog && len(r.log) > 0 {
		body.WriteString("\n### Developer log\n\n```text\n" + strings.Join(r.log, "\n") + "\n```\n")
	}
	return body.String()
}

// Text is the full report, as Copy report copies it.
func (r Report) Text(withLog bool) string {
	return "# " + r.Title + "\n\n" + r.Body(withLog, -1)
}

// URL is the link to a new issue holding the report, and whether its
// trace had to be cut to fit (the full one is copied instead).
func (r Report) URL(withLog bool) (link string, cut bool) {
	build := func(lines int) string {
		return NewIssue + "?" + url.Values{"title": {r.Title}, "body": {r.Body(withLog, lines)}, "labels": {"bug"}}.Encode()
	}
	link = build(-1)
	for lines := len(r.trace) - 1; len(link) > MaxURL && lines >= 0; lines-- {
		link, cut = build(lines), true
	}
	if len(link) > MaxURL && withLog {
		// Even without a trace the log does not fit: without the log.
		link, cut = NewIssue+"?"+url.Values{"title": {r.Title}, "body": {r.Body(false, 0)}, "labels": {"bug"}}.Encode(), true
	}
	return link, cut
}
