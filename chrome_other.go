//go:build !linux || !cgo

package main

// Without cgo or outside Linux the window keeps the system title bar.

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

func chromeReady()                      {}
func windowIdentity(fyne.Window)        {}
func switchBackend()                    {}
func customChromeSupported() bool       { return false }
func systemScale() float32              { return 0 }
func windowMoveResize(fyne.Window, int) {}
func windowToggleMaximize(fyne.Window)  {}
func windowMinimize(fyne.Window)        {}
