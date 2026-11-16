// SPDX-License-Identifier: MPL-2.0

package ui

// Soft drop shadows and glows: one picture each, drawn by 1loop at the
// screen's own resolution, fading out as a blurred shape does.
//
// They used to be four translucent rounded rectangles, each 3 px larger
// than the last, standing in for a blur: their edges showed as steps, the
// last one as a hard line where the composer's amber halo ended. Here the
// fade is continuous, the same on every side, at any scale.

import (
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// shadowInk is the black of shadows: blue 1, which no theme token is, so a
// change of theme never maps it to the new theme's overlay (white on dark,
// #436). Their opacity is set again for the theme instead.
var shadowInk = color.NRGBA{B: 1}

const (
	// shadowSigma is how soft the edge is, in points: the blur's standard
	// deviation. The fade is all but gone 3σ out.
	shadowSigma = 5.5
	// shadowPeak and shadowCap make a shadow of strength 1 as dark, and
	// reach as far, as the four rectangles did: about 0.28 at the edge.
	shadowPeak = 0.82
	shadowCap  = 0.28
)

// shadowMargin is how far past its shape a shadow is drawn.
var shadowMargin = float32(math.Ceil(3 * shadowSigma))

type shadow struct {
	raster   *canvas.Raster
	tint     color.NRGBA // black for a shadow, a colour for a glow (newGlow)
	strength float32
	size     fyne.Size // the shape's, in points
	radius   float32

	cache    *image.NRGBA // the last picture, kept while nothing changed
	cacheKey shadowKey
}

type shadowKey struct {
	w, h           int
	shapeW, shapeH float32
	radius         float32
	tint           color.NRGBA
	strength       float32
}

func newShadow(strength float32) *shadow {
	dropShadow := &shadow{tint: shadowInk, strength: strength}
	dropShadow.raster = canvas.NewRaster(dropShadow.draw)
	dropShadow.raster.ScaleMode = canvas.ImageScaleSmooth
	dropShadow.setOpacity(1) // lighter on the light theme
	return dropShadow
}

// newGlow is a halo of tint around a surface, soft like newShadow's.
func newGlow(tint color.NRGBA, strength float32) *shadow {
	glow := newShadow(strength)
	glow.tint = tint
	glow.setOpacity(0)
	glows = append(glows, glow)
	return glow
}

// setOpacity fades the whole shadow, from 0 (not drawn at all) to 1.
func (dropShadow *shadow) setOpacity(o float32) {
	if !themeTokens.Dark && dropShadow.tint == shadowInk {
		o *= 0.4 // a shadow, not a glow: on white a dark one reads as a grey band
	}
	o = fyne.Min(1, fyne.Max(0, o))
	dropShadow.raster.Translucency = float64(1 - o)
	// A shadow with nothing to show is not drawn: a translucent picture
	// still costs its whole area on every frame.
	if o == 0 {
		dropShadow.raster.Hide()
	} else {
		dropShadow.raster.Show()
	}
}

// layout sets the shape the shadow is cast by: its position, size and
// corner radius, in its container's points.
func (dropShadow *shadow) layout(pos fyne.Position, size fyne.Size, radius float32) {
	dropShadow.size, dropShadow.radius = size, radius
	dropShadow.raster.Move(pos.SubtractXY(shadowMargin, shadowMargin))
	dropShadow.raster.Resize(size.AddWidthHeight(2*shadowMargin, 2*shadowMargin))
}

func (dropShadow *shadow) objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{dropShadow.raster}
}

func (dropShadow *shadow) refresh() { dropShadow.raster.Refresh() }

// draw is the raster's picture at w × h pixels.
func (dropShadow *shadow) draw(w, h int) image.Image {
	key := shadowKey{w, h, dropShadow.size.Width, dropShadow.size.Height, dropShadow.radius, dropShadow.tint, dropShadow.strength}
	if dropShadow.cache != nil && dropShadow.cacheKey == key {
		return dropShadow.cache
	}
	img := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	full := dropShadow.size.AddWidthHeight(2*shadowMargin, 2*shadowMargin)
	if w > 0 && h > 0 && full.Width > 0 {
		paintSoftShape(img, float32(w)/full.Width, dropShadow.size, dropShadow.radius, dropShadow.tint, dropShadow.strength)
	}
	dropShadow.cache, dropShadow.cacheKey = img, key
	return img
}

// paintSoftShape draws a rounded rectangle of size and radius, in points,
// blurred, centred in img at scale pixels per point: the tint at
// strength's opacity inside, fading out past its edge.
func paintSoftShape(img *image.NRGBA, scale float32, size fyne.Size, radius float32, tint color.NRGBA, strength float32) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	sigma := shadowSigma * scale
	hw, hh := size.Width*scale/2, size.Height*scale/2
	r := fyne.Min(radius*scale, fyne.Min(hw, hh))
	cx, cy := float32(w)/2, float32(h)/2
	peak := shadowPeak * strength
	ceiling := fyne.Min(1, shadowCap*strength)

	// alpha of a pixel d pixels outside the shape (negative inside): the
	// edge of a shape blurred by a Gaussian of sigma.
	const steps = 1024
	reach := 4 * sigma
	var table [steps + 1]uint8
	for i := range table {
		d := -reach + 2*reach*float32(i)/steps
		a := peak * float32(0.5*math.Erfc(float64(d/(sigma*math.Sqrt2))))
		table[i] = uint8(255*fyne.Min(a, ceiling) + 0.5)
	}
	alphaAt := func(d float32) uint8 {
		i := int((d + reach) / (2 * reach) * steps)
		return table[min(max(i, 0), steps)]
	}

	// distance from a pixel's centre to the rounded rectangle
	distance := func(px, py float32) float32 {
		qx := float32(math.Abs(float64(px-cx))) - (hw - r)
		qy := float32(math.Abs(float64(py-cy))) - (hh - r)
		outside := float32(math.Hypot(float64(max(qx, 0)), float64(max(qy, 0))))
		return outside + min(max(qx, qy), 0) - r
	}

	row := func(y int, py float32) {
		line := img.Pix[y*img.Stride : y*img.Stride+4*w]
		for x := 0; x < w; x++ {
			a := alphaAt(distance(float32(x)+0.5, py))
			if a == 0 {
				continue
			}
			line[4*x], line[4*x+1], line[4*x+2], line[4*x+3] = tint.R, tint.G, tint.B, a
		}
	}
	// The rows far enough from the top and bottom edges are all alike: the
	// first such row is drawn, the others copied from it.
	var middle []uint8
	for y := 0; y < h; y++ {
		py := float32(y) + 0.5
		if float32(math.Abs(float64(py-cy))) < hh-r-reach {
			if middle != nil {
				copy(img.Pix[y*img.Stride:], middle)
				continue
			}
			row(y, cy) // the shape's middle: only the distance across counts
			middle = img.Pix[y*img.Stride : y*img.Stride+4*w]
			continue
		}
		row(y, py)
	}
}
