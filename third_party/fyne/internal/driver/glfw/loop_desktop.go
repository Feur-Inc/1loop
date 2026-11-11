//go:build !wasm && !test_web_driver

package glfw

import (
	"sync/atomic"

	"fyne.io/fyne/v2"

	"github.com/go-gl/glfw/v3.3/glfw"
)

func (d *gLDriver) initGLFW() {
	err := glfw.Init()
	if err != nil {
		fyne.LogError("failed to initialise GLFW", err)
		return
	}

	initCursors()
	glfw.SetMonitorCallback(func(*glfw.Monitor, glfw.PeripheralEvent) { screensChanged.Store(true) })
	screensChanged.Store(true)
}

// The loop turns at the fastest refresh rate of the screens (1loop): at a
// fixed 60 Hz a 120 or 165 Hz screen got a new frame every second or third
// refresh. Asked again only when the screens change.
var (
	screensChanged atomic.Bool
	screensRate    = 60
)

const maxRefreshRate = 360

func (d *gLDriver) refreshRate() int {
	if !screensChanged.Swap(false) {
		return screensRate
	}
	rate := 0
	for _, monitor := range glfw.GetMonitors() {
		if mode := monitor.GetVideoMode(); mode != nil && mode.RefreshRate > rate {
			rate = mode.RefreshRate
		}
	}
	if rate < 60 {
		rate = 60
	}
	if rate > maxRefreshRate {
		rate = maxRefreshRate
	}
	screensRate = rate
	return screensRate
}

func (d *gLDriver) pollEvents() {
	glfw.PollEvents() // This call blocks while window is being resized, which prevents freeDirtyTextures from being called
}

func (d *gLDriver) Terminate() {
	glfw.Terminate()
}
