// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #474): the retained frame.

package gl

import (
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// retainContext is what the retained frame needs beyond Fyne's context:
// the desktop GL and GL ES have it; the browser's and gomobile's paint
// straight into the window as before.
type retainContext interface {
	AllocTexture(width, height int)
	CreateFramebuffer() uint32
	BindFramebuffer(fb uint32)
	FramebufferTexture(texture Texture) bool
	BlitToWindow(fb uint32, width, height int)
	GLInfo() (version, renderer string)
}

// retained is a window's frame, painted into a texture that is kept from
// one frame to the next and copied into the window to be shown. What the
// window's own back buffer holds after a swap is undefined, so this is
// what lets a frame paint only the part of the window that changed (a
// caret blinking repainted the whole window, sidebar, transcript and
// shader).
type retained struct {
	ctx           retainContext
	texture       Texture
	fb            uint32
	width, height int
	held          bool // it holds the last frame painted
}

// retainUsable says whether this GL may retain the frame: framebuffer
// objects and their blit (GL 3, GL ES 3). Software renderers too: they
// are where painting less saves the most.
func retainUsable(version string) bool {
	version = strings.TrimPrefix(version, "OpenGL ES ")
	major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	return err == nil && major >= 3
}

// StartRetained has the frame painted into the retained texture, made or
// resized to the output's size. held says whether it holds the last frame
// painted, which a frame may then paint over only in part. ok is false
// where the frame cannot be retained: it is painted into the window, as
// Fyne did.
func (p *painter) StartRetained() (held, ok bool) {
	if p.retainFailed {
		return false, false
	}
	fail := func(why string) (bool, bool) {
		p.retainFailed, p.retain = true, nil
		fyne.LogError("1loop: frames painted whole: "+why, nil)
		return false, false
	}
	r := p.retain
	if r == nil {
		ctx, ok := p.ctx.(retainContext)
		if !ok {
			return fail("this GL has no framebuffer objects")
		}
		if version, renderer := ctx.GLInfo(); !retainUsable(version) {
			return fail("GL " + version + " on " + renderer)
		}
		r = &retained{ctx: ctx, texture: p.newTexture(canvas.ImageScaleFastest), fb: ctx.CreateFramebuffer()}
		p.retain = r
	}
	if r.width != p.outWidth || r.height != p.outHeight {
		r.width, r.height, r.held = p.outWidth, p.outHeight, false
		p.ctx.ActiveTexture(texture0)
		p.ctx.BindTexture(texture2D, r.texture)
		r.ctx.AllocTexture(r.width, r.height)
		r.ctx.BindFramebuffer(r.fb)
		if !r.ctx.FramebufferTexture(r.texture) {
			r.ctx.BindFramebuffer(0)
			return fail("the frame's framebuffer is incomplete")
		}
		p.logError()
		return false, true
	}
	r.ctx.BindFramebuffer(r.fb)
	return r.held, true
}

// EndRetained copies the frame painted into the window's back buffer,
// ready to be shown, and paints into the window again.
func (p *painter) EndRetained() {
	r := p.retain
	if r == nil {
		return
	}
	p.ctx.Disable(scissorTest) // a blit is clipped too
	r.ctx.BlitToWindow(r.fb, r.width, r.height)
	if p.clipping {
		p.ctx.Enable(scissorTest)
	}
	p.logError()
	r.held = true
}

// ForgetRetained says a frame was painted into the window, not into the
// retained frame, which no longer holds the last one.
func (p *painter) ForgetRetained() {
	if p.retain != nil {
		p.retain.held = false
	}
}
