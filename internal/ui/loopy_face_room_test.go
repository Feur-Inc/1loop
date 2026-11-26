// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"image"
	"math"
	"testing"
)

// highest is the first row with ink, from the top, in a share of the face.
func highest(img image.Image, px int) float64 {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x2000 {
				return float64(y) / float64(px)
			}
		}
	}
	return 1
}

// Every state's moves, springs and all, stay inside the room the big face
// is drawn with: a jump is never cut at the picture's top.
func TestLoopyFaceJumpsStayInside(t *testing.T) {
	const px, head = 60, bigFaceHead
	worst, which := 1.0, ""
	for name, state := range faceStates {
		face := &loopyFace{pose: restPose(), head: head}
		for t := 0.0; t < state.period*2; t += 1.0 / 30 {
			target := state.pose(math.Mod(t, state.period))
			for i := 0; i < 4; i++ {
				face.step(target, state, 1.0/120)
			}
			if top := highest(drawLoopy(face.pose, px, false, head), px); top < worst {
				worst, which = top, name
			}
		}
	}
	t.Logf("highest ink: %.3f of the face from the top, in %q", worst, which)
	if worst < 0.01 {
		t.Errorf("%q reaches the picture's top edge: it is cut", which)
	}
}
