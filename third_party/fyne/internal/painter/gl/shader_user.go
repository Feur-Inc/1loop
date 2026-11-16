// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): draws canvas.Shader.

package gl

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// userProgram is a canvas.Shader's source compiled, or the error it gave.
type userProgram struct {
	state  ProgramState
	failed bool
}

const userVertex = `attribute vec2 vert;
attribute vec2 normal;
void main() {
    gl_Position = vec4(vert+normal, 0, 1);
}
`

func (p *painter) userProgram(s *canvas.Shader) *userProgram {
	if p.userPrograms == nil {
		p.userPrograms = map[string]*userProgram{}
	}
	if prog, ok := p.userPrograms[s.Source]; ok {
		return prog
	}
	prog := &userProgram{}
	p.userPrograms[s.Source] = prog
	fail := func(err error) {
		prog.failed = true
		if s.OnError != nil {
			s.OnError(err)
		}
	}
	vert, err := p.compileShader(userShaderHeader+userVertex, vertexShader)
	if err != nil {
		fail(err)
		return prog
	}
	frag, err := p.compileShader(userShaderHeader+userFragmentPrecision+s.Source, fragmentShader)
	if err != nil {
		fail(err)
		return prog
	}
	ref := p.ctx.CreateProgram()
	p.ctx.AttachShader(ref, vert)
	p.ctx.AttachShader(ref, frag)
	p.ctx.LinkProgram(ref)
	if p.ctx.GetProgrami(ref, linkStatus) == glFalse {
		fail(&linkError{p.ctx.GetProgramInfoLog(ref)})
		return prog
	}
	prog.state = ProgramState{ref: ref, buff: p.createBuffer(16), uniforms: map[string]*UniformState{}, attributes: map[string]Attribute{}}
	p.enableAttribArrays(prog.state, "vert", "normal")
	return prog
}

type linkError struct{ info string }

func (e *linkError) Error() string {
	return "failed to link the shader: " + strings.TrimRight(e.info, "\x00")
}

func (p *painter) drawUserShader(s *canvas.Shader, pos fyne.Position, frame fyne.Size) {
	if s.Source == "" || s.Size().IsZero() {
		return
	}
	prog := p.userProgram(s)
	if prog.failed {
		return
	}
	program := prog.state
	bounds, points := p.vecSquareCoords(pos, s, frame)
	p.ctx.UseProgram(program.ref)
	p.updateBuffer(program.buff, points)
	p.UpdateVertexArray(program, "vert", 2, 4, 0)
	p.UpdateVertexArray(program, "normal", 2, 4, 2)
	p.ctx.BlendFunc(srcAlpha, oneMinusSrcAlpha)
	p.logError()

	x1, x2, y1, y2 := p.scaleRectCoords(bounds[0], bounds[2], bounds[1], bounds[3])
	_, frameHeight := p.scaleFrameSize(frame)
	uniforms := s.Uniforms()
	uniforms["u_resolution"] = []float32{x2 - x1, y2 - y1}
	uniforms["u_origin"] = []float32{x1, frameHeight - y2}
	for name, v := range uniforms {
		u := p.ctx.GetUniformLocation(program.ref, name)
		switch len(v) {
		case 1:
			p.ctx.Uniform1f(u, v[0])
		case 2:
			p.ctx.Uniform2f(u, v[0], v[1])
		case 3:
			p.ctx.Uniform4f(u, v[0], v[1], v[2], 1)
		case 4:
			p.ctx.Uniform4f(u, v[0], v[1], v[2], v[3])
		}
	}
	p.ctx.DrawArrays(triangleStrip, 0, 4)
	p.logError()
}
