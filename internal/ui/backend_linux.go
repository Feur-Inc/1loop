//go:build linux && cgo

package ui

import (
	"fmt"
	"os"
	"syscall"
)

// switchBackend hands over to the build for the display displayBackend
// chooses, when that is not this one: 1loop-wayland renders text at the
// screen's real resolution where XWayland would upscale it, and the X11
// build still runs when the Wayland compositor is gone. Without the other
// build beside it, this one tries its own display. With no display at all
// it stops here with a message, instead of a GLFW panic.
func switchBackend() {
	want := displayBackend(os.Getenv, waylandAnswers)
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
