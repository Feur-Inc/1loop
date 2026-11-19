//go:build (!gles && !arm && !arm64 && !android && !ios && !mobile && !test_web_driver && !wasm) || (darwin && !mobile && !ios && !wasm && !test_web_driver)

// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): what canvas.Blur needs from GL.

package gl

import (
	"github.com/go-gl/gl/v2.1/gl"
)

var _ blurContext = (*coreContext)(nil)

func (c *coreContext) CopyTexSubImage2D(x, y, width, height int) {
	gl.CopyTexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, int32(x), int32(y), int32(width), int32(height))
}

func (c *coreContext) CreateFramebuffer() uint32 {
	var fb uint32
	gl.GenFramebuffers(1, &fb)
	return fb
}

func (c *coreContext) BindFramebuffer(fb uint32) {
	gl.BindFramebuffer(gl.FRAMEBUFFER, fb)
}

func (c *coreContext) FramebufferTexture(texture Texture) bool {
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, uint32(texture), 0)
	return gl.CheckFramebufferStatus(gl.FRAMEBUFFER) == gl.FRAMEBUFFER_COMPLETE
}

func (c *coreContext) FramebufferBinding() uint32 {
	var fb int32
	gl.GetIntegerv(gl.FRAMEBUFFER_BINDING, &fb)
	return uint32(fb)
}

func (c *coreContext) BlendEnabled() bool {
	return gl.IsEnabled(gl.BLEND)
}

func (c *coreContext) GLInfo() (version, renderer string) {
	str := func(name uint32) string {
		if s := gl.GetString(name); s != nil {
			return gl.GoStr(s)
		}
		return ""
	}
	return str(gl.VERSION), str(gl.RENDERER)
}

func (c *coreContext) AllocTexture(width, height int) {
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(width), int32(height), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
}

func (c *coreContext) SetReadBuffer(back bool) {
	if back {
		gl.ReadBuffer(gl.BACK)
	}
}

// UniformFound: GL gives -1 for a uniform the program does not use.
func (c *coreContext) UniformFound(u Uniform) bool {
	return u >= 0
}
