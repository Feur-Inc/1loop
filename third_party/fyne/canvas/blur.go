// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): not part of upstream Fyne.

package canvas

import (
	"sync/atomic"

	"fyne.io/fyne/v2"
)

var _ fyne.CanvasObject = (*Blur)(nil)

// Blur shows what was drawn behind it, blurred: a backdrop filter. Draw it
// before the translucent fill that tints it. The GL painter copies the
// pixels under it, blurs them on the GPU (downsampled, dual Kawase) and
// draws them back clipped to its rounded corners. Painters without GL, or
// a GL that cannot (see BlurSupported), draw nothing for it.
type Blur struct {
	baseObject

	// Radius is the blur's radius in Fyne units; 0 draws nothing.
	Radius float32
	// CornerRadius rounds every corner; the others override it for one.
	CornerRadius                                    float32
	TopLeftCornerRadius, TopRightCornerRadius       float32
	BottomLeftCornerRadius, BottomRightCornerRadius float32
}

// NewBlur makes a blur of the given radius.
func NewBlur(radius float32) *Blur {
	return &Blur{Radius: radius}
}

var blurUnsupported atomic.Bool

// BlurChanged is called when the painter learns that it cannot blur.
var BlurChanged func()

// BlurSupported says whether the painter blurs: false once it found its
// GL cannot (no framebuffer objects) or draws on the CPU (llvmpipe, Mesa's
// software renderer on Windows), where a blur would cost every frame.
func BlurSupported() bool { return !blurUnsupported.Load() }

// SetBlurUnsupported is for painters: the GL in use cannot blur.
func SetBlurUnsupported() {
	if !blurUnsupported.Swap(true) && BlurChanged != nil {
		BlurChanged()
	}
}

// Hide hides the blur.
func (b *Blur) Hide() {
	b.baseObject.Hide()
	repaint(b)
}

// Move moves the blur.
func (b *Blur) Move(pos fyne.Position) {
	if b.Position() == pos {
		return
	}
	b.baseObject.Move(pos)
	repaint(b)
}

// Refresh draws the blur again.
func (b *Blur) Refresh() {
	Refresh(b)
}

// Resize resizes the blur.
func (b *Blur) Resize(size fyne.Size) {
	if b.Size() == size {
		return
	}
	b.baseObject.Resize(size)
	Refresh(b)
}
