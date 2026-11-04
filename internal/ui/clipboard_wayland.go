// SPDX-License-Identifier: MPL-2.0

//go:build linux && wayland && cgo

package ui

// Images from the Wayland clipboard. Fyne's clipboard (GLFW's) is text-only,
// so 1loop keeps a data device of its own on GLFW's display, on a private
// event queue: it learns the current selection and the types it is offered
// in, and reads an image/* type through a pipe when the user pastes. Every
// listener is complete (GLFW's are not, see #145).

/*
#cgo LDFLAGS: -lwayland-client
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wayland-client.h>

extern struct wl_display* glfwGetWaylandDisplay(void);

// What one offer can be read as: the image type to ask for, image/png when
// it is offered, else the first image/* type.
struct clip_types {
	char image[128];
	int png;
};

static struct wl_event_queue* clip_queue;
static struct wl_seat* clip_seat;
static struct wl_data_device_manager* clip_manager;
static struct wl_data_device* clip_device;
static struct wl_data_offer* clip_selection; // NULL when the clipboard is empty

static void clip_offer_destroy(struct wl_data_offer* offer) {
	if (!offer) return;
	free(wl_data_offer_get_user_data(offer));
	wl_data_offer_destroy(offer);
}

static void o_offer(void* data, struct wl_data_offer* offer, const char* mime) {
	struct clip_types* types = data;
	if (!types || strncmp(mime, "image/", 6) != 0 || types->png) return;
	if (strcmp(mime, "image/png") == 0) types->png = 1;
	else if (types->image[0]) return;
	if (strlen(mime) < sizeof(types->image)) strcpy(types->image, mime);
}
static void o_source_actions(void* data, struct wl_data_offer* offer, uint32_t actions) {}
static void o_action(void* data, struct wl_data_offer* offer, uint32_t action) {}
static const struct wl_data_offer_listener clip_offer_listener = { o_offer, o_source_actions, o_action };

static void d_data_offer(void* data, struct wl_data_device* device, struct wl_data_offer* offer) {
	wl_data_offer_add_listener(offer, &clip_offer_listener, calloc(1, sizeof(struct clip_types)));
}
// Drag and drop is GLFW's business: an offer that comes with enter is
// dropped at once.
static void d_enter(void* data, struct wl_data_device* device, uint32_t serial, struct wl_surface* surface,
	wl_fixed_t x, wl_fixed_t y, struct wl_data_offer* offer) {
	if (offer && offer != clip_selection) clip_offer_destroy(offer);
}
static void d_leave(void* data, struct wl_data_device* device) {}
static void d_motion(void* data, struct wl_data_device* device, uint32_t time, wl_fixed_t x, wl_fixed_t y) {}
static void d_drop(void* data, struct wl_data_device* device) {}
static void d_selection(void* data, struct wl_data_device* device, struct wl_data_offer* offer) {
	if (clip_selection != offer) clip_offer_destroy(clip_selection);
	clip_selection = offer;
}
static const struct wl_data_device_listener clip_device_listener = {
	d_data_offer, d_enter, d_leave, d_motion, d_drop, d_selection,
};

static void r_global(void* data, struct wl_registry* registry, uint32_t name, const char* interface_name, uint32_t version) {
	if (!clip_seat && strcmp(interface_name, "wl_seat") == 0)
		clip_seat = wl_registry_bind(registry, name, &wl_seat_interface, 1); // no listener: its events are ignored
	else if (!clip_manager && strcmp(interface_name, "wl_data_device_manager") == 0)
		clip_manager = wl_registry_bind(registry, name, &wl_data_device_manager_interface, version < 3 ? version : 3);
}
static void r_remove(void* data, struct wl_registry* registry, uint32_t name) {}
static const struct wl_registry_listener clip_registry_listener = { r_global, r_remove };

// clip_setup creates the data device. The compositor tells it the selection
// whenever the window gets the keyboard, so this runs as soon as GLFW is up.
static int clip_setup(void) {
	if (clip_device) return 1;
	struct wl_display* display = glfwGetWaylandDisplay();
	if (!display) return 0;
	if (!clip_queue) clip_queue = wl_display_create_queue(display);
	if (!clip_queue) return 0;
	if (!clip_seat || !clip_manager) {
		struct wl_registry* registry = wl_display_get_registry(display);
		wl_proxy_set_queue((struct wl_proxy*) registry, clip_queue);
		wl_registry_add_listener(registry, &clip_registry_listener, NULL);
		wl_display_roundtrip_queue(display, clip_queue);
		wl_registry_destroy(registry);
	}
	if (!clip_seat || !clip_manager) return 0;
	clip_device = wl_data_device_manager_get_data_device(clip_manager, clip_seat);
	wl_data_device_add_listener(clip_device, &clip_device_listener, NULL);
	wl_display_flush(display);
	return 1;
}

// clip_image_type brings the selection up to date and returns the image
// type it can be read as: 0 when there is one (copied to mime), 1 when the
// clipboard holds no image, -1 when the display cannot be read.
static int clip_image_type(char* mime, size_t size) {
	struct wl_display* display = glfwGetWaylandDisplay();
	if (!clip_setup() || !display) return -1;
	if (wl_display_roundtrip_queue(display, clip_queue) < 0) return -1;
	if (!clip_selection) return 1;
	struct clip_types* types = wl_data_offer_get_user_data(clip_selection);
	if (!types || !types->image[0]) return 1;
	strncpy(mime, types->image, size - 1);
	mime[size - 1] = 0;
	return 0;
}

// clip_receive asks the selection's owner to write it, as mime, into fd.
static int clip_receive(const char* mime, int fd) {
	struct wl_display* display = glfwGetWaylandDisplay();
	if (!clip_selection || !display) return 0;
	wl_data_offer_receive(clip_selection, mime, fd);
	return wl_display_flush(display) >= 0;
}
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"1loop/internal/platform"
)

// clipboardReady starts following the clipboard once GLFW is running.
func clipboardReady() {
	platform.Devf("clipboard", "Wayland data device: %v", C.clip_setup() == 1)
}

func imageClipboardData() ([]byte, error) {
	data, err := waylandClipboardImage()
	if err == nil || errors.Is(err, errNoClipboardImage) {
		return data, err
	}
	// Our own reader failed: wl-paste, when installed, may still manage.
	if _, lookErr := exec.LookPath("wl-paste"); lookErr == nil {
		platform.Devf("clipboard", "%v, trying wl-paste", err)
		return externalImageClipboardData()
	}
	return nil, err
}

// waylandClipboardImage runs on the interface thread, like GLFW, which
// keeps reading its own events while we wait for ours.
func waylandClipboardImage() ([]byte, error) {
	var mime [128]C.char
	switch C.clip_image_type(&mime[0], C.size_t(len(mime))) {
	case 1:
		return nil, errNoClipboardImage
	case -1:
		return nil, fmt.Errorf("the Wayland clipboard is not available")
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	cmime := C.CString(C.GoString(&mime[0]))
	defer C.free(unsafe.Pointer(cmime))
	// The request carries a copy of the descriptor, so ours closes at once
	// and the pipe ends when the owner closes its copy.
	sent := C.clip_receive(cmime, C.int(write.Fd())) == 1
	write.Close()
	if !sent {
		return nil, fmt.Errorf("the Wayland clipboard is not available")
	}
	return readClipboardPipe(read, C.GoString(&mime[0]))
}

// readClipboardPipe reads what the selection's owner writes, within a few
// seconds and up to clipboardImageLimit.
func readClipboardPipe(read *os.File, mime string) ([]byte, error) {
	read.SetReadDeadline(time.Now().Add(clipboardTimeout))
	data, err := io.ReadAll(io.LimitReader(read, clipboardImageLimit+1))
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		return nil, fmt.Errorf("the application that copied the image did not send it")
	case err != nil:
		return nil, err
	case len(data) > clipboardImageLimit:
		return nil, fmt.Errorf("the image in the clipboard is too large")
	case len(bytes.TrimSpace(data)) == 0:
		return nil, fmt.Errorf("the application that copied the image sent nothing (%s)", mime)
	}
	return data, nil
}
