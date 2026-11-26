// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"1loop/assets"
)

// 1loop's dark palette.
var (
	colBack    = color.NRGBA{0x0F, 0x0F, 0x10, 0xFF}
	colSurface = color.NRGBA{0x18, 0x18, 0x1A, 0xFF}
	colRaised  = color.NRGBA{0x24, 0x24, 0x27, 0xFF}
	colBorder  = color.NRGBA{0x2C, 0x2C, 0x30, 0xFF}
	colText    = color.NRGBA{0xF3, 0xF3, 0xF3, 0xFF}
	colMuted   = color.NRGBA{0x9A, 0x9A, 0xA2, 0xFF}
	colAmber   = color.NRGBA{0xFF, 0xB0, 0x00, 0xFF}
	colOnAmber = color.NRGBA{0x17, 0x11, 0x00, 0xFF}
	colDanger  = color.NRGBA{0xFF, 0x6B, 0x5E, 0xFF}
)

// faded is c at a fraction of its opacity.
func faded(c color.NRGBA, alpha float64) color.NRGBA {
	c.A = uint8(float64(c.A) * min(max(alpha, 0), 1))
	return c
}

// mix goes from a to b.
func mix(a, b color.NRGBA, t float64) color.NRGBA {
	at := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{at(a.R, b.R), at(a.G, b.G), at(a.B, b.B), at(a.A, b.A)}
}

// painter draws in units of a 96 dpi screen on an image scaled for the real
// one. Everything drawn takes its alpha and is moved down by dy: a screen
// fades in as it rises.
type painter struct {
	img   *image.RGBA
	scale float64
	alpha float64
	dy    float64
	fonts *fontSet
}

type fontKey struct {
	bold bool
	size float64
}

type fontSet struct {
	regular, bold *opentype.Font
	faces         map[fontKey]font.Face
	art           map[string]image.Image // decoded, then scaled, artwork
	scaled        map[image.Point]image.Image
	back          *image.RGBA
}

// newFonts takes the system's fonts, and Go's when they cannot be read.
func newFonts(regular, bold []byte) *fontSet {
	set := &fontSet{faces: map[fontKey]font.Face{}, art: map[string]image.Image{}, scaled: map[image.Point]image.Image{}}
	parse := func(data, fallback []byte) *opentype.Font {
		if parsed, err := opentype.Parse(data); err == nil {
			return parsed
		}
		parsed, _ := opentype.Parse(fallback)
		return parsed
	}
	set.regular, set.bold = parse(regular, goregular.TTF), parse(bold, gobold.TTF)
	return set
}

func (p *painter) face(size float64, bold bool) font.Face {
	key := fontKey{bold, size * p.scale}
	if face := p.fonts.faces[key]; face != nil {
		return face
	}
	source := p.fonts.regular
	if bold {
		source = p.fonts.bold
	}
	face, err := opentype.NewFace(source, &opentype.FaceOptions{Size: key.size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err)
	}
	p.fonts.faces[key] = face
	return face
}

func (p *painter) width(text string, size float64, bold bool) float64 {
	return float64(font.MeasureString(p.face(size, bold), text)) / 64 / p.scale
}

// text draws one line, its middle at y, from x rightwards (align 0), around
// it (0.5) or up to it (1).
func (p *painter) text(text string, x, y, size float64, bold bool, c color.NRGBA, align float64) {
	x -= p.width(text, size, bold) * align
	y += size*0.36 + p.dy // from the middle to the baseline
	drawer := font.Drawer{Dst: p.img, Src: image.NewUniform(faded(c, p.alpha)), Face: p.face(size, bold),
		Dot: fixed.Point26_6{X: fixed.Int26_6(x * p.scale * 64), Y: fixed.Int26_6(math.Round(y*p.scale) * 64)}}
	drawer.DrawString(text)
}

// cut shortens text in its middle to fit width.
func (p *painter) cut(text string, width, size float64) string {
	if p.width(text, size, false) <= width {
		return text
	}
	runes := []rune(text)
	for keep := len(runes) - 1; keep > 4; keep-- {
		short := string(runes[:keep/2]) + "…" + string(runes[len(runes)-keep+keep/2:])
		if p.width(short, size, false) <= width {
			return short
		}
	}
	return text
}

// shape is a path for the rasterizer, moved to its box's corner.
type shape struct {
	*vector.Rasterizer
	x, y float32
}

func (s shape) MoveTo(x, y float32) { s.Rasterizer.MoveTo(x-s.x, y-s.y) }
func (s shape) LineTo(x, y float32) { s.Rasterizer.LineTo(x-s.x, y-s.y) }
func (s shape) CubeTo(a, b, c, d, e, f float32) {
	s.Rasterizer.CubeTo(a-s.x, b-s.y, c-s.x, d-s.y, e-s.x, f-s.y)
}

// fill fills a path within a box of pixels: only that box is worked on.
func (p *painter) fill(left, top, right, bottom float64, c color.NRGBA, path func(shape)) {
	box := image.Rect(int(math.Floor(left)), int(math.Floor(top)), int(math.Ceil(right)), int(math.Ceil(bottom)))
	if box.Empty() || c.A == 0 {
		return
	}
	raster := vector.NewRasterizer(box.Dx(), box.Dy())
	path(shape{raster, float32(box.Min.X), float32(box.Min.Y)})
	raster.Draw(p.img, box, image.NewUniform(faded(c, p.alpha)), image.Point{})
}

// roundRect fills a rounded rectangle; a border, when it has one, is a
// larger one under it.
func (p *painter) roundRect(x, y, w, h, radius float64, c color.NRGBA) {
	s := p.scale
	x, y, w, h, radius = x*s, (y+p.dy)*s, w*s, h*s, min(radius, w/2, h/2)*s
	const k = 0.5523
	p.fill(x, y, x+w, y+h, c, func(r shape) {
		f := func(v float64) float32 { return float32(v) }
		c := radius * (1 - k)
		r.MoveTo(f(x+radius), f(y))
		r.LineTo(f(x+w-radius), f(y))
		r.CubeTo(f(x+w-c), f(y), f(x+w), f(y+c), f(x+w), f(y+radius))
		r.LineTo(f(x+w), f(y+h-radius))
		r.CubeTo(f(x+w), f(y+h-c), f(x+w-c), f(y+h), f(x+w-radius), f(y+h))
		r.LineTo(f(x+radius), f(y+h))
		r.CubeTo(f(x+c), f(y+h), f(x), f(y+h-c), f(x), f(y+h-radius))
		r.LineTo(f(x), f(y+radius))
		r.CubeTo(f(x), f(y+c), f(x+c), f(y), f(x+radius), f(y))
		r.ClosePath()
	})
}

func (p *painter) card(x, y, w, h, radius float64, fill, border color.NRGBA) {
	p.roundRect(x, y, w, h, radius, border)
	p.roundRect(x+1, y+1, w-2, h-2, radius-1, fill)
}

// stroke draws a polyline with round ends.
func (p *painter) stroke(points [][2]float64, width float64, c color.NRGBA) {
	s, half := p.scale, width*p.scale/2
	left, top, right, bottom := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		x, y := point[0]*s, (point[1]+p.dy)*s
		left, top, right, bottom = min(left, x-half), min(top, y-half), max(right, x+half), max(bottom, y+half)
	}
	p.fill(left, top, right, bottom, c, func(r shape) {
		for i := 0; i+1 < len(points); i++ {
			x1, y1 := points[i][0]*s, (points[i][1]+p.dy)*s
			x2, y2 := points[i+1][0]*s, (points[i+1][1]+p.dy)*s
			angle := math.Atan2(y2-y1, x2-x1)
			const steps = 12
			first := true
			add := func(x, y float64) {
				if first {
					r.MoveTo(float32(x), float32(y))
					first = false
				} else {
					r.LineTo(float32(x), float32(y))
				}
			}
			for j := 0; j <= steps; j++ {
				a := angle - math.Pi/2 + math.Pi*float64(j)/steps
				add(x2+half*math.Cos(a), y2+half*math.Sin(a))
			}
			for j := 0; j <= steps; j++ {
				a := angle + math.Pi/2 + math.Pi*float64(j)/steps
				add(x1+half*math.Cos(a), y1+half*math.Sin(a))
			}
			r.ClosePath()
		}
	})
}

// arc draws part of a ring, clockwise from angle from (0 is up), round ended.
func (p *painter) arc(cx, cy, radius, width, from, sweep float64, c color.NRGBA) {
	if sweep <= 0 {
		return
	}
	s := p.scale
	cx, cy, radius, half := cx*s, (cy+p.dy)*s, radius*s, width*s/2
	steps := max(2, int(sweep/math.Pi*90))
	at := func(angle, radius float64) (float64, float64) {
		return cx + radius*math.Sin(angle), cy - radius*math.Cos(angle)
	}
	reach := radius + half
	p.fill(cx-reach, cy-reach, cx+reach, cy+reach, c, func(r shape) {
		first := true
		add := func(x, y float64) {
			if first {
				r.MoveTo(float32(x), float32(y))
				first = false
			} else {
				r.LineTo(float32(x), float32(y))
			}
		}
		for i := 0; i <= steps; i++ {
			add(at(from+sweep*float64(i)/float64(steps), radius+half))
		}
		ex, ey := at(from+sweep, radius)
		for i := 1; i < 10; i++ { // the round end
			a := from + sweep + math.Pi*float64(i)/10
			add(ex+half*math.Sin(a), ey-half*math.Cos(a))
		}
		for i := steps; i >= 0; i-- {
			add(at(from+sweep*float64(i)/float64(steps), radius-half))
		}
		bx, by := at(from, radius)
		for i := 1; i < 10; i++ {
			a := from + math.Pi + math.Pi*float64(i)/10
			add(bx+half*math.Sin(a), by-half*math.Cos(a))
		}
		r.ClosePath()
	})
}

// artwork draws one of assets' images, its centre at x, y, h units high.
func (p *painter) artwork(name string, x, y, h float64) {
	source := p.fonts.art[name]
	if source == nil {
		data, err := assets.Artwork.ReadFile(name)
		if err != nil {
			return
		}
		if source, err = png.Decode(bytes.NewReader(data)); err != nil {
			return
		}
		p.fonts.art[name] = source
	}
	bounds := source.Bounds()
	height := int(math.Round(h * p.scale))
	size := image.Pt(height*bounds.Dx()/bounds.Dy(), height)
	scaled := p.fonts.scaled[size]
	if scaled == nil {
		to := image.NewRGBA(image.Rectangle{Max: size})
		xdraw.CatmullRom.Scale(to, to.Bounds(), source, bounds, xdraw.Src, nil)
		p.fonts.scaled[size], scaled = to, to
	}
	corner := image.Pt(int(math.Round(x*p.scale))-size.X/2, int(math.Round((y+p.dy)*p.scale))-size.Y/2)
	draw.DrawMask(p.img, image.Rectangle{Min: corner, Max: corner.Add(size)}, scaled, image.Point{},
		image.NewUniform(color.Alpha{uint8(255 * p.alpha)}), image.Point{}, draw.Over)
}

// backdrop is the window's background: near black, an amber glow behind the
// artwork, a hairline around.
func (p *painter) backdrop(w, h, gx, gy float64) {
	bounds := p.img.Bounds()
	if back := p.fonts.back; back != nil && back.Bounds() == bounds {
		copy(p.img.Pix, back.Pix)
		return
	}
	reach := 300 * p.scale
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			d := math.Hypot(float64(x)-gx*p.scale, float64(y)-gy*p.scale) / reach
			light := 0.0
			if d < 1 {
				light = (1 - d) * (1 - d) * 0.13
			}
			// A little noise, fixed by position, hides the gradient's steps.
			noise := (float64((x*7919+y*104729)%17)/17 - .5) / 255
			mixed := func(a, b uint8) uint8 {
				v := (float64(a)+(float64(b)-float64(a))*light)/255 + noise*min(light*40, 1)
				return uint8(math.Round(min(max(v, 0), 1) * 255))
			}
			p.img.SetRGBA(x, y, color.RGBA{mixed(colBack.R, colAmber.R), mixed(colBack.G, colAmber.G), mixed(colBack.B, colAmber.B), 255})
		}
	}
	border := image.NewUniform(colBorder)
	for _, side := range []image.Rectangle{
		image.Rect(0, 0, bounds.Max.X, 1), image.Rect(0, bounds.Max.Y-1, bounds.Max.X, bounds.Max.Y),
		image.Rect(0, 0, 1, bounds.Max.Y), image.Rect(bounds.Max.X-1, 0, bounds.Max.X, bounds.Max.Y)} {
		draw.Draw(p.img, side, border, image.Point{}, draw.Src)
	}
	p.fonts.back = image.NewRGBA(bounds)
	copy(p.fonts.back.Pix, p.img.Pix)
}
