//go:build linux && wayland && cgo

package main

// Native Wayland window chrome. GLFW 3.3 has no public call to start an
// interactive move, so we ask the compositor ourselves (xdg_toplevel.move /
// resize). Every pointer taken from GLFW is checked with wl_proxy_get_class
// before use; if anything does not match, the call silently does nothing.

/*
#cgo LDFLAGS: -lwayland-client
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wayland-client.h>

// Public GLFW API (linked from go-gl/glfw).
extern struct wl_display* glfwGetWaylandDisplay(void);
extern void glfwInitHint(int hint, int value);
extern int glfwGetWindowAttrib(void* window, int attrib);
extern void glfwMaximizeWindow(void* window);
extern void glfwRestoreWindow(void* window);
extern void glfwIconifyWindow(void* window);
// Internal GLFW entry point, used to tell Fyne the button went up once the
// compositor has taken the pointer for a move.
extern void _glfwInputMouseClick(void* window, int button, int action, int mods);

#define LOOP_GLFW_WAYLAND_LIBDECOR 0x00053001
#define LOOP_GLFW_WAYLAND_DISABLE_LIBDECOR 0x00038002
#define LOOP_GLFW_MAXIMIZED 0x00020008

// xdg_toplevel request opcodes (xdg-shell, stable).
#define XDG_TOPLEVEL_SET_APP_ID 3
#define XDG_TOPLEVEL_MOVE 5
#define XDG_TOPLEVEL_RESIZE 6

static struct wl_seat* loop_seat;
static struct wl_pointer* loop_pointer;
static uint32_t loop_serial;

static void p_enter(void* d, struct wl_pointer* p, uint32_t s, struct wl_surface* f, wl_fixed_t x, wl_fixed_t y) {}
static void p_leave(void* d, struct wl_pointer* p, uint32_t s, struct wl_surface* f) {}
static void p_motion(void* d, struct wl_pointer* p, uint32_t t, wl_fixed_t x, wl_fixed_t y) {}
static void p_button(void* d, struct wl_pointer* p, uint32_t serial, uint32_t t, uint32_t b, uint32_t state) {
	if (state == WL_POINTER_BUTTON_STATE_PRESSED) loop_serial = serial;
}
static void p_axis(void* d, struct wl_pointer* p, uint32_t t, uint32_t a, wl_fixed_t v) {}
static void p_frame(void* d, struct wl_pointer* p) {}
static void p_axis_source(void* d, struct wl_pointer* p, uint32_t s) {}
static void p_axis_stop(void* d, struct wl_pointer* p, uint32_t t, uint32_t a) {}
static void p_axis_discrete(void* d, struct wl_pointer* p, uint32_t a, int32_t v) {}
static const struct wl_pointer_listener pointer_listener = {
	p_enter, p_leave, p_motion, p_button, p_axis, p_frame, p_axis_source, p_axis_stop, p_axis_discrete,
};

static void r_global(void* data, struct wl_registry* r, uint32_t name, const char* iface, uint32_t version) {
	if (!loop_seat && strcmp(iface, "wl_seat") == 0)
		loop_seat = wl_registry_bind(r, name, &wl_seat_interface, version < 5 ? version : 5);
}
static void r_remove(void* data, struct wl_registry* r, uint32_t name) {}
static const struct wl_registry_listener registry_listener = { r_global, r_remove };

static void loop_disable_libdecor(void) {
	// Our own title bar replaces client decorations; plain xdg-shell keeps
	// the toplevel reachable for move and resize.
	glfwInitHint(LOOP_GLFW_WAYLAND_LIBDECOR, LOOP_GLFW_WAYLAND_DISABLE_LIBDECOR);
}

// Bind our own seat on a private queue (so GLFW's events are never dispatched
// here), then listen to the pointer on the default queue to learn the serial
// of each button press, which the compositor requires for move and resize.
static int loop_setup(void) {
	if (loop_pointer) return 1;
	struct wl_display* display = glfwGetWaylandDisplay();
	if (!display) return 0;
	struct wl_event_queue* queue = wl_display_create_queue(display);
	struct wl_registry* registry = wl_display_get_registry(display);
	wl_proxy_set_queue((struct wl_proxy*) registry, queue);
	wl_registry_add_listener(registry, &registry_listener, NULL);
	wl_display_roundtrip_queue(display, queue);
	wl_registry_destroy(registry);
	if (!loop_seat) return 0;
	wl_proxy_set_queue((struct wl_proxy*) loop_seat, queue);
	loop_pointer = wl_seat_get_pointer(loop_seat);
	wl_proxy_set_queue((struct wl_proxy*) loop_pointer, NULL);
	wl_pointer_add_listener(loop_pointer, &pointer_listener, NULL);
	return 1;
}

static int is_class(void* proxy, const char* name) {
	return proxy && strcmp(wl_proxy_get_class((struct wl_proxy*) proxy), name) == 0;
}

// GLFW stores its window as the surface's listener data. In GLFW 3.3.10 the
// Wayland window state holds, in order: surface, callback, egl.window,
// pending{6 ints}, xdg.surface, xdg.toplevel. We locate the surface field and
// verify both xdg objects by their protocol class.
static void* loop_glfw_window(struct wl_surface* surface) {
	if (!is_class(surface, "wl_surface")) return NULL;
	return wl_proxy_get_user_data((struct wl_proxy*) surface);
}

static void* loop_toplevel(void* window, struct wl_surface* surface) {
	char* base = window;
	for (size_t off = 0; off < 4096; off += sizeof(void*)) {
		if (*(void**)(base + off) != surface) continue;
		void* xdg_surface = *(void**)(base + off + 48);
		void* toplevel = *(void**)(base + off + 56);
		if (is_class(xdg_surface, "xdg_surface") && is_class(toplevel, "xdg_toplevel")) return toplevel;
		return NULL;
	}
	return NULL;
}

static int xdg_edge(int direction) {
	switch (direction) {
	case 0: return 5;  // top left
	case 1: return 1;  // top
	case 2: return 9;  // top right
	case 3: return 8;  // right
	case 4: return 10; // bottom right
	case 5: return 2;  // bottom
	case 6: return 6;  // bottom left
	case 7: return 4;  // left
	}
	return 0;
}

static void loop_moveresize(uintptr_t handle, int direction) {
	struct wl_surface* surface = (struct wl_surface*) handle;
	if (!loop_setup() || !loop_serial) return;
	void* window = loop_glfw_window(surface);
	if (!window) return;
	void* toplevel = loop_toplevel(window, surface);
	if (!toplevel) return;
	if (direction == 8)
		wl_proxy_marshal((struct wl_proxy*) toplevel, XDG_TOPLEVEL_MOVE, loop_seat, loop_serial);
	else
		wl_proxy_marshal((struct wl_proxy*) toplevel, XDG_TOPLEVEL_RESIZE, loop_seat, loop_serial, xdg_edge(direction));
	wl_display_flush(glfwGetWaylandDisplay());
	_glfwInputMouseClick(window, 0, 0, 0); // left button released, for Fyne
}

static void loop_toggle_maximize(uintptr_t handle) {
	struct wl_surface* surface = (struct wl_surface*) handle;
	void* window = loop_glfw_window(surface);
	if (!window) return;
	if (glfwGetWindowAttrib(window, LOOP_GLFW_MAXIMIZED)) glfwRestoreWindow(window);
	else glfwMaximizeWindow(window);
}

// GLFW 3.3 only names the window through libdecor, which is off here: the
// compositor would know neither which application this is nor its icon.
static void loop_set_app_id(uintptr_t handle, const char* id) {
	struct wl_surface* surface = (struct wl_surface*) handle;
	void* window = loop_glfw_window(surface);
	if (!window) return;
	void* toplevel = loop_toplevel(window, surface);
	if (!toplevel) return;
	wl_proxy_marshal((struct wl_proxy*) toplevel, XDG_TOPLEVEL_SET_APP_ID, id);
	wl_display_flush(glfwGetWaylandDisplay());
}

static void loop_minimize(uintptr_t handle) {
	struct wl_surface* surface = (struct wl_surface*) handle;
	void* window = loop_glfw_window(surface);
	if (window) glfwIconifyWindow(window);
}
*/
import "C"

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

const (
	edgeTopLeft = iota
	edgeTop
	edgeTopRight
	edgeRight
	edgeBottomRight
	edgeBottom
	edgeBottomLeft
	edgeLeft
	edgeMove
)

func customChromeSupported() bool {
	C.loop_disable_libdecor()
	return true
}

// chromeReady starts listening to button serials once GLFW is running.
func chromeReady() { C.loop_setup() }

// Wayland scales through the buffer scale, no Xft.dpi involved.
func systemScale() float32 { return 0 }

// switchBackend hands over to the X11 build when no Wayland display exists.
func switchBackend() {
	if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("ONELOOP_BACKEND") != "x11" {
		useSystemCursor()
		return
	}
	exe, err := os.Executable()
	if err != nil || !strings.HasSuffix(exe, "-wayland") {
		return
	}
	target := strings.TrimSuffix(exe, "-wayland")
	if _, err := os.Stat(target); err == nil {
		os.Setenv("ONELOOP_BACKEND", "x11")
		syscall.Exec(target, append([]string{target}, os.Args[1:]...), os.Environ())
	}
}

// useSystemCursor gives GLFW the desktop's cursor theme and size: without
// XCURSOR_* it falls back to a generic theme at 16 px, unlike every other app.
// Only this process's environment is set; explicit user values are kept.
func useSystemCursor() {
	read := func(key string) string {
		out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", key).Output()
		if err != nil {
			return ""
		}
		return strings.Trim(strings.TrimSpace(string(out)), "'")
	}
	if os.Getenv("XCURSOR_THEME") == "" {
		if theme := read("cursor-theme"); theme != "" {
			os.Setenv("XCURSOR_THEME", theme)
		}
	}
	if os.Getenv("XCURSOR_SIZE") == "" {
		size := strings.TrimPrefix(read("cursor-size"), "int32 ")
		if size == "" {
			size = "24"
		}
		os.Setenv("XCURSOR_SIZE", size)
	}
}

func withSurface(w fyne.Window, f func(C.uintptr_t)) {
	native, ok := w.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		if c, ok := context.(driver.WaylandWindowContext); ok && c.WaylandSurface != 0 {
			f(C.uintptr_t(c.WaylandSurface))
		}
	})
}

// The press that triggers a move is still being dispatched when Fyne calls
// us; deferring by one loop turn lets our listener record its serial first.
func windowMoveResize(w fyne.Window, direction int) {
	go fyne.Do(func() {
		withSurface(w, func(s C.uintptr_t) { C.loop_moveresize(s, C.int(direction)) })
	})
}

// windowIdentity gives the window its application id, which the desktop
// matches with the launcher entry of the same name (see installLauncher).
func windowIdentity(w fyne.Window) {
	withSurface(w, func(s C.uintptr_t) {
		id := C.CString(appID)
		defer C.free(unsafe.Pointer(id))
		C.loop_set_app_id(s, id)
	})
}

func windowToggleMaximize(w fyne.Window) {
	withSurface(w, func(s C.uintptr_t) { C.loop_toggle_maximize(s) })
}

func windowMinimize(w fyne.Window) {
	withSurface(w, func(s C.uintptr_t) { C.loop_minimize(s) })
}
