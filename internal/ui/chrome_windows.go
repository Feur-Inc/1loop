// SPDX-License-Identifier: MPL-2.0

//go:build windows

package ui

// 1loop's own title bar on Windows, as on Linux: the window has no system
// frame, and Windows still moves, resizes and maximises it.
//
// GLFW makes the undecorated window a bare popup, which Windows neither
// resizes nor snaps, and which a maximise stretches over the taskbar. The
// window gets the styles of a framed one back, and its window procedure
// is wrapped so that the frame they bring takes no room (WM_NCCALCSIZE)
// and is never drawn (WM_NCACTIVATE).

import (
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
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

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	procGetWindowLongPtr   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr   = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc     = user32.NewProc("CallWindowProcW")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procShowWindow         = user32.NewProc("ShowWindow")
	procIsZoomed           = user32.NewProc("IsZoomed")
	procPostMessage        = user32.NewProc("PostMessageW")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procScreenToClient     = user32.NewProc("ScreenToClient")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procGetSystemMetricDpi = user32.NewProc("GetSystemMetricsForDpi")
	procGetDpiForWindow    = user32.NewProc("GetDpiForWindow")

	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procDwmExtendFrame        = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
)

const (
	gwlStyle    = -16
	gwlpWndProc = -4

	wsPopup       = 0x80000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsThickFrame  = 0x00040000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	swMaximize = 3
	swMinimize = 6
	swRestore  = 9

	wmNCCalcSize    = 0x0083
	wmNCActivate    = 0x0086
	wmNCLButtonDown = 0x00A1
	wmLButtonUp     = 0x0202

	smCXFrame        = 32
	smCYFrame        = 33
	smCXPaddedBorder = 92

	htCaption     = 2
	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17

	dwmwaWindowCornerPreference = 33
	dwmwcpRound                 = 2
)

func customChromeSupported() bool { return true }

func chromeReady()                                    {}
func framePaceSetup(fyne.Window)                      {}
func framePaceWait(bool)                              {}
func scrollGesture() (finger, released, ok bool)      { return false, false, false }
func scrollEventTime() (milliseconds uint32, ok bool) { return 0, false }
func scrollFinger() (finger, ok bool)                 { return false, false }
func switchBackend()                                  {}
func backendLabel() string                            { return "" }
func systemScale() float32                            { return 0 }

// framedWindows maps each window given a frame to its original procedure.
var framedWindows = map[uintptr]uintptr{}

var chromeProc = syscall.NewCallback(func(hwnd, message, wParam, lParam uintptr) uintptr {
	original := framedWindows[hwnd]
	switch message {
	case wmNCCalcSize:
		if wParam != 0 {
			// The client area is the whole window. A maximised window
			// reaches past the screen by its frame: keep that part out.
			if zoomed(hwnd) {
				x, y := frameThickness(hwnd)
				// lParam points to the window's rectangle, in memory Windows owns.
				rect := *(**[4]int32)(unsafe.Pointer(&lParam))
				rect[0] += x
				rect[1] += y
				rect[2] -= x
				rect[3] -= y
			}
			return 0
		}
	case wmNCActivate:
		// -1: Windows changes the active state without drawing a frame.
		r, _, _ := procCallWindowProc.Call(original, hwnd, message, wParam, ^uintptr(0))
		return r
	}
	r, _, _ := procCallWindowProc.Call(original, hwnd, message, wParam, lParam)
	return r
})

// windowIdentity gives the window its frame styles, once.
func windowIdentity(window fyne.Window) {
	withHWND(window, func(hwnd uintptr) {
		if _, done := framedWindows[hwnd]; done {
			return
		}
		style, _, _ := procGetWindowLongPtr.Call(hwnd, toUintptr(gwlStyle))
		style = style&^wsPopup | wsCaption | wsSysMenu | wsThickFrame | wsMinimizeBox | wsMaximizeBox
		original, _, _ := procGetWindowLongPtr.Call(hwnd, toUintptr(gwlpWndProc))
		framedWindows[hwnd] = original
		procSetWindowLongPtr.Call(hwnd, toUintptr(gwlpWndProc), chromeProc)
		procSetWindowLongPtr.Call(hwnd, toUintptr(gwlStyle), style)
		procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
		// Windows 11 rounds the four corners of a window that asks for it,
		// and squares them again while it is maximised. With no frame left,
		// it cut only the top corners round and filled the bottom ones
		// black: 1 px of frame extended into the window at the bottom lets
		// the compositor round those too. That frame blends the window
		// with what is behind it by alpha, which is why the framebuffer has
		// none on Windows (third_party/fyne, glfw_core.go). Windows 10
		// refuses the attribute (it has no rounded corners): nothing more
		// is done there.
		if procDwmSetWindowAttribute.Find() == nil && procDwmExtendFrame.Find() == nil {
			round := uint32(dwmwcpRound)
			hr, _, _ := procDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&round)), unsafe.Sizeof(round))
			if hr == 0 { // S_OK
				margins := [4]int32{0, 0, 0, 1} // left, right, top, bottom
				procDwmExtendFrame.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
			}
		}
	})
}

// windowMoveResize hands the pressed pointer to Windows, as a press on the
// frame would. Fyne is told first that the button went up: Windows keeps the
// release for itself.
func windowMoveResize(window fyne.Window, direction int) {
	hit := map[int]uintptr{edgeMove: htCaption, edgeLeft: htLeft, edgeRight: htRight, edgeTop: htTop, edgeBottom: htBottom,
		edgeTopLeft: htTopLeft, edgeTopRight: htTopRight, edgeBottomLeft: htBottomLeft, edgeBottomRight: htBottomRight}[direction]
	withHWND(window, func(hwnd uintptr) {
		var screen [2]int32
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&screen)))
		client := screen
		procScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&client)))
		procPostMessage.Call(hwnd, wmLButtonUp, 0, pointParam(client))
		procPostMessage.Call(hwnd, wmNCLButtonDown, hit, pointParam(screen))
	})
}

func windowToggleMaximize(window fyne.Window) {
	withHWND(window, func(hwnd uintptr) {
		if zoomed(hwnd) {
			procShowWindow.Call(hwnd, swRestore)
		} else {
			procShowWindow.Call(hwnd, swMaximize)
		}
	})
}

func windowMinimize(window fyne.Window) {
	withHWND(window, func(hwnd uintptr) { procShowWindow.Call(hwnd, swMinimize) })
}

// withHWND runs f with the window's handle, on the main thread like GLFW.
func withHWND(window fyne.Window, f func(uintptr)) {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		if win, ok := context.(driver.WindowsWindowContext); ok && win.HWND != 0 {
			f(win.HWND)
		}
	})
}

func zoomed(hwnd uintptr) bool {
	r, _, _ := procIsZoomed.Call(hwnd)
	return r != 0
}

// frameThickness is the sizing frame at the window's DPI (Windows 10 and
// later), or the system's.
func frameThickness(hwnd uintptr) (x, y int32) {
	metric := func(index uintptr) int32 {
		if procGetSystemMetricDpi.Find() == nil && procGetDpiForWindow.Find() == nil {
			dpi, _, _ := procGetDpiForWindow.Call(hwnd)
			r, _, _ := procGetSystemMetricDpi.Call(index, dpi)
			return int32(r)
		}
		r, _, _ := procGetSystemMetrics.Call(index)
		return int32(r)
	}
	padding := metric(smCXPaddedBorder)
	return metric(smCXFrame) + padding, metric(smCYFrame) + padding
}

func pointParam(point [2]int32) uintptr {
	return uintptr(uint16(point[0])) | uintptr(uint16(point[1]))<<16
}

func toUintptr(value int) uintptr { return uintptr(value) }
