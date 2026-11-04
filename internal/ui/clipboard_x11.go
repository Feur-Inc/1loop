// SPDX-License-Identifier: MPL-2.0

//go:build linux && !wayland && cgo

package ui

// Images from the X11 clipboard. Fyne's clipboard (GLFW's) is text-only, so
// 1loop asks the CLIPBOARD selection for an image itself, over a connection
// and a hidden window of its own: GLFW's event loop never sees the replies.

/*
#cgo LDFLAGS: -lX11
#include <poll.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <X11/Xatom.h>
#include <X11/Xlib.h>
#include <X11/Xlibint.h>

struct clip_x11 {
	Display* d;
	Window w;
	Atom property;
	long long deadline;
};

static long long clip_now_ms(void) {
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	return (long long) t.tv_sec * 1000 + t.tv_nsec / 1000000;
}

// clip_wait waits for an event of type on our window until the deadline.
// Returns 1 with it in ev, 0 on time out.
static int clip_wait(struct clip_x11* c, int type, XEvent* ev) {
	for (;;) {
		while (XPending(c->d)) {
			XNextEvent(c->d, ev);
			if (ev->type == type && ev->xany.window == c->w) return 1;
		}
		long long left = c->deadline - clip_now_ms();
		if (left <= 0) return 0;
		struct pollfd fd = { ConnectionNumber(c->d), POLLIN, 0 };
		poll(&fd, 1, (int) left);
	}
}

// clip_convert asks the clipboard's owner for target. Returns 1 when the
// owner answered with it, 0 when it refused, -1 on time out.
static int clip_convert(struct clip_x11* c, Atom target) {
	XConvertSelection(c->d, XInternAtom(c->d, "CLIPBOARD", False), target, c->property, c->w, CurrentTime);
	XFlush(c->d);
	XEvent ev;
	for (;;) {
		if (!clip_wait(c, SelectionNotify, &ev)) return -1;
		if (ev.xselection.target != target) continue;
		return ev.xselection.property != None;
	}
}

// clip_take reads and deletes the property, appending its bytes to *buf.
// type receives its type; returns the number of bytes read, -1 on failure.
static long clip_take(struct clip_x11* c, unsigned char** buf, size_t* len, Atom* type) {
	int format;
	unsigned long count, after;
	unsigned char* data = NULL;
	if (XGetWindowProperty(c->d, c->w, c->property, 0, 0x1fffffff, True, AnyPropertyType,
		type, &format, &count, &after, &data) != Success) return -1;
	size_t unit = format == 32 ? sizeof(long) : format / 8;
	size_t size = count * unit;
	if (size && *type != XInternAtom(c->d, "INCR", False)) {
		unsigned char* grown = realloc(*buf, *len + size);
		if (!grown) { XFree(data); return -1; }
		memcpy(grown + *len, data, size);
		*buf = grown;
		*len += size;
	}
	if (data) XFree(data);
	return (long) size;
}

// clip_x11_image reads the clipboard as an image. Returns 0 with the bytes
// in *out (malloc'd) and the type in mime, 1 when there is no image, -1 when
// there is no display, -2 when the owner did not answer in time.
// GLFW's own connection: 1loop answers for the clipboard there, on the
// thread now waiting for us, and only ever with text.
static int clip_owned_by(Display* own, Window owner) {
	if (!own) return 0;
	struct _XDisplay* p = (struct _XDisplay*) own; // Xlibint.h: the connection's id range
	return (owner & ~p->resource_mask) == p->resource_base;
}

static int clip_x11_image(Display* own, int timeout, unsigned char** out, size_t* outlen, char* mime, size_t mimesize) {
	*out = NULL;
	*outlen = 0;
	struct clip_x11 c;
	c.d = XOpenDisplay(NULL);
	if (!c.d) return -1;
	c.w = XCreateSimpleWindow(c.d, DefaultRootWindow(c.d), 0, 0, 1, 1, 0, 0, 0);
	XSelectInput(c.d, c.w, PropertyChangeMask);
	c.property = XInternAtom(c.d, "LOOP_CLIPBOARD", False);
	c.deadline = clip_now_ms() + timeout;
	int result = 1;

	Atom target = None;
	Window owner = XGetSelectionOwner(c.d, XInternAtom(c.d, "CLIPBOARD", False));
	if (owner == None || clip_owned_by(own, owner)) goto done;
	// Which types the owner offers: image/png when there, else the first image/*.
	int got = clip_convert(&c, XInternAtom(c.d, "TARGETS", False));
	if (got < 0) { result = -2; goto done; }
	if (got > 0) {
		unsigned char* list = NULL;
		size_t len = 0;
		Atom type;
		if (clip_take(&c, &list, &len, &type) > 0 && type == XA_ATOM) {
			Atom* atoms = (Atom*) list;
			for (size_t i = 0; i < len / sizeof(Atom); i++) {
				char* name = atoms[i] ? XGetAtomName(c.d, atoms[i]) : NULL;
				if (!name) continue;
				if (strcmp(name, "image/png") == 0) target = atoms[i];
				else if (target == None && strncmp(name, "image/", 6) == 0 && strlen(name) < mimesize) target = atoms[i];
				XFree(name);
				if (target == XInternAtom(c.d, "image/png", False)) break;
			}
		}
		free(list);
	}
	if (target == None) goto done;
	char* name = XGetAtomName(c.d, target);
	strncpy(mime, name, mimesize - 1);
	mime[mimesize - 1] = 0;
	XFree(name);

	got = clip_convert(&c, target);
	if (got < 0) { result = -2; goto done; }
	if (got == 0) goto done;
	Atom type;
	if (clip_take(&c, out, outlen, &type) < 0) goto done;
	if (type == XInternAtom(c.d, "INCR", False)) {
		// A large image comes in pieces: each one is a new value of the
		// property, and an empty one ends it.
		XEvent ev;
		for (;;) {
			c.deadline = clip_now_ms() + timeout;
			do {
				if (!clip_wait(&c, PropertyNotify, &ev)) { result = -2; goto done; }
			} while (ev.xproperty.atom != c.property || ev.xproperty.state != PropertyNewValue);
			long size = clip_take(&c, out, outlen, &type);
			if (size < 0) goto done;
			if (size == 0) break;
		}
	}
	result = *outlen ? 0 : 1;

done:
	if (result != 0) { free(*out); *out = NULL; *outlen = 0; }
	XDestroyWindow(c.d, c.w);
	XCloseDisplay(c.d);
	return result;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os/exec"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"

	"1loop/internal/platform"
)

// clipboardReady has nothing to start: X11 is asked on each paste.
func clipboardReady() {}

func imageClipboardData() ([]byte, error) {
	data, err := x11ClipboardImage()
	if err == nil || errors.Is(err, errNoClipboardImage) {
		return data, err
	}
	if _, lookErr := exec.LookPath("xclip"); lookErr == nil {
		platform.Devf("clipboard", "%v, trying xclip", err)
		return externalImageClipboardData()
	}
	return nil, err
}

func x11ClipboardImage() ([]byte, error) {
	var out *C.uchar
	var size C.size_t
	var mime [128]C.char
	own := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	switch C.clip_x11_image(own, C.int(clipboardTimeout.Milliseconds()), &out, &size, &mime[0], C.size_t(len(mime))) {
	case 1:
		return nil, errNoClipboardImage
	case -1:
		return nil, fmt.Errorf("the X11 display is not available")
	case -2:
		return nil, fmt.Errorf("the application that copied the image did not send it")
	}
	defer C.free(unsafe.Pointer(out))
	if size > clipboardImageLimit {
		return nil, fmt.Errorf("the image in the clipboard is too large")
	}
	return C.GoBytes(unsafe.Pointer(out), C.int(size)), nil
}
