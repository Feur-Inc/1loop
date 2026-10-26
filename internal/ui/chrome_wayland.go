//go:build linux && wayland && cgo

package ui

// Native Wayland window chrome. GLFW 3.3 has no public call to start an
// interactive move, so we ask the compositor ourselves (xdg_toplevel.move /
// resize). Every pointer taken from GLFW is checked with wl_proxy_get_class
// before use; if anything does not match, the call silently does nothing.

/*
#cgo LDFLAGS: -lwayland-client
#include <errno.h>
#include <poll.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <wayland-client.h>

// Public GLFW API (linked from go-gl/glfw).
extern struct wl_display* glfwGetWaylandDisplay(void);
extern void glfwInitHint(int hint, int value);
extern int glfwGetWindowAttrib(void* window, int attrib);
extern void glfwMaximizeWindow(void* window);
extern void glfwRestoreWindow(void* window);
extern void glfwIconifyWindow(void* window);
extern void glfwMakeContextCurrent(void* window);
extern void glfwSwapInterval(int interval);
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

static void p_enter(void* user_data, struct wl_pointer* pointer, uint32_t serial, struct wl_surface* surface, wl_fixed_t x, wl_fixed_t y) {}
static void p_leave(void* user_data, struct wl_pointer* pointer, uint32_t serial, struct wl_surface* surface) {}
static void p_motion(void* user_data, struct wl_pointer* pointer, uint32_t timestamp, wl_fixed_t x, wl_fixed_t y) {}
static void p_button(void* user_data, struct wl_pointer* pointer, uint32_t serial, uint32_t timestamp, uint32_t button, uint32_t state) {
	if (state == WL_POINTER_BUTTON_STATE_PRESSED) loop_serial = serial;
}
// Scroll gestures. GLFW 3.3 passes the distances on but drops what the
// inertia needs: when each event happened, whether it comes from fingers on
// a touchpad or from a wheel, and the moment the fingers leave the pad.
static uint32_t loop_axis_time;   // when the last scroll event happened (compositor clock, ms)
static int loop_axis_finger;      // the gesture in progress comes from a touchpad
static int loop_axis_released;    // the fingers have left the pad since the last look
static void p_axis(void* user_data, struct wl_pointer* pointer, uint32_t timestamp, uint32_t axis, wl_fixed_t distance) {
	loop_axis_time = timestamp;
	loop_axis_released = 0;
}
static void p_frame(void* user_data, struct wl_pointer* pointer) {}
static void p_axis_source(void* user_data, struct wl_pointer* pointer, uint32_t source) {
	// Fingers on a touchpad, or another continuous device: not a wheel.
	loop_axis_finger = source == WL_POINTER_AXIS_SOURCE_FINGER || source == WL_POINTER_AXIS_SOURCE_CONTINUOUS;
}
static void p_axis_stop(void* user_data, struct wl_pointer* pointer, uint32_t timestamp, uint32_t axis) {
	loop_axis_time = timestamp;
	loop_axis_released = 1;
}
static int loop_take_release(void) {
	int released = loop_axis_released;
	loop_axis_released = 0;
	return released;
}
static int loop_scroll_finger(void) { return loop_axis_finger; }
static uint32_t loop_scroll_time(void) { return loop_axis_time; }
static int loop_listening(void) { return loop_pointer != NULL; }
static void p_axis_discrete(void* user_data, struct wl_pointer* pointer, uint32_t axis, int32_t steps) {}
static const struct wl_pointer_listener pointer_listener = {
	p_enter, p_leave, p_motion, p_button, p_axis, p_frame, p_axis_source, p_axis_stop, p_axis_discrete,
};

static void r_global(void* user_data, struct wl_registry* registry, uint32_t name, const char* interface_name, uint32_t version) {
	if (!loop_seat && strcmp(interface_name, "wl_seat") == 0)
		loop_seat = wl_registry_bind(registry, name, &wl_seat_interface, version < 5 ? version : 5);
}
static void r_remove(void* user_data, struct wl_registry* registry, uint32_t name) {}
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

// Frame pacing. By default the GL driver waits, inside each buffer swap,
// for the compositor to say the previous frame was shown; a compositor says
// nothing for a window nobody can see (covered, minimized, on another
// workspace), so the interface thread stayed in that swap for as long as
// the window was hidden: the agent's updates piled up behind it and the
// desktop called the window unresponsive. So the swap no longer waits
// (interval 0), and 1loop waits for the frame itself, before drawing the
// next one, with a limit: a hidden window just stops being paced.
static struct wl_event_queue* pace_queue;
static struct wl_surface* pace_surface;   // GLFW's surface, as seen from pace_queue
static struct wl_callback* pace_callback; // the frame awaited, NULL when none

static void pace_done(void* data, struct wl_callback* callback, uint32_t time) {
	wl_callback_destroy(callback);
	if (callback == pace_callback) pace_callback = NULL;
}
static const struct wl_callback_listener pace_listener = { pace_done };

static int loop_pace_setup(uintptr_t handle) {
	struct wl_surface* surface = (struct wl_surface*) handle;
	struct wl_display* display = glfwGetWaylandDisplay();
	void* window = loop_glfw_window(surface);
	if (!window || !display || pace_surface) return 0;
	glfwMakeContextCurrent(window);
	glfwSwapInterval(0);
	glfwMakeContextCurrent(NULL);
	pace_queue = wl_display_create_queue(display);
	pace_surface = wl_proxy_create_wrapper(surface);
	if (!pace_queue || !pace_surface) { pace_surface = NULL; return 0; }
	wl_proxy_set_queue((struct wl_proxy*) pace_surface, pace_queue);
	return 1;
}

static int loop_pace_pending(void) { return pace_callback != NULL; }

// loop_pace_request asks to be told when the next frame drawn is on screen.
static void loop_pace_request(void) {
	if (!pace_surface || pace_callback) return;
	pace_callback = wl_surface_frame(pace_surface);
	wl_callback_add_listener(pace_callback, &pace_listener, NULL);
}

static int64_t pace_now_ms(void) {
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	return (int64_t) t.tv_sec * 1000 + t.tv_nsec / 1000000;
}

// loop_pace_wait waits for the awaited frame, at most limit milliseconds
// (0 only collects what has already arrived). It reads the display on its
// own queue, as the GL driver did inside the swap, so GLFW's events stay
// queued for GLFW. Returns 1 when no frame is awaited any more.
static int loop_pace_wait(int limit) {
	struct wl_display* display = glfwGetWaylandDisplay();
	if (!pace_surface || !display) return 1;
	int64_t deadline = pace_now_ms() + limit;
	while (pace_callback) {
		if (wl_display_prepare_read_queue(display, pace_queue) != 0) {
			wl_display_dispatch_queue_pending(display, pace_queue);
			continue;
		}
		wl_display_flush(display);
		int64_t left = deadline - pace_now_ms();
		struct pollfd fd = { wl_display_get_fd(display), POLLIN, 0 };
		int ready = poll(&fd, 1, left > 0 ? (int) left : 0);
		if (ready <= 0) {
			wl_display_cancel_read(display);
			if (ready < 0 && errno == EINTR && left > 0) continue;
			break;
		}
		if (wl_display_read_events(display) < 0) break;
		wl_display_dispatch_queue_pending(display, pace_queue);
		if (left <= 0) break;
	}
	return pace_callback == NULL;
}

static void loop_minimize(uintptr_t handle) {
	struct wl_surface* surface = (struct wl_surface*) handle;
	void* window = loop_glfw_window(surface);
	if (window) glfwIconifyWindow(window);
}
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/go-gl/glfw/v3.3/glfw"

	"1loop/internal/platform"
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

// builtBackend is this build's display, see switchBackend.
const builtBackend = backendWayland

func backendReady() {
	useSystemCursor()
	C.loop_disable_libdecor()
	fallbackDisplay(initializeWayland)
}

// Only the short-lived display probe calls this, before loading app state.
func initializeWayland() (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("GLFW initialization: %v", failure)
		}
	}()
	if err := glfw.Init(); err != nil {
		return err
	}
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.ContextVersionMajor, 2)
	glfw.WindowHint(glfw.ContextVersionMinor, 1)
	window, err := glfw.CreateWindow(1, 1, "1loop", nil, nil)
	if err != nil {
		return err
	}
	if window == nil {
		return fmt.Errorf("GLFW did not create a context")
	}
	window.Destroy()
	glfw.Terminate()
	return nil
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

func withSurface(window fyne.Window, callback func(C.uintptr_t)) {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		if c, ok := context.(driver.WaylandWindowContext); ok && c.WaylandSurface != 0 {
			callback(C.uintptr_t(c.WaylandSurface))
		}
	})
}

// The press that triggers a move is still being dispatched when Fyne calls
// us; deferring by one loop turn lets our listener record its serial first.
func windowMoveResize(window fyne.Window, direction int) {
	go fyne.Do(func() {
		withSurface(window, func(s C.uintptr_t) { C.loop_moveresize(s, C.int(direction)) })
	})
}

// windowIdentity gives the window its application id, which the desktop
// matches with the launcher entry of the same name (see appID).
func windowIdentity(window fyne.Window) {
	withSurface(window, func(s C.uintptr_t) {
		id := C.CString(appID)
		defer C.free(unsafe.Pointer(id))
		C.loop_set_app_id(s, id)
	})
}

// Frame pacing, see loop_pace_setup. pacing is off until the window exists,
// and stays off if anything about the display is not as expected (the
// swap then waits as before).
var pacing struct {
	on        bool
	committed bool // a frame was drawn since the awaited one was asked for
	hidden    bool // the last wait ran out: nobody shows our frames
}

// paceLimit is how long a turn waits for the previous frame to be shown:
// three frames of a 60 Hz screen. Past that the window is taken as hidden.
const paceLimit = 50

func framePaceSetup(window fyne.Window) {
	if os.Getenv("ONELOOP_VSYNC") == "1" { // the GL driver's own waiting, as before
		platform.Devf("frame", "pacing left to the GL driver (ONELOOP_VSYNC=1)")
		return
	}
	withSurface(window, func(s C.uintptr_t) { pacing.on = C.loop_pace_setup(s) == 1 })
	platform.Devf("frame", "Wayland frame pacing by 1loop: %v", pacing.on)
}

// framePaceWait runs at each turn of the loop, before a frame may be drawn.
// painted says the previous turn drew one. It waits for that frame to be
// on screen, so frames are drawn at the screen's pace, each with a whole
// refresh period ahead of it.
func framePaceWait(painted bool) {
	if !pacing.on {
		return
	}
	if painted {
		pacing.committed = true
	}
	wait := 0
	if pacing.committed && !pacing.hidden {
		wait = paceLimit
	}
	shown := C.loop_pace_wait(C.int(wait)) == 1
	switch {
	case shown:
		if pacing.hidden {
			pacing.hidden = false
			platform.Devf("frame", "the window is shown again: frames are paced by the screen")
		}
		C.loop_pace_request() // rides on the next frame drawn, whenever that is
		pacing.committed = false
	case wait > 0:
		pacing.hidden = true
		platform.Devf("frame", "no frame shown for %d ms: the window is hidden, drawing without waiting", paceLimit)
	}
}

// scrollGesture tells what GLFW does not about the scroll gesture in
// progress: whether it comes from a touchpad, and whether the fingers have
// just left it. ok is false where the display does not say.
func scrollGesture() (finger, released, ok bool) {
	return C.loop_scroll_finger() == 1, C.loop_take_release() == 1, C.loop_listening() == 1
}

// scrollEventTime is when the last scroll event happened, in milliseconds
// on the compositor's clock: steadier than the time 1loop hears of it,
// since events reach it in batches, one per turn of the loop.
func scrollEventTime() (uint32, bool) {
	ms := uint32(C.loop_scroll_time())
	return ms, ms != 0 && C.loop_listening() == 1
}

func windowToggleMaximize(window fyne.Window) {
	withSurface(window, func(s C.uintptr_t) { C.loop_toggle_maximize(s) })
}

func windowMinimize(window fyne.Window) {
	withSurface(window, func(s C.uintptr_t) { C.loop_minimize(s) })
}
