// SPDX-License-Identifier: MPL-2.0

//go:build linux && !wayland && cgo

package ui

// The primary selection on X11 (and XWayland): the text selected last,
// which a middle click pastes in any other program. GLFW owns the X
// connection and answers the requests for PRIMARY itself; it only needs
// the text.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/go-gl/glfw/v3.3/glfw"
)

func setPrimarySelection(window fyne.Window, text string) {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		if x, ok := context.(driver.X11WindowContext); ok && x.WindowHandle != 0 {
			glfw.SetX11SelectionString(text)
		}
	})
}
