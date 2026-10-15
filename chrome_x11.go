//go:build linux && !wayland && cgo && !server

package main

// Native window chrome for X11 sessions and Wayland sessions running the app
// through XWayland (Fyne's default Linux build). The window manager still
// owns moving, resizing, snapping and maximising: we only ask it to start.

/*
#cgo LDFLAGS: -lX11
#include <stdlib.h>
#include <string.h>
#include <X11/Xlib.h>

static Atom atom(Display *d, const char *name) { return XInternAtom(d, name, False); }

static void send_root(Display *d, Window w, Atom type, long a, long b, long c, long e, long f) {
	XEvent ev;
	memset(&ev, 0, sizeof(ev));
	ev.xclient.type = ClientMessage;
	ev.xclient.window = w;
	ev.xclient.message_type = type;
	ev.xclient.format = 32;
	ev.xclient.data.l[0] = a;
	ev.xclient.data.l[1] = b;
	ev.xclient.data.l[2] = c;
	ev.xclient.data.l[3] = e;
	ev.xclient.data.l[4] = f;
	XSendEvent(d, DefaultRootWindow(d), False, SubstructureRedirectMask | SubstructureNotifyMask, &ev);
}

// Hand the pressed pointer to the window manager (_NET_WM_MOVERESIZE), then
// tell GLFW the button went up so Fyne does not stay in a pressed state.
static void loop_moveresize(Display *d, Window w, int direction) {
	Window root, child;
	int rx = 0, ry = 0, wx = 0, wy = 0;
	unsigned int mask = 0;
	XQueryPointer(d, w, &root, &child, &rx, &ry, &wx, &wy, &mask);
	XUngrabPointer(d, CurrentTime);
	send_root(d, w, atom(d, "_NET_WM_MOVERESIZE"), rx, ry, direction, Button1, 1);

	XEvent up;
	memset(&up, 0, sizeof(up));
	up.xbutton.type = ButtonRelease;
	up.xbutton.display = d;
	up.xbutton.window = w;
	up.xbutton.root = root;
	up.xbutton.x = wx;
	up.xbutton.y = wy;
	up.xbutton.x_root = rx;
	up.xbutton.y_root = ry;
	up.xbutton.button = Button1;
	up.xbutton.same_screen = True;
	XSendEvent(d, w, False, ButtonReleaseMask, &up);
	XFlush(d);
}

static void loop_toggle_maximize(Display *d, Window w) {
	send_root(d, w, atom(d, "_NET_WM_STATE"), 2,
		atom(d, "_NET_WM_STATE_MAXIMIZED_VERT"), atom(d, "_NET_WM_STATE_MAXIMIZED_HORZ"), 1, 0);
	XFlush(d);
}

static void loop_minimize(Display *d, Window w) {
	XIconifyWindow(d, w, DefaultScreen(d));
	XFlush(d);
}

// Xft.dpi is the scale GNOME/KDE actually apply to X clients.
static int loop_xft_dpi(void) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return 0;
	int dpi = 0;
	char *res = XResourceManagerString(d);
	if (res) {
		char *p = strstr(res, "Xft.dpi:");
		if (p) dpi = atoi(p + 8);
	}
	XCloseDisplay(d);
	return dpi;
}
*/
import "C"

import (
	"os"
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/go-gl/glfw/v3.3/glfw"
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

func customChromeSupported() bool { return true }

func chromeReady() {}

// switchBackend hands over to the native Wayland build (same path with a
// "-wayland" suffix) when the session is Wayland, so text is rendered at the
// screen's real resolution instead of being upscaled by XWayland.
func switchBackend() {
	if os.Getenv("WAYLAND_DISPLAY") == "" || os.Getenv("ONELOOP_BACKEND") == "x11" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	target := exe + "-wayland"
	if _, err := os.Stat(target); err == nil {
		syscall.Exec(target, append([]string{target}, os.Args[1:]...), os.Environ())
	}
}

func systemScale() float32 {
	if dpi := int(C.loop_xft_dpi()); dpi > 0 {
		return float32(dpi) / 96
	}
	return 0
}

// withX11 runs f with GLFW's own display connection and the window id; it is
// only called from Fyne callbacks, which run on the main thread like GLFW.
func withX11(window fyne.Window, f func(*C.Display, C.Window)) {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		x, ok := context.(driver.X11WindowContext)
		if !ok || x.WindowHandle == 0 {
			return
		}
		display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
		if display != nil {
			f(display, C.Window(x.WindowHandle))
		}
	})
}

func windowMoveResize(window fyne.Window, direction int) {
	withX11(window, func(d *C.Display, x C.Window) { C.loop_moveresize(d, x, C.int(direction)) })
}

// X11 has no frame pacing of 1loop's own: the swap never waits for a
// hidden window there. Nor does it tell when fingers leave a touchpad.
func framePaceSetup(fyne.Window)                      {}
func framePaceWait(bool)                              {}
func scrollGesture() (finger, released, ok bool)      { return false, false, false }
func scrollEventTime() (milliseconds uint32, ok bool) { return 0, false }

// windowIdentity has nothing to do on X11: GLFW already names the window
// class after its title, which is the application id.
func windowIdentity(fyne.Window) {}

func windowToggleMaximize(window fyne.Window) {
	withX11(window, func(d *C.Display, x C.Window) { C.loop_toggle_maximize(d, x) })
}

func windowMinimize(window fyne.Window) {
	withX11(window, func(d *C.Display, x C.Window) { C.loop_minimize(d, x) })
}
