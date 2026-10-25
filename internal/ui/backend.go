package ui

import (
	"net"
	"path/filepath"
	"strings"
	"time"
)

// Linux builds 1loop twice, because GLFW picks X11 or Wayland when it is
// compiled: 1loop (X11, also used through XWayland) and 1loop-wayland. Each
// one hands over to the other when the session calls for it (switchBackend),
// following displayBackend.
const (
	backendWayland = "wayland"
	backendX11     = "x11"
)

// displayBackend chooses the display 1loop's window goes to:
//   - ONELOOP_BACKEND=x11 or wayland, when the user asks for one;
//   - native Wayland when WAYLAND_DISPLAY names a compositor that answers:
//     a variable left over from another session (an ssh or tmux reattach)
//     points at a socket that is gone, and GLFW would stop on it;
//   - X11 (or XWayland) when DISPLAY is set;
//   - "" when there is no display at all.
func displayBackend(getenv func(string) string, answers func(socket string) bool) string {
	switch forced := getenv("ONELOOP_BACKEND"); forced {
	case backendWayland, backendX11:
		return forced
	}
	if name := getenv("WAYLAND_DISPLAY"); name != "" {
		socket := name
		if !filepath.IsAbs(socket) {
			socket = filepath.Join(getenv("XDG_RUNTIME_DIR"), name)
		}
		if answers(socket) {
			return backendWayland
		}
	}
	if getenv("DISPLAY") != "" {
		return backendX11
	}
	return ""
}

// waylandAnswers tells whether a compositor listens on socket.
func waylandAnswers(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// siblingBinary is the build for backend next to exe: 1loop-wayland beside
// 1loop, and the other way round. "" when exe is not named that way.
func siblingBinary(exe, backend string) string {
	plain := strings.TrimSuffix(exe, "-wayland")
	if backend == backendWayland {
		return plain + "-wayland"
	}
	if plain == exe {
		return ""
	}
	return plain
}
