// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// The logo at the top of the sidebar, in 1loop and in Loopy. Loopy's
// wordmark (assets/loopy-logo.png) keeps 1loop's "oop" pixels as they are
// and adds an L and a y in the logo's typeface (Space Grotesk Bold, its
// tracking): at the switch, only "1l" turns into "L", the y comes, and
// Loopy's face takes the ring's place.

const brandHeight = 30

// brandMark is both logos stacked, one shown.
type brandMark struct {
	oneloop, loopy *canvas.Image
	face           *loopyFace
	inLoopy        bool
	anim           *fyne.Animation
}

func newBrandMark(inLoopy bool) *brandMark {
	mark := &brandMark{
		oneloop: brandLogo(brandHeight*1483.0/512.0, brandHeight),
		loopy:   inkedBrand("loopy-logo.png", brandHeight*1668.0/512.0, brandHeight),
		// The ring is centred at (100, 100) of the logo's 248 high view,
		// 24 in from its edge, with a radius of 76.7; the face's ring is
		// 0.3 of its picture.
		face:    newLoopyFace(76.67*brandHeight/248/0.3, "idle", false, true),
		inLoopy: inLoopy,
	}
	mark.set(1)
	return mark
}

// set shows the mark's own logo by t, the other one by 1-t.
func (mark *brandMark) set(t float32) {
	own, other := mark.layer(mark.inLoopy), mark.layer(!mark.inLoopy)
	for _, image := range own {
		image.Translucency = float64(1 - t)
		image.Refresh()
	}
	for _, image := range other {
		image.Translucency = float64(t)
		image.Refresh()
	}
}

func (mark *brandMark) layer(loopy bool) []*canvas.Image {
	if loopy {
		return []*canvas.Image{mark.loopy, mark.face.image}
	}
	return []*canvas.Image{mark.oneloop}
}

// reveal fades from the other space's logo into this one's.
func (mark *brandMark) reveal() {
	if mark.anim != nil {
		mark.anim.Stop()
	}
	mark.set(0)
	mark.anim = motion(320*time.Millisecond, fyne.AnimationEaseInOut, mark.set)
}

func (mark *brandMark) object() fyne.CanvasObject {
	return &fyne.Container{Layout: mark, Objects: []fyne.CanvasObject{mark.oneloop, mark.loopy, mark.face.image}}
}

// MinSize is the shown logo's: 1loop's header does not change.
func (mark *brandMark) MinSize([]fyne.CanvasObject) fyne.Size {
	if mark.inLoopy {
		return mark.loopy.MinSize()
	}
	return mark.oneloop.MinSize()
}

func (mark *brandMark) Layout(_ []fyne.CanvasObject, _ fyne.Size) {
	mark.oneloop.Move(fyne.NewPos(0, 0))
	mark.oneloop.Resize(mark.oneloop.MinSize())
	mark.loopy.Move(fyne.NewPos(0, 0))
	mark.loopy.Resize(mark.loopy.MinSize())
	face := mark.face.image.MinSize()
	centre := float32(124) * brandHeight / 248
	mark.face.image.Move(fyne.NewPos(centre-face.Width/2, centre-face.Height/2))
	mark.face.image.Resize(face)
}
