// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): draws canvas.Blur.

package gl

import (
	"math"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// blurContext is what the blur needs beyond Fyne's context: the desktop
// GL and GL ES have it; the browser's and gomobile's draw no blur.
// blendCap is GL_BLEND, the same in every GL.
const blendCap = 0x0BE2

type blurContext interface {
	AllocTexture(width, height int)
	CopyTexSubImage2D(x, y, width, height int)
	CreateFramebuffer() uint32
	BindFramebuffer(fb uint32)
	FramebufferTexture(texture Texture) bool
	FramebufferBinding() uint32
	BlendEnabled() bool
	SetReadBuffer(back bool)
	GLInfo() (version, renderer string)
}

// blurLevel is one step of the blur's chain: level 0 is the pixels copied
// from the frame, each next one half as large.
type blurLevel struct {
	texture       Texture
	fb            uint32
	width, height int
}

type blurState struct {
	ctx             blurContext
	down, up, final ProgramState
	quad            Buffer
	levels          []blurLevel
}

const blurVertex = `attribute vec2 vert;
void main() {
    gl_Position = vec4(vert, 0, 1);
}
`

// Dual Kawase (Marius Bjørge, SIGGRAPH 2015): a few taps a pixel, each step
// at half the size of the last, so a wide blur stays cheap.
const blurFragment = `uniform sampler2D tex;
uniform vec2 texel;    // a pixel of tex, in uv
uniform vec4 region;   // where tex lies in gl_FragCoord: x, y, width, height
uniform float spread;
#ifdef FINAL
uniform vec4 rect;     // the blur's own area: x1, y1, x2, y2 in gl_FragCoord
uniform vec4 radius;   // top right, bottom right, top left, bottom left
float rounded(vec2 p, vec2 b, vec4 r) {
    r.xy = (p.x > 0.0) ? r.xy : r.zw;
    r.x = (p.y > 0.0) ? r.x : r.y;
    vec2 d = abs(p) - b + r.x;
    return min(max(d.x, d.y), 0.0) + length(max(d, 0.0)) - r.x;
}
#endif
void main() {
    vec2 uv = (gl_FragCoord.xy - region.xy) / region.zw;
    vec2 h = texel * spread;
#ifdef DOWN
    vec4 sum = texture2D(tex, uv) * 4.0;
    sum += texture2D(tex, uv - h);
    sum += texture2D(tex, uv + h);
    sum += texture2D(tex, uv + vec2(h.x, -h.y));
    sum += texture2D(tex, uv - vec2(h.x, -h.y));
    vec4 colour = sum / 8.0;
#else
    vec4 sum = texture2D(tex, uv + vec2(-h.x * 2.0, 0.0));
    sum += texture2D(tex, uv + vec2(-h.x, h.y)) * 2.0;
    sum += texture2D(tex, uv + vec2(0.0, h.y * 2.0));
    sum += texture2D(tex, uv + vec2(h.x, h.y)) * 2.0;
    sum += texture2D(tex, uv + vec2(h.x * 2.0, 0.0));
    sum += texture2D(tex, uv + vec2(h.x, -h.y)) * 2.0;
    sum += texture2D(tex, uv + vec2(0.0, -h.y * 2.0));
    sum += texture2D(tex, uv + vec2(-h.x, -h.y)) * 2.0;
    vec4 colour = sum / 12.0;
#endif
#ifdef FINAL
    vec2 centre = (rect.xy + rect.zw) * 0.5;
    float d = rounded(gl_FragCoord.xy - centre, (rect.zw - rect.xy) * 0.5, radius);
    gl_FragColor = vec4(colour.rgb, 1.0 - smoothstep(-0.5, 0.5, d));
#else
    gl_FragColor = vec4(colour.rgb, 1.0);
#endif
}
`

// blurUsable says whether this GL may blur: framebuffer objects (GL 3, or
// any GL ES 2) and a GPU. On a CPU renderer the copy and passes would
// cost every frame, for a look the plain tint gives well enough.
func blurUsable(version, renderer string) bool {
	low := strings.ToLower(renderer)
	for _, soft := range []string{"llvmpipe", "softpipe", "software", "swiftshader", "gdi generic", "swrast"} {
		if strings.Contains(low, soft) {
			return false
		}
	}
	es := strings.HasPrefix(version, "OpenGL ES ")
	version = strings.TrimPrefix(version, "OpenGL ES ")
	major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if err != nil {
		return false
	}
	return major >= 3 || (es && major >= 2)
}

func (p *painter) blurInit() *blurState {
	if p.blur != nil || p.blurFailed {
		return p.blur
	}
	fail := func(why string) *blurState {
		p.blurFailed = true
		fyne.LogError("1loop: no blur: "+why, nil)
		canvas.SetBlurUnsupported()
		return nil
	}
	ctx, ok := p.ctx.(blurContext)
	if !ok {
		return fail("this GL has no framebuffer objects")
	}
	if version, renderer := ctx.GLInfo(); !blurUsable(version, renderer) {
		return fail("GL " + version + " on " + renderer)
	}
	b := &blurState{ctx: ctx, quad: p.createBuffer(8)}
	for _, prog := range []struct {
		state  *ProgramState
		define string
	}{{&b.down, "DOWN"}, {&b.up, "UP"}, {&b.final, "FINAL"}} {
		vert, err := p.compileShader(userShaderHeader+blurVertex, vertexShader)
		if err != nil {
			return fail(err.Error())
		}
		frag, err := p.compileShader(userShaderHeader+userFragmentPrecision+"#define "+prog.define+"\n"+blurFragment, fragmentShader)
		if err != nil {
			return fail(err.Error())
		}
		ref := p.ctx.CreateProgram()
		p.ctx.AttachShader(ref, vert)
		p.ctx.AttachShader(ref, frag)
		p.ctx.LinkProgram(ref)
		if p.ctx.GetProgrami(ref, linkStatus) == glFalse {
			return fail(p.ctx.GetProgramInfoLog(ref))
		}
		*prog.state = ProgramState{ref: ref, buff: b.quad, uniforms: map[string]*UniformState{}, attributes: map[string]Attribute{}}
		p.getUniformLocations(*prog.state, "texel", "region", "spread", "rect", "radius")
		p.enableAttribArrays(*prog.state, "vert")
	}
	p.blur = b
	return b
}

// level is the blur's level i, made or resized to width × height.
func (b *blurState) level(p *painter, i, width, height int) (blurLevel, bool) {
	for len(b.levels) <= i {
		b.levels = append(b.levels, blurLevel{texture: p.newTexture(canvas.ImageScaleSmooth)})
	}
	l := &b.levels[i]
	p.ctx.ActiveTexture(texture0)
	p.ctx.BindTexture(texture2D, l.texture)
	if l.width != width || l.height != height {
		l.width, l.height = width, height
		b.ctx.AllocTexture(width, height)
		if i > 0 {
			if l.fb == 0 {
				l.fb = b.ctx.CreateFramebuffer()
			}
			b.ctx.BindFramebuffer(l.fb)
			if !b.ctx.FramebufferTexture(l.texture) {
				return *l, false
			}
		}
	}
	return *l, true
}

func (p *painter) drawBlur(obj *canvas.Blur, pos fyne.Position, frame fyne.Size) {
	if obj.Radius <= 0 || obj.Size().IsZero() {
		return
	}
	b := p.blurInit()
	if b == nil {
		return
	}
	fw, fh := p.scaleFrameSize(frame)
	x1, x2, y1, y2 := p.scaleRectCoords(pos.X, pos.X+obj.Size().Width, pos.Y, pos.Y+obj.Size().Height)
	// In gl_FragCoord: from the bottom.
	rx1, ry1, rx2, ry2 := bmax(0, x1), bmax(0, fh-y2), bmin(fw, x2), bmin(fh, fh-y1)
	if rx2-rx1 < 2 || ry2-ry1 < 2 {
		return
	}
	radius := obj.Radius * p.pixScale
	// Each step halves the size; the last steps' spread makes up the rest.
	steps := int(math.Ceil(math.Log2(float64(radius) / 2)))
	steps = bmax(1, bmin(steps, 6))
	spread := bmax(0.5, bmin(radius/float32(int(1)<<(steps+1)), 2.5))
	// The copy reaches past the edges so they blur from what is beside them.
	pad := float32(math.Ceil(float64(radius) * 1.5))
	cx1, cy1, cx2, cy2 := int(bmax(0, rx1-pad)), int(bmax(0, ry1-pad)), int(bmin(fw, rx2+pad)), int(bmin(fh, ry2+pad))
	cw, ch := cx2-cx1, cy2-cy1

	prevFB := b.ctx.FramebufferBinding()
	blend := b.ctx.BlendEnabled()
	p.ctx.Disable(scissorTest)
	defer func() {
		b.ctx.BindFramebuffer(prevFB)
		p.ctx.Viewport(0, 0, p.outWidth, p.outHeight)
		if p.clipping {
			p.ctx.Scissor(p.clip[0], p.clip[1], p.clip[2], p.clip[3])
			p.ctx.Enable(scissorTest)
		}
		if blend {
			p.ctx.Enable(blendCap)
		}
		p.logError()
	}()

	level0, _ := b.level(p, 0, cw, ch)
	b.ctx.SetReadBuffer(prevFB == 0)
	b.ctx.CopyTexSubImage2D(cx1, cy1, cw, ch)
	p.logError()
	p.updateBuffer(b.quad, []float32{-1, -1, 1, -1, -1, 1, 1, 1})
	p.ctx.Disable(blendCap)

	pass := func(prog ProgramState, src, dst blurLevel) {
		b.ctx.BindFramebuffer(dst.fb)
		p.ctx.Viewport(0, 0, dst.width, dst.height)
		p.ctx.UseProgram(prog.ref)
		p.UpdateVertexArray(prog, "vert", 2, 2, 0)
		p.ctx.ActiveTexture(texture0)
		p.ctx.BindTexture(texture2D, src.texture)
		p.SetUniform2f(prog, "texel", 1/float32(src.width), 1/float32(src.height))
		p.SetUniform4f(prog, "region", 0, 0, float32(dst.width), float32(dst.height))
		p.SetUniform1f(prog, "spread", spread)
		p.ctx.DrawArrays(triangleStrip, 0, 4)
	}
	chain := []blurLevel{level0}
	for i := 1; i <= steps; i++ {
		l, ok := b.level(p, i, bmax(1, cw>>i), bmax(1, ch>>i))
		if !ok {
			p.blurFailed, p.blur = true, nil
			canvas.SetBlurUnsupported()
			return
		}
		chain = append(chain, l)
		pass(b.down, chain[i-1], l)
	}
	for i := steps - 1; i >= 1; i-- {
		pass(b.up, chain[i+1], chain[i])
	}

	// The last step up is drawn into the frame, clipped to the blur's shape.
	b.ctx.BindFramebuffer(prevFB)
	p.ctx.Viewport(0, 0, p.outWidth, p.outHeight)
	if p.clipping {
		p.ctx.Scissor(p.clip[0], p.clip[1], p.clip[2], p.clip[3])
		p.ctx.Enable(scissorTest)
	}
	p.ctx.Enable(blendCap)
	p.ctx.BlendFunc(srcAlpha, oneMinusSrcAlpha)
	nx := func(x float32) float32 { return x/fw*2 - 1 }
	ny := func(y float32) float32 { return y/fh*2 - 1 }
	p.updateBuffer(b.quad, []float32{nx(rx1), ny(ry1), nx(rx2), ny(ry1), nx(rx1), ny(ry2), nx(rx2), ny(ry2)})
	prog := b.final
	p.ctx.UseProgram(prog.ref)
	p.UpdateVertexArray(prog, "vert", 2, 2, 0)
	p.ctx.ActiveTexture(texture0)
	p.ctx.BindTexture(texture2D, chain[1].texture)
	p.SetUniform2f(prog, "texel", 1/float32(chain[1].width), 1/float32(chain[1].height))
	p.SetUniform4f(prog, "region", float32(cx1), float32(cy1), float32(cw), float32(ch))
	p.SetUniform1f(prog, "spread", spread)
	p.SetUniform4f(prog, "rect", x1, fh-y2, x2, fh-y1)
	corner := func(one float32) float32 {
		if one == 0 {
			one = obj.CornerRadius
		}
		return bmin(one*p.pixScale, (x2-x1)/2, (y2-y1)/2)
	}
	p.SetUniform4f(prog, "radius", corner(obj.TopRightCornerRadius), corner(obj.BottomRightCornerRadius),
		corner(obj.TopLeftCornerRadius), corner(obj.BottomLeftCornerRadius))
	p.ctx.DrawArrays(triangleStrip, 0, 4)
}

// bmax and bmin: this module builds as Go 1.19, before the built-in ones.
func bmax[T int | float32](a T, more ...T) T {
	for _, b := range more {
		if b > a {
			a = b
		}
	}
	return a
}

func bmin[T int | float32](a T, more ...T) T {
	for _, b := range more {
		if b < a {
			a = b
		}
	}
	return a
}
