// SPDX-License-Identifier: MPL-2.0

//go:build !windows && (!linux || !cgo)

package ui

// Without cgo on Linux, and on other systems than Linux and Windows, the
// window keeps the system title bar.

import "fyne.io/fyne/v2"

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

func chromeReady()               {}
func windowIdentity(fyne.Window) {}
func framePaceSetup(fyne.Window) {}
func framePaceWait(bool)         {}

func scrollGesture() (finger, released, ok bool)      { return false, false, false }
func scrollEventTime() (milliseconds uint32, ok bool) { return 0, false }
func scrollFinger() (finger, ok bool)                 { return false, false }
func switchBackend()                                  {}
func backendLabel() string                            { return "" }
func customChromeSupported() bool                     { return false }
func systemScale() float32                            { return 0 }
func windowMoveResize(fyne.Window, int)               {}
func windowToggleMaximize(fyne.Window)                {}
func windowMinimize(fyne.Window)                      {}
