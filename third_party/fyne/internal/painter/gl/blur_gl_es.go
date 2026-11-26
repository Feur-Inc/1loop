//go:build (gles || arm || arm64) && !android && !ios && !mobile && !darwin && !wasm && !test_web_driver

// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): what canvas.Blur needs from GL.

package gl

import (
	gl "github.com/go-gl/gl/v3.1/gles2"
)

var _ blurContext = (*esContext)(nil)

func (c *esContext) CopyTexSubImage2D(x, y, width, height int) {
	gl.CopyTexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, int32(x), int32(y), int32(width), int32(height))
}

func (c *esContext) CreateFramebuffer() uint32 {
	var fb uint32
	gl.GenFramebuffers(1, &fb)
	return fb
}

func (c *esContext) BindFramebuffer(fb uint32) {
	gl.BindFramebuffer(gl.FRAMEBUFFER, fb)
}

func (c *esContext) FramebufferTexture(texture Texture) bool {
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, uint32(texture), 0)
	return gl.CheckFramebufferStatus(gl.FRAMEBUFFER) == gl.FRAMEBUFFER_COMPLETE
}

// BlitToWindow copies framebuffer fb into the window's (1loop: the
// retained frame, retained.go).
func (c *esContext) BlitToWindow(fb uint32, width, height int) {
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, fb)
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, 0)
	gl.BlitFramebuffer(0, 0, int32(width), int32(height), 0, 0, int32(width), int32(height), gl.COLOR_BUFFER_BIT, gl.NEAREST)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
}

func (c *esContext) FramebufferBinding() uint32 {
	var fb int32
	gl.GetIntegerv(gl.FRAMEBUFFER_BINDING, &fb)
	return uint32(fb)
}

func (c *esContext) BlendEnabled() bool {
	return gl.IsEnabled(gl.BLEND)
}

func (c *esContext) GLInfo() (version, renderer string) {
	str := func(name uint32) string {
		if s := gl.GetString(name); s != nil {
			return gl.GoStr(s)
		}
		return ""
	}
	return str(gl.VERSION), str(gl.RENDERER)
}

func (c *esContext) AllocTexture(width, height int) {
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(width), int32(height), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
}

// SetReadBuffer: GL ES reads the back buffer of the default framebuffer
// already, and ES 2 has no glReadBuffer.
func (c *esContext) SetReadBuffer(bool) {}

// UniformFound: GL gives -1 for a uniform the program does not use.
func (c *esContext) UniformFound(u Uniform) bool {
	return u >= 0
}
