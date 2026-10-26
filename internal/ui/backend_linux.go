//go:build linux && cgo

package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// switchBackend hands over to the build for the display displayBackend
// chooses, when that is not this one: 1loop-wayland renders text at the
// screen's real resolution where XWayland would upscale it, and the X11
// build still runs when the Wayland compositor is gone. Without the other
// build beside it, this one tries its own display. With no display at all
// it stops here with a message, instead of a GLFW panic.
func switchBackend() {
	// A probe must never hand over to X11 and start the actual application
	// if its Wayland socket disappears between the parent's check and exec.
	if os.Getenv("ONELOOP_DISPLAY_PROBE") == "1" {
		if builtBackend == backendWayland {
			backendReady()
		}
		os.Exit(1)
	}
	want := displayBackend(os.Getenv, waylandAnswers)
	// A failed Wayland initialization gets one fresh X11 process. Consume
	// the marker here so terminals and agent commands keep their session.
	fallback := os.Getenv("ONELOOP_X11_FALLBACK") == "1"
	os.Unsetenv("ONELOOP_X11_FALLBACK")
	if fallback {
		want = backendX11
	}
	if want == "" {
		fmt.Fprintln(os.Stderr, "1loop needs a graphical session: neither WAYLAND_DISPLAY nor DISPLAY names a display that answers.")
		os.Exit(1)
	}
	if want != builtBackend {
		if exe, err := os.Executable(); err == nil {
			if target := siblingBinary(exe, want); target != "" {
				if _, err := os.Stat(target); err == nil {
					syscall.Exec(target, append([]string{target}, os.Args[1:]...), os.Environ())
				}
			}
		}
	}
	backendReady()
}

// fallbackDisplay checks native initialization in a disposable process;
// only a failed probe, never an application exit, selects X11.
func fallbackDisplay(initialize func() error) {
	if os.Getenv("ONELOOP_DISPLAY_PROBE") == "1" {
		os.Unsetenv("ONELOOP_DISPLAY_PROBE")
		if err := initialize(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if builtBackend != backendWayland || os.Getenv("ONELOOP_BACKEND") == backendWayland || os.Getenv("DISPLAY") == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	target := siblingBinary(exe, backendX11)
	if info, err := os.Stat(target); err != nil || info.Mode()&0111 == 0 {
		return
	}
	// A native driver can crash inside cgo, beyond Go's recover. Keep that
	// initialization in a short-lived process, before any app state is loaded.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, exe)
	probe.Env = append(os.Environ(), "ONELOOP_DISPLAY_PROBE=1")
	probe.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	var diagnostics bytes.Buffer
	probe.Stderr = &diagnostics
	if err := probe.Run(); err != nil {
		detail := strings.SplitN(strings.TrimSpace(diagnostics.String()), "\n", 2)[0]
		fmt.Fprintf(os.Stderr, "1loop: Wayland initialization failed (%v: %s); trying X11.\n", err, detail)
		os.Setenv("ONELOOP_X11_FALLBACK", "1")
		if err := syscall.Exec(target, append([]string{target}, os.Args[1:]...), os.Environ()); err != nil {
			fmt.Fprintln(os.Stderr, "1loop: cannot start the X11 fallback:", err)
		}
		os.Exit(1)
	}
}
