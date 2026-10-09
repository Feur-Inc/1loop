package main

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
)

// PNG artwork is rasterized from the original DA files; the intro sheet preserves its geometry.
// The work ring is drawn from the DA geometry itself (see workFrame).
//
//go:embed assets/*.png
var artwork embed.FS

func brandResource(name string) fyne.Resource {
	data, _ := artwork.ReadFile("assets/" + name)
	return fyne.NewStaticResource(name, data)
}

func brandImage(name string, width, height float32) *canvas.Image {
	image := canvas.NewImageFromResource(brandResource(name))
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(width, height))
	return image
}

// The intro sheet is an 8×8 grid of 144 px cells. It is decoded only when it
// is first shown, and the full-size sheet is dropped again once every frame
// has been resampled for the screen (see loopMark.frame), so the large pixel
// buffer does not stay in memory for the whole session.
var introSheet []image.Image

const introFrames = 64

func introFrame(index int) image.Image {
	if introSheet == nil {
		data, _ := artwork.ReadFile("assets/work-intro.png")
		sheet, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		source := sheet.(interface {
			SubImage(image.Rectangle) image.Image
		})
		introSheet = make([]image.Image, introFrames)
		for i := range introSheet {
			x, y := (i%8)*144, (i/8)*144
			introSheet[i] = source.SubImage(image.Rect(x, y, x+144, y+144))
		}
	}
	return introSheet[index]
}

var brandRingImage image.Image

func brandRing() image.Image {
	if brandRingImage == nil {
		data, _ := artwork.ReadFile("assets/1loop-anneau.png")
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		brandRingImage = img
	}
	return brandRingImage
}

// scaleTo resamples once with a high quality filter; Fyne then uploads the
// pixels as they are instead of resampling the full-size art every frame.
func scaleTo(src image.Image, w, h int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// devicePixels converts a length in Fyne units to physical pixels for c.
func devicePixels(c fyne.Canvas, length float32) int {
	if c == nil {
		return 0
	}
	x0, _ := c.PixelCoordinateForPosition(fyne.Position{})
	x1, _ := c.PixelCoordinateForPosition(fyne.NewPos(length, 0))
	return x1 - x0
}

// loopMark is the 1loop ring. On the welcome screen it plays the intro sheet
// and rests on the brand artwork; under a working chat it is the work ring
// of DA/1loop-anneau-travail.html, with the same layers and transitions.
type loopMark struct {
	image   *canvas.Image
	size    float32
	state   string // "brand", "intro" or a work state
	anim    *fyne.Animation
	px      int
	brand   image.Image
	introAt []image.Image // the intro frames, resampled for the screen

	work workRing
}

func newLoopMark(size float32) *loopMark {
	return &loopMark{image: brandImage("1loop-anneau.png", size, size), state: "brand", size: size}
}

// pixels is the mark's size on screen; before it is attached to a window,
// twice its size, which is right on a HiDPI screen.
func (m *loopMark) pixels() int {
	px := devicePixels(fyne.CurrentApp().Driver().CanvasForObject(m.image), m.size)
	if px <= 0 {
		px = int(2 * m.size)
	}
	if px != m.px {
		m.px, m.brand, m.introAt = px, nil, nil
		m.work.art = newRingArt(px)
	}
	return px
}

// resize changes the mark's size on screen; its art is drawn again for it.
func (m *loopMark) resize(size float32) {
	m.size = size
	m.image.SetMinSize(fyne.NewSquareSize(size))
	m.pixels()
	switch m.state {
	case "brand":
		m.showBrand()
	case "intro":
	default:
		m.work.drawn = time.Time{} // draw now, even if the ring is still
		m.tick()
	}
}

func (m *loopMark) show(img image.Image) {
	m.image.Resource = nil
	m.image.Image = img
	m.image.Refresh()
}

func (m *loopMark) stop() {
	if m.anim != nil {
		m.anim.Stop()
		m.anim = nil
	}
}

func (m *loopMark) showBrand() {
	px := m.pixels()
	if m.brand == nil {
		m.brand = scaleTo(brandRing(), px, px)
	}
	m.show(m.brand)
}

// intro draws the ring, the "1" and the dot once (DA intro, 1.24 s), then
// settles on the original artwork at full resolution.
func (m *loopMark) intro() {
	m.stop()
	m.state = "intro"
	m.anim = fyne.NewAnimation(1240*time.Millisecond, func(progress float32) {
		if m.state != "intro" {
			return
		}
		if progress >= 1 {
			m.state = "brand"
			m.introAt = nil
			introSheet = nil
			m.showBrand()
			return
		}
		px := m.pixels()
		if m.introAt == nil {
			m.introAt = make([]image.Image, introFrames)
		}
		index := int(progress * float32(introFrames-1))
		if m.introAt[index] == nil {
			m.introAt[index] = scaleTo(introFrame(index), px, px)
		}
		m.show(m.introAt[index])
	})
	m.anim.Curve = fyne.AnimationLinear
	m.anim.Start()
}

// setState moves the ring to a work state ("brand" puts the artwork back).
// Like the web page, the ring keeps turning across states: only the centre
// icon changes, and done and stopped close the ring.
func (m *loopMark) setState(state string) {
	if state == m.state {
		return
	}
	if state == "brand" {
		m.stop()
		m.state = "brand"
		m.work = workRing{art: m.work.art}
		m.showBrand()
		return
	}
	if !workGlyphs[state] {
		state = "think"
	}
	m.pixels()
	previous := m.state
	m.work.change(state, previous == "brand" || previous == "intro", time.Now())
	m.state = state
	if m.anim == nil || previous == "intro" { // the intro's own animation must end
		m.stop()
		m.anim = fyne.NewAnimation(time.Second, func(float32) { m.tick() })
		m.anim.RepeatCount = fyne.AnimationRepeatForever
		m.anim.Start()
	}
	m.tick()
}

func (m *loopMark) tick() {
	now := time.Now()
	img, still := m.work.frame(now)
	if img != nil {
		m.show(img)
	}
	if still { // closed and settled: nothing moves until the next state
		m.stop()
	}
}

// ---------------------------------------------------------------------------
// Work ring. Every layer of the web page is drawn once at the exact screen
// size (strokes included), then the frame is composed with each layer's
// opacity: about 0.1 ms a frame instead of several for a full drawing.

// workGlyphs are the states with a centre icon, drawn from the page's paths.
var workGlyphs = map[string]bool{
	"think": true, "write": true, "wait": true, "call": true, "read": true, "list": true,
	"edit": true, "bash": true, "approval": true, "compact": true, "done": true, "stopped": true,
}

const glyphStyle = `stroke="#F4F4F4" stroke-linecap="round" stroke-linejoin="round" fill="none"`

// glyphMarkup is each icon of DA/1loop-anneau-travail.html, path for path.
func glyphMarkup(state string) string {
	switch state {
	case "think": // drawn dot by dot, see thinkDot
		return ""
	case "write":
		return `<path stroke-width="8" d="M49 40 C55.5 40 60 42.5 60 48 C60 42.5 64.5 40 71 40 M60 48 V72 M49 80 C55.5 80 60 77.5 60 72 C60 77.5 64.5 80 71 80"/>`
	case "wait":
		return `<path stroke-width="8.5" d="M60 60 V33 M60 60 L77 71"/><circle cx="60" cy="60" r="6" fill="#F4F4F4" stroke="none"/>`
	case "call":
		return `<path stroke-width="7" d="M52 41 C46 41 47 49 47 53 C47 57 45 60 42 60 C45 60 47 63 47 67 C47 71 46 79 52 79 M68 41 C74 41 73 49 73 53 C73 57 75 60 78 60 C75 60 73 63 73 67 C73 71 74 79 68 79"/>`
	case "read":
		return `<path stroke-width="8" d="M44 49 H76 M44 60 H76 M44 71 H64"/>`
	case "list":
		return `<path stroke-width="8" d="M40.4 50.2 C40.4 47.82 42.22 46 44.6 46 H52.3 L57.2 50.9 H75.4 C77.78 50.9 79.6 52.72 79.6 55.1 V71.2 C79.6 73.58 77.78 75.4 75.4 75.4 H44.6 C42.22 75.4 40.4 73.58 40.4 71.2 Z"/>`
	case "edit":
		return `<path stroke-width="7" d="M46 75 L48.5 64.5 L67 46 L75 54 L56.5 72.5 Z M63 50 L71 58"/>`
	case "bash":
		return `<path stroke-width="8" d="M44 48 L55 59 L44 70 M60 72 H76"/>`
	case "approval":
		return `<path stroke-width="8" d="M60 39.7 L75.95 46.23 V58.55 C75.95 68.41 69.57 75.37 60 79.58 C50.43 75.37 44.05 68.41 44.05 58.55 V46.23 Z"/>`
	case "compact":
		return `<path stroke-width="8" d="M60 37 V52 M52.5 45 L60 52 L67.5 45 M60 83 V68 M52.5 75 L60 68 L67.5 75 M42 60 H78"/>`
	case "done":
		return `<path stroke="#FFB000" stroke-width="9" d="M45 61 L55 71 L76 50"/>`
	case "stopped":
		return `<path stroke-width="9" d="M47 47 L73 73 M73 47 L47 73"/>`
	}
	return ""
}

var strokeWidth = regexp.MustCompile(`stroke-width="([0-9.]+)"`)

// drawLayer rasterises markup from the 120 unit grid at px pixels, scaled by
// scale around the centre like the page's CSS transform. oksvg does not
// scale stroke widths with the target or with transforms, so they are
// scaled here.
func drawLayer(markup string, px int, scale float64) image.Image {
	k := float64(px) / 120 * scale
	markup = strokeWidth.ReplaceAllStringFunc(markup, func(attr string) string {
		width, _ := strconv.ParseFloat(strokeWidth.FindStringSubmatch(attr)[1], 64)
		return fmt.Sprintf(`stroke-width="%.3f"`, width*k)
	})
	offset := float64(px)/2 - 60*k
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" fill="none"><g transform="matrix(%.5f 0 0 %.5f %.3f %.3f)">%s</g></svg>`,
		px, px, k, k, offset, offset, markup)
	icon, err := oksvg.ReadIconStream(strings.NewReader(svg))
	if err != nil {
		panic(err) // the markup is fixed: a failure is a programming error
	}
	img := image.NewNRGBA(image.Rect(0, 0, px, px))
	icon.SetTarget(0, 0, float64(px), float64(px))
	icon.Draw(rasterx.NewDasher(px, px, rasterx.NewScannerGV(px, px, img, img.Bounds())), 1)
	return img
}

// ringArt caches the layers drawn for one screen size.
type ringArt struct {
	px     int
	spin   []image.Image // the open ring, every 5 degrees
	dots   []image.Image // its dot, which the page shows and hides without a fade
	closed map[string]image.Image
	icons  map[string]image.Image // by state and scale
}

const spinSteps = 72

func newRingArt(px int) *ringArt {
	return &ringArt{px: px, spin: make([]image.Image, spinSteps), dots: make([]image.Image, spinSteps), closed: map[string]image.Image{}, icons: map[string]image.Image{}}
}

// open and dot draw the page's .spin group at a rotation.
func (a *ringArt) open(angle float64) image.Image {
	return a.turned(a.spin, angle, `<path d="M95.24 30.43 A46 46 0 1 1 67.99 14.70" stroke="#FFB000" stroke-width="12" stroke-linecap="round"/>`)
}

func (a *ringArt) dot(angle float64) image.Image {
	return a.turned(a.dots, angle, `<circle cx="83" cy="20.2" r="6" fill="#F4F4F4"/>`)
}

func (a *ringArt) turned(cache []image.Image, angle float64, markup string) image.Image {
	step := int(math.Round(angle/360*spinSteps)) % spinSteps
	if cache[step] == nil {
		cache[step] = drawLayer(fmt.Sprintf(`<g transform="rotate(%.1f 60 60)">%s</g>`, float64(step)*360/spinSteps, markup), a.px, 1)
	}
	return cache[step]
}

func (a *ringArt) full(color string) image.Image {
	if a.closed[color] == nil {
		a.closed[color] = drawLayer(`<circle cx="60" cy="60" r="46" stroke="`+color+`" stroke-width="12"/>`, a.px, 1)
	}
	return a.closed[color]
}

// icon draws a centre icon at a scale rounded to a hundredth, the steps of
// the pop-in transition.
func (a *ringArt) icon(name string, scale float64) image.Image {
	scale = math.Round(scale*100) / 100
	key := fmt.Sprintf("%s@%.2f", name, scale)
	if a.icons[key] == nil {
		markup := glyphMarkup(name)
		if strings.HasPrefix(name, "dot") { // one of the three thinking dots
			markup = fmt.Sprintf(`<circle cx="%s" cy="60" r="5" fill="#F4F4F4"/>`, strings.TrimPrefix(name, "dot"))
		}
		a.icons[key] = drawLayer(`<g `+glyphStyle+`>`+markup+`</g>`, a.px, scale)
	}
	return a.icons[key]
}

// Timing functions of the page's CSS.
var (
	cssEase      = cubicBezier(0.25, 0.1, 0.25, 1)
	cssEaseInOut = cubicBezier(0.42, 0, 0.58, 1)
	cssPop       = cubicBezier(0.34, 1.56, 0.64, 1)
)

// cubicBezier returns a CSS cubic-bezier() timing function.
func cubicBezier(x1, y1, x2, y2 float64) func(float64) float64 {
	curve := func(a, b, t float64) float64 { return 3*a*(1-t)*(1-t)*t + 3*b*(1-t)*t*t + t*t*t }
	return func(x float64) float64 {
		if x <= 0 {
			return 0
		}
		if x >= 1 {
			return 1
		}
		t := x
		for i := 0; i < 8; i++ { // Newton steps on x(t) = x
			slope := 3*x1*(1-t)*(1-t) + 6*(x2-x1)*(1-t)*t + 3*(1-x2)*t*t
			if math.Abs(slope) < 1e-6 {
				break
			}
			t -= (curve(x1, x2, t) - x) / slope
			t = math.Max(0, math.Min(1, t))
		}
		return curve(y1, y2, t)
	}
}

// fade is one CSS opacity transition.
type fade struct {
	from, to float64
	at       time.Time
	length   time.Duration
}

func (f fade) value(now time.Time) float64 {
	p := float64(now.Sub(f.at)) / float64(f.length)
	return f.from + (f.to-f.from)*cssEase(p)
}

func (f fade) settled(now time.Time) bool { return now.Sub(f.at) >= f.length }

// glyphLayer is a centre icon fading in or out.
type glyphLayer struct {
	state   string
	at      time.Time
	leaving bool
}

type workRing struct {
	art         *ringArt
	state       string
	angle       float64 // the open ring's rotation, kept across states
	last        time.Time
	drawn       time.Time
	open, full  fade
	fullColor   string
	glyphs      []glyphLayer
	thinkOrigin time.Time
}

// change starts the page's transitions towards a new state.
func (w *workRing) change(state string, fresh bool, now time.Time) {
	closed := state == "done" || state == "stopped"
	target := func(on bool) float64 {
		if on {
			return 1
		}
		return 0
	}
	if fresh { // first state after the brand artwork: no fade from nothing
		w.open = fade{from: target(!closed), to: target(!closed), at: now, length: 250 * time.Millisecond}
		w.full = fade{from: target(closed), to: target(closed), at: now, length: 250 * time.Millisecond}
		w.glyphs, w.last = nil, now
	} else {
		w.open = fade{from: w.open.value(now), to: target(!closed), at: now, length: 250 * time.Millisecond}
		w.full = fade{from: w.full.value(now), to: target(closed), at: now, length: 250 * time.Millisecond}
	}
	w.fullColor = "#FFB000"
	if state == "stopped" {
		w.fullColor = "#5C5C5C"
	}
	for i := range w.glyphs {
		if !w.glyphs[i].leaving {
			w.glyphs[i].leaving, w.glyphs[i].at = true, now
		}
	}
	w.glyphs = append(w.glyphs, glyphLayer{state: state, at: now})
	if state == "think" && w.thinkOrigin.IsZero() {
		w.thinkOrigin = now
	}
	w.state = state
}

// frame composes the ring at now. It returns nil when nothing visible
// changed since the last frame, and still once a closed ring has settled.
func (w *workRing) frame(now time.Time) (img image.Image, still bool) {
	closed := w.state == "done" || w.state == "stopped"
	if !closed {
		w.angle = math.Mod(w.angle+float64(now.Sub(w.last))/float64(1100*time.Millisecond)*360, 360)
	}
	w.last = now
	if now.Sub(w.drawn) < 28*time.Millisecond { // ~33 frames a second is smooth
		return nil, false
	}
	w.drawn = now
	px := w.art.px
	out := image.NewNRGBA(image.Rect(0, 0, px, px))
	layer := func(src image.Image, opacity float64) {
		if opacity <= 0.004 {
			return
		}
		mask := image.NewUniform(color.Alpha{A: uint8(math.Min(1, opacity)*255 + 0.5)})
		draw.DrawMask(out, out.Bounds(), src, image.Point{}, mask, image.Point{}, draw.Over)
	}
	layer(w.art.open(w.angle), w.open.value(now))
	if !closed { // .dot has no transition on the page: it goes at once
		layer(w.art.dot(w.angle), 1)
	}
	layer(w.art.full(w.fullColor), w.full.value(now))
	settled := w.open.settled(now) && w.full.settled(now)
	kept := w.glyphs[:0]
	for _, g := range w.glyphs {
		elapsed := float64(now.Sub(g.at))
		opacity := cssEase(elapsed / float64(220*time.Millisecond))
		scale := 0.7 + 0.3*cssPop(elapsed/float64(320*time.Millisecond))
		if g.leaving {
			opacity, scale = 1-opacity, 1.7-scale
		}
		if g.leaving && elapsed >= float64(320*time.Millisecond) {
			continue // gone
		}
		if elapsed < float64(320*time.Millisecond) {
			settled = false
		}
		kept = append(kept, g)
		if g.state == "think" {
			// The page's think keyframes: each dot breathes over 1.2 s,
			// the next ones 0.15 s and 0.3 s later.
			for i, cx := range []string{"46", "60", "74"} {
				t := float64(now.Sub(w.thinkOrigin))/float64(1200*time.Millisecond) - 0.125*float64(i)
				layer(w.art.icon("dot"+cx, scale), opacity*thinkOpacity(t))
			}
			continue
		}
		layer(w.art.icon(g.state, scale), opacity)
	}
	w.glyphs = kept
	return out, closed && settled
}

// thinkOpacity follows @keyframes think: .25 at 0 %, 1 at 40 %, .25 at
// 100 %, eased in and out between keyframes.
func thinkOpacity(t float64) float64 {
	t -= math.Floor(t)
	if t < 0.4 {
		return 0.25 + 0.75*cssEaseInOut(t/0.4)
	}
	return 1 - 0.75*cssEaseInOut((t-0.4)/0.6)
}

// Keep chat and composer comfortably narrow while letting the window resize.
type columnLayout struct {
	width float32
	inset float32
}

func (l columnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.Size{}
	}
	size := objects[0].MinSize()
	size.Width = fyne.Min(size.Width, l.width) + 2*l.inset
	size.Height += 2 * l.inset
	return size
}
func (l columnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := fyne.Min(l.width, fyne.Max(0, size.Width-2*l.inset))
	for _, object := range objects {
		object.Move(fyne.NewPos((size.Width-width)/2, l.inset))
		object.Resize(fyne.NewSize(width, fyne.Max(0, size.Height-2*l.inset)))
	}
}

// launchScreen plays DA/1loop-lancement.html with the original wordmark PNG.
// The PNG is only cut into its four separate glyphs (viewBox columns), never
// redrawn: "1" drops in, "l" rises, the loop is revealed left to right as if
// drawn, "p" slides in; then the layer fades to the app. Click skips.
type launchScreen struct {
	widget.BaseWidget
	canvas fyne.Canvas
	bg     *canvas.Rectangle
	parts  [4]*canvas.Image
	cuts   [4]image.Image
	scaled [4]image.Image // cuts resampled to screen pixels
	px     int
	bounds [4][2]float32 // left edge and width of each glyph, in display units
	t      float32       // elapsed milliseconds
	anim   *fyne.Animation
	done   func()
}

const (
	launchLength = 2200
	launchHeight = 132 // display height of the wordmark
)

func newLaunchScreen(c fyne.Canvas, done func()) *launchScreen {
	l := &launchScreen{canvas: c, done: done, bg: canvas.NewRectangle(colBackground)}
	data, _ := artwork.ReadFile("assets/1loop-une-seule-boucle.png")
	sheet, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	src := sheet.(interface {
		SubImage(image.Rectangle) image.Image
	})
	// Column cuts between glyphs, from the SVG viewBox (-16..277.41 -> 1131 px).
	w, h := sheet.Bounds().Dx(), sheet.Bounds().Dy()
	edges := []int{0, 246, 334, 830, w}
	scale := float32(launchHeight) / float32(h)
	for i := range l.parts {
		l.cuts[i] = src.SubImage(image.Rect(edges[i], 0, edges[i+1], h))
		img := canvas.NewImageFromImage(l.cuts[i])
		img.FillMode = canvas.ImageFillStretch
		img.ScaleMode = canvas.ImageScaleFastest // pre-resampled below; GPU does the 4 % settle
		img.Translucency = 1
		l.parts[i] = img
		l.bounds[i] = [2]float32{float32(edges[i]) * scale, float32(edges[i+1]-edges[i]) * scale}
	}
	l.ExtendBaseWidget(l)
	return l
}

func (l *launchScreen) show() {
	l.Resize(l.canvas.Size())
	l.canvas.Overlays().Add(l)
}

func (l *launchScreen) play() {
	l.anim = fyne.NewAnimation(launchLength*time.Millisecond, func(p float32) {
		l.t = p * launchLength
		l.Refresh()
		if p >= 1 {
			l.finish()
		}
	})
	l.anim.Curve = fyne.AnimationLinear
	l.anim.Start()
}

func (l *launchScreen) Tapped(*fyne.PointEvent) { l.finish() }

func (l *launchScreen) finish() {
	if l.done == nil {
		return
	}
	if l.anim != nil {
		l.anim.Stop()
	}
	l.canvas.Overlays().Remove(l)
	// The sequence never plays again: release its decoded artwork.
	for i := range l.parts {
		l.parts[i].Image = nil
		l.cuts[i], l.scaled[i] = nil, nil
	}
	done := l.done
	l.done = nil
	done()
}

func clamp01(x float32) float32 { return fyne.Min(1, fyne.Max(0, x)) }

// Curves from the DA CSS: cubic-bezier(.34,1.56,.64,1) overshoots like
// easeOutBack; (.65,0,.35,1) is a symmetric ease-in-out.
func easeBack(x float32) float32 {
	x = clamp01(x) - 1
	return 1 + 2.70158*x*x*x + 1.70158*x*x
}

func easeInOut(x float32) float32 {
	x = clamp01(x)
	if x < 0.5 {
		return 4 * x * x * x
	}
	x = -2*x + 2
	return 1 - x*x*x/2
}

func easeOut(x float32) float32 {
	x = 1 - clamp01(x)
	return 1 - x*x*x
}

func (l *launchScreen) CreateRenderer() fyne.WidgetRenderer { return &launchRenderer{l} }

type launchRenderer struct{ l *launchScreen }

func (r *launchRenderer) Layout(size fyne.Size) {
	l := r.l
	t := l.t
	l.bg.Resize(size)
	unit := float32(launchHeight) / 132.8 // display px per viewBox unit
	settle := 0.96 + 0.04*easeOut(t/1500)
	total := l.bounds[3][0] + l.bounds[3][1]
	left := (size.Width - total*settle) / 2
	top := (size.Height-launchHeight*settle)/2 - 20
	offsets := [4]fyne.Position{
		fyne.NewPos(0, -40*unit*(1-easeBack(t/520))),
		fyne.NewPos(0, 28*unit*(1-easeBack((t-120)/460))),
		{},
		fyne.NewPos(28*unit*(1-easeBack((t-760)/480)), 0),
	}
	for i, part := range l.parts {
		width := l.bounds[i][1]
		if i == 2 {
			width *= easeInOut((t - 280) / 700) // loop drawn left to right
		}
		// A zero-sized image cannot be painted, so keep it hidden until it has width.
		if width*settle < 1 {
			part.Hide()
			continue
		}
		part.Show()
		part.Move(fyne.NewPos(left+l.bounds[i][0]*settle+offsets[i].X, top+offsets[i].Y))
		part.Resize(fyne.NewSize(width*settle, launchHeight*settle))
	}
}

func (r *launchRenderer) MinSize() fyne.Size { return fyne.Size{} }

// prepare resamples the glyphs once for the screen's pixel density.
func (l *launchScreen) prepare() {
	px := devicePixels(l.canvas, launchHeight)
	if px <= 0 || px == l.px {
		return
	}
	l.px = px
	for i, cut := range l.cuts {
		b := cut.Bounds()
		w := int(float32(b.Dx())*float32(px)/float32(b.Dy()) + 0.5)
		l.scaled[i] = scaleTo(cut, w, px)
		l.parts[i].Image = l.scaled[i]
		l.parts[i].Refresh()
	}
}

func (r *launchRenderer) Refresh() {
	l := r.l
	if l.done == nil {
		return // finished: its artwork has been released
	}
	t := l.t
	l.prepare()
	out := easeOut((t - 1800) / 400)
	starts := [4][2]float32{{0, 520}, {120, 460}, {280, 120}, {760, 480}}
	for i, part := range l.parts {
		in := clamp01((t - starts[i][0]) / starts[i][1])
		// Read at draw time: no texture rebuild for a fade.
		part.Translucency = float64(fyne.Max(1-in, out))
	}
	// Reveal the loop by cropping the original pixels, never by redrawing.
	loop := l.scaled[2]
	if loop == nil {
		loop = l.cuts[2]
	}
	b := loop.Bounds()
	reveal := easeInOut((t - 280) / 700)
	visible := b.Min.X + int(float32(b.Dx())*reveal+0.5)
	if visible <= b.Min.X {
		visible = b.Min.X + 1
	}
	if current := l.parts[2].Image; current == nil || current.Bounds().Max.X != visible {
		l.parts[2].Image = loop.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(image.Rect(b.Min.X, b.Min.Y, visible, b.Max.Y))
		l.parts[2].Refresh()
	}
	l.bg.FillColor = color.NRGBA{R: colBackground.R, G: colBackground.G, B: colBackground.B, A: uint8(255 * (1 - out))}
	r.Layout(l.Size())
	l.bg.Refresh() // marks the frame dirty; positions and fades are read at draw time
}

func (r *launchRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.l.bg, r.l.parts[0], r.l.parts[1], r.l.parts[2], r.l.parts[3]}
}

func (r *launchRenderer) Destroy() {}
