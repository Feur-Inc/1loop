// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): not part of upstream Fyne.

package canvas

import (
	"sync"

	"fyne.io/fyne/v2"
)

var _ fyne.CanvasObject = (*Shader)(nil)

// Shader fills its area with a GLSL fragment shader. Source is the shader
// without its #version line, which the painter adds for the GL in use
// (with a default float precision on GL ES). The painter sets the vec2
// uniform u_resolution (the area in pixels) and u_origin (its bottom left
// corner in gl_FragCoord's pixels); Uniforms gives the others, read at each
// draw: one value for a float, two to four for a vec2 to a vec4. Painters
// without GL draw nothing.
type Shader struct {
	baseObject

	Source string

	mu       sync.Mutex
	uniforms map[string][]float32

	// OnError is called, once per Source, when it does not compile or link.
	OnError func(error)
}

// NewShader makes a shader object from a fragment shader's source.
func NewShader(source string) *Shader {
	return &Shader{Source: source}
}

// SetUniform sets a uniform for the next draws.
func (s *Shader) SetUniform(name string, values ...float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uniforms == nil {
		s.uniforms = map[string][]float32{}
	}
	s.uniforms[name] = append([]float32(nil), values...)
}

// Uniforms are the uniforms set, copied.
func (s *Shader) Uniforms() map[string][]float32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]float32, len(s.uniforms))
	for k, v := range s.uniforms {
		out[k] = v
	}
	return out
}

// Hide hides the shader.
func (s *Shader) Hide() {
	s.baseObject.Hide()
	repaint(s)
}

// Move moves the shader.
func (s *Shader) Move(pos fyne.Position) {
	if s.Position() == pos {
		return
	}
	s.baseObject.Move(pos)
	repaint(s)
}

// Refresh draws the shader again: call it for each frame of an animation.
func (s *Shader) Refresh() {
	Refresh(s)
}

// Resize resizes the shader.
func (s *Shader) Resize(size fyne.Size) {
	if s.Size() == size {
		return
	}
	s.baseObject.Resize(size)
	Refresh(s)
}
