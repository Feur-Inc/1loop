//go:build wasm || test_web_driver

package glfw

import (
	"fyne.io/fyne/v2"

	"github.com/fyne-io/gl-js"
	"github.com/fyne-io/glfw-js"
)

func (d *gLDriver) initGLFW() {
	err := glfw.Init(gl.ContextWatcher)
	if err != nil {
		fyne.LogError("failed to initialise GLFW", err)
		return
	}
}

// refreshRate: the browser draws at its own pace (requestAnimationFrame).
func (d *gLDriver) refreshRate() int { return 60 }

func (d *gLDriver) pollEvents() {
	glfw.PollEvents() // This call blocks while window is being resized, which prevents freeDirtyTextures from being called
}

func (d *gLDriver) Terminate() {
	glfw.Terminate()
}
