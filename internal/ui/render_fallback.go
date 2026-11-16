// SPDX-License-Identifier: MPL-2.0

package ui

// 1loop draws with OpenGL. When the window cannot be made, it tries every
// other way it has, by itself and without asking anything: on Windows its
// own Mesa, which draws on the CPU (software_gl_windows.go); on Linux the
// system's Mesa on the CPU, then X11 instead of Wayland
// (software_gl_linux.go). Only when they all failed does it say so, once,
// with what it tried and what the user can do (renderFailureText). Each
// attempt is noted in renderer.log, in 1loop's cache folder.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/platform"
)

// triedVariable carries, from one attempt to the next process, what was
// tried and how it failed: label, unit separator, failure, record separator.
const triedVariable = "ONELOOP_GL_TRIED"

// showFirstWindow shows the main window, or says why it could not be made:
// GLFW panics when it finds no OpenGL configuration (Linux), or the window
// is left without a handle (Windows).
func showFirstWindow(window fyne.Window) (failure error) {
	defer func() {
		if caught := recover(); caught != nil {
			failure = fmt.Errorf("%v", caught)
		}
	}()
	window.Show()
	if windowMissing(window) {
		return errors.New("the window could not be made: OpenGL 2.1 is not available")
	}
	return nil
}

// attempt is one way of drawing that failed.
type attempt struct{ label, failure string }

// triedSoFar are the attempts of the processes before this one.
func triedSoFar() []attempt {
	var out []attempt
	for _, record := range strings.Split(os.Getenv(triedVariable), "\x1e") {
		if label, failure, ok := strings.Cut(record, "\x1f"); ok {
			out = append(out, attempt{label, failure})
		}
	}
	return out
}

// noteTried adds this attempt for the next process, which inherits it.
func noteTried(label string, failure error) {
	text := strings.TrimSpace(strings.SplitN(failure.Error(), "\n", 2)[0])
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	previous := os.Getenv(triedVariable)
	if previous != "" {
		previous += "\x1e"
	}
	os.Setenv(triedVariable, previous+label+"\x1f"+text)
	rendererLogf("%s failed: %s", label, text)
}

// rendererLogPath is renderer.log in 1loop's cache folder.
func rendererLogPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "1loop", "renderer.log")
}

// rendererLogf notes how 1loop draws, in renderer.log (kept short) and the
// developer log.
func rendererLogf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	platform.Devf("app", "renderer: %s", line)
	path := rendererLogPath()
	if path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	if info, err := os.Stat(path); err == nil && info.Size() > 256<<10 {
		os.Remove(path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s pid %d: %s\n", time.Now().Format("2006-01-02 15:04:05"), os.Getpid(), line)
}

// renderFailureText is the last message: what happened, what 1loop tried,
// what the user can do, and where the details are.
func renderFailureText(tried []attempt, advice []string) string {
	var text strings.Builder
	text.WriteString("1loop draws its window with OpenGL, and none of the ways it has to draw worked on this computer, so it cannot start.\n\n")
	text.WriteString("What 1loop tried, by itself:\n")
	for i, step := range tried {
		fmt.Fprintf(&text, "%d. %s: %s\n", i+1, step.label, step.failure)
	}
	text.WriteString("\nWhat you can do:\n")
	for _, line := range advice {
		text.WriteString("• " + line + "\n")
	}
	if path := rendererLogPath(); path != "" {
		text.WriteString("\nDetails: " + path)
	}
	return text.String()
}

// renderFailureTitle titles the last message.
const renderFailureTitle = "1loop cannot open its window"

// forgetTried keeps the attempts out of what 1loop starts (terminals,
// agent commands), once a way of drawing worked.
func forgetTried() {
	if tried := triedSoFar(); len(tried) > 0 {
		rendererLogf("drawing after %d failed attempt(s)", len(tried))
	}
	os.Unsetenv(triedVariable)
}
