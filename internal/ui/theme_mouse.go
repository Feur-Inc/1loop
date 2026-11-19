// SPDX-License-Identifier: MPL-2.0

package ui

// The pointer in shader backgrounds (issue #464): iMouse, as on Shadertoy
// but with xy following the pointer whether a button is held or not, and
// u_mouse_inside. Raw positions, not eased: a shader eases them if it
// wants. A shader that uses neither is not drawn again as the pointer
// moves, so it costs nothing while it is still.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
)

var mouseUniforms = []string{"iMouse", "u_mouse_inside"}

// pointer is where the pointer was last seen over the main window.
type pointer struct {
	x, y, pressX, pressY float32
	held, inside, seen   bool
}

// watchPointer gives the backdrop's shader the pointer, on the main window.
func (ui *desktopUI) watchPointer() {
	var at pointer
	desktop.OnPointer = func(e desktop.PointerEvent) {
		b := ui.backdrop
		if b == nil || ui.window == nil || e.Canvas != ui.window.Canvas() || !b.shader.Visible() || !shaderUsesMouse(b.shader) {
			return
		}
		x, y := pixelAt(e.Canvas, e.Position)
		held := e.Button != 0
		if held && !at.held {
			at.pressX, at.pressY = x, y
		}
		at.x, at.y, at.held, at.inside, at.seen = x, y, held, e.Inside, true
		setMouseUniforms(b.shader, at)
		b.shader.Refresh()
	}
}

func shaderUsesMouse(s *canvas.Shader) bool {
	for _, name := range mouseUniforms {
		if used, known := s.Uses(name); used || !known {
			return true
		}
	}
	return false
}

// pixelAt is a canvas position in gl_FragCoord's pixels: from the bottom.
func pixelAt(c fyne.Canvas, pos fyne.Position) (float32, float32) {
	x, y := c.PixelCoordinateForPosition(pos)
	_, height := c.PixelCoordinateForPosition(fyne.NewPos(c.Size().Width, c.Size().Height))
	return float32(x), float32(height - y)
}

func setMouseUniforms(s *canvas.Shader, at pointer) {
	z := at.pressX
	if !at.held {
		z = -z
	}
	inside := float32(0)
	if at.inside {
		inside = 1
	}
	s.SetUniform("iMouse", at.x, at.y, z, at.pressY)
	s.SetUniform("u_mouse_inside", inside)
}
