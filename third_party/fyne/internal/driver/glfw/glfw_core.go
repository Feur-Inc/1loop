//go:build ((!gles && !arm && !arm64) || darwin) && !wasm && !test_web_driver

package glfw

import (
	"runtime"

	"github.com/go-gl/glfw/v3.3/glfw"
)

func initWindowHints() {
	glfw.WindowHint(glfw.ContextVersionMajor, 2)
	glfw.WindowHint(glfw.ContextVersionMinor, 1)

	glfw.WindowHint(glfw.CocoaGraphicsSwitching, glfw.True)

	// 1loop: on Windows, a framebuffer without alpha. 1loop extends the
	// compositor's frame into its window to get round corners (internal/ui
	// chrome_windows.go), and Windows then blends the window's alpha with
	// the desktop: the half-transparent edges Fyne draws (shadows) let the
	// desktop show through. Without alpha the window is opaque.
	if runtime.GOOS == "windows" {
		glfw.WindowHint(glfw.AlphaBits, 0)
	}
}
