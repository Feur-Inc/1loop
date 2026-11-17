// SPDX-License-Identifier: MPL-2.0

//go:build linux && cgo

package ui

import "github.com/go-gl/glfw/v3.3/glfw"

// nameWindows gives every window GLFW makes from now on appID as its X11
// class and, through 1loop's GLFW (third_party/glfw), as its Wayland app id
// before its first frame: the desktop then knows which application it is
// from the start, rather than showing a generic icon until windowIdentity.
// GLFW must be running for the hint to hold; Fyne's own Init after this
// one does nothing. It runs after customChromeSupported, whose init hint
// must come first.
func nameWindows() {
	defer func() { recover() }() // no display: Fyne says so when it makes the window
	if glfw.Init() != nil {
		return
	}
	glfw.WindowHintString(glfw.X11ClassName, appID)
	glfw.WindowHintString(glfw.X11InstanceName, appID)
}
