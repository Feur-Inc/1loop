// SPDX-License-Identifier: MPL-2.0

//go:build linux && wayland && cgo

package ui

// The primary selection on Wayland: the text selected last, which a middle
// click pastes in any other program. GLFW 3.3 has none; 1loop's copy offers
// it through zwp_primary_selection_v1 (third_party/glfw, wl_window.c) on
// GLFW's own seat, with the serial of the last input.

/*
#include <stdlib.h>

extern int glfwSetWaylandPrimarySelection(const char* string);
*/
import "C"

import (
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

func setPrimarySelection(window fyne.Window, text string) {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		if _, ok := context.(driver.WaylandWindowContext); !ok {
			return
		}
		ctext := C.CString(text)
		defer C.free(unsafe.Pointer(ctext))
		C.glfwSetWaylandPrimarySelection(ctext)
	})
}
