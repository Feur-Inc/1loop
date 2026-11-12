// SPDX-License-Identifier: MPL-2.0

//go:build linux && wayland && cgo

package ui

// Images from the Wayland clipboard. Fyne's clipboard (GLFW's) is text-only,
// so GLFW's data device also notes the image type the selection is offered
// in (third_party/glfw, wl_window.c), and 1loop reads that type through a
// pipe when the user pastes. It has no data device of its own: with a second
// one, GNOME (Mutter) most often left GLFW's without the selection, so text
// could not be pasted (#334).

/*
#include <stdlib.h>

extern const char* glfwGetWaylandClipboardImageType(void);
extern int glfwReceiveWaylandClipboard(const char* mime, int fd);
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

// clipboardReady has nothing to start: GLFW follows the clipboard.
func clipboardReady() {}

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

// waylandClipboardImage runs on the interface thread, like GLFW, whose
// events keep the selection up to date.
func waylandClipboardImage() ([]byte, error) {
	cmime := C.glfwGetWaylandClipboardImageType()
	if cmime == nil {
		return nil, errNoClipboardImage
	}
	mime := C.GoString(cmime)
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	request := C.CString(mime)
	defer C.free(unsafe.Pointer(request))
	// The request carries a copy of the descriptor, so ours closes at once
	// and the pipe ends when the owner closes its copy.
	sent := C.glfwReceiveWaylandClipboard(request, C.int(write.Fd())) == 1
	write.Close()
	if !sent {
		return nil, fmt.Errorf("the Wayland clipboard is not available")
	}
	return readClipboardPipe(read, mime)
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
