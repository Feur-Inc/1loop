package ui

import (
	"net"
	"path/filepath"
	"testing"
)

func TestDisplayBackend(t *testing.T) {
	runtime := t.TempDir()
	live := filepath.Join(runtime, "wayland-1")
	listener, err := net.Listen("unix", live)
	if err != nil {
		t.Skip("no unix sockets here:", err)
	}
	defer listener.Close()
	for _, test := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"native Wayland", map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}, backendWayland},
		{"absolute socket", map[string]string{"WAYLAND_DISPLAY": live}, backendWayland},
		{"stale Wayland variable, X11 left", map[string]string{"WAYLAND_DISPLAY": "wayland-9", "DISPLAY": ":0"}, backendX11},
		{"stale Wayland variable alone", map[string]string{"WAYLAND_DISPLAY": "wayland-9"}, ""},
		{"X11 session", map[string]string{"DISPLAY": ":0"}, backendX11},
		{"forced X11 in Wayland", map[string]string{"ONELOOP_BACKEND": "x11", "WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}, backendX11},
		{"forced Wayland", map[string]string{"ONELOOP_BACKEND": "wayland", "DISPLAY": ":0"}, backendWayland},
		{"unknown value ignored", map[string]string{"ONELOOP_BACKEND": "auto", "WAYLAND_DISPLAY": "wayland-1"}, backendWayland},
		{"no display", map[string]string{}, ""},
	} {
		test.env["XDG_RUNTIME_DIR"] = runtime
		got := displayBackend(func(key string) string { return test.env[key] }, waylandAnswers)
		if got != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}

func TestSiblingBinary(t *testing.T) {
	for _, test := range []struct{ exe, backend, want string }{
		{"/opt/1loop/1loop", backendWayland, "/opt/1loop/1loop-wayland"},
		{"/opt/1loop/1loop-wayland", backendX11, "/opt/1loop/1loop"},
		{"/opt/1loop/1loop-wayland", backendWayland, "/opt/1loop/1loop-wayland"},
		{"/opt/1loop/custom", backendX11, ""},
	} {
		if got := siblingBinary(test.exe, test.backend); got != test.want {
			t.Errorf("siblingBinary(%q, %q) = %q, want %q", test.exe, test.backend, got, test.want)
		}
	}
}
