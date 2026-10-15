//go:build !server

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
//go:embed assets/*.png assets/provider-*.svg
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
func devicePixels(canvas fyne.Canvas, length float32) int {
	if canvas == nil {
		return 0
	}
	x0, _ := canvas.PixelCoordinateForPosition(fyne.Position{})
	x1, _ := canvas.PixelCoordinateForPosition(fyne.NewPos(length, 0))
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
func (mark *loopMark) pixels() int {
	px := devicePixels(fyne.CurrentApp().Driver().CanvasForObject(mark.image), mark.size)
	if px <= 0 {
		px = int(2 * mark.size)
	}
	if px != mark.px {
		mark.px, mark.brand, mark.introAt = px, nil, nil
		mark.work.art = newRingArt(px)
	}
	return px
}

// resize changes the mark's size on screen; its art is drawn again for it.
func (mark *loopMark) resize(size float32) {
	mark.size = size
	mark.image.SetMinSize(fyne.NewSquareSize(size))
	mark.pixels()
	switch mark.state {
	case "brand":
		mark.showBrand()
	case "intro":
	default:
		mark.work.drawn = time.Time{} // draw now, even if the ring is still
		mark.tick()
	}
}

func (mark *loopMark) show(img image.Image) {
	mark.image.Resource = nil
	mark.image.Image = img
	mark.image.Refresh()
}

func (mark *loopMark) stop() {
	if mark.anim != nil {
		mark.anim.Stop()
		mark.anim = nil
	}
}

func (mark *loopMark) showBrand() {
	px := mark.pixels()
	if mark.brand == nil {
		mark.brand = scaleTo(brandRing(), px, px)
	}
	mark.show(mark.brand)
}

// intro draws the ring, the "1" and the dot once (DA intro, 1.24 s), then
// settles on the original artwork at full resolution.
func (mark *loopMark) intro() {
	mark.stop()
	mark.state = "intro"
	mark.anim = fyne.NewAnimation(1240*time.Millisecond, func(progress float32) {
		if mark.state != "intro" {
			return
		}
		if progress >= 1 {
			mark.state = "brand"
			mark.introAt = nil
			introSheet = nil
			mark.showBrand()
			return
		}
		px := mark.pixels()
		if mark.introAt == nil {
			mark.introAt = make([]image.Image, introFrames)
		}
		index := int(progress * float32(introFrames-1))
		if mark.introAt[index] == nil {
			mark.introAt[index] = scaleTo(introFrame(index), px, px)
		}
		mark.show(mark.introAt[index])
	})
	mark.anim.Curve = fyne.AnimationLinear
	mark.anim.Start()
}

// setState moves the ring to a work state ("brand" puts the artwork back).
// Like the web page, the ring keeps turning across states: only the centre
// icon changes, and done and stopped close the ring.
func (mark *loopMark) setState(state string) {
	if state == mark.state {
		return
	}
	if state == "brand" {
		mark.stop()
		mark.state = "brand"
		mark.work = workRing{art: mark.work.art}
		mark.showBrand()
		return
	}
	if !workGlyphs[state] {
		state = "think"
	}
	mark.pixels()
	previous := mark.state
	mark.work.change(state, previous == "brand" || previous == "intro", time.Now())
	mark.state = state
	if mark.anim == nil || previous == "intro" { // the intro's own animation must end
		mark.stop()
		mark.anim = fyne.NewAnimation(time.Second, func(float32) { mark.tick() })
		mark.anim.RepeatCount = fyne.AnimationRepeatForever
		mark.anim.Start()
	}
	mark.tick()
}

func (mark *loopMark) tick() {
	now := time.Now()
	img, still := mark.work.frame(now)
	if img != nil {
		mark.show(img)
	}
	if still { // closed and settled: nothing moves until the next state
		mark.stop()
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
func (art *ringArt) open(angle float64) image.Image {
	return art.turned(art.spin, angle, `<path d="M95.24 30.43 A46 46 0 1 1 67.99 14.70" stroke="#FFB000" stroke-width="12" stroke-linecap="round"/>`)
}

func (art *ringArt) dot(angle float64) image.Image {
	return art.turned(art.dots, angle, `<circle cx="83" cy="20.2" r="6" fill="#F4F4F4"/>`)
}

func (art *ringArt) turned(cache []image.Image, angle float64, markup string) image.Image {
	step := int(math.Round(angle/360*spinSteps)) % spinSteps
	if cache[step] == nil {
		cache[step] = drawLayer(fmt.Sprintf(`<g transform="rotate(%.1f 60 60)">%s</g>`, float64(step)*360/spinSteps, markup), art.px, 1)
	}
	return cache[step]
}

func (art *ringArt) full(color string) image.Image {
	if art.closed[color] == nil {
		art.closed[color] = drawLayer(`<circle cx="60" cy="60" r="46" stroke="`+color+`" stroke-width="12"/>`, art.px, 1)
	}
	return art.closed[color]
}

// icon draws a centre icon at a scale rounded to a hundredth, the steps of
// the pop-in transition.
func (art *ringArt) icon(name string, scale float64) image.Image {
	scale = math.Round(scale*100) / 100
	key := fmt.Sprintf("%s@%.2f", name, scale)
	if art.icons[key] == nil {
		markup := glyphMarkup(name)
		if strings.HasPrefix(name, "dot") { // one of the three thinking dots
			markup = fmt.Sprintf(`<circle cx="%s" cy="60" r="5" fill="#F4F4F4"/>`, strings.TrimPrefix(name, "dot"))
		}
		art.icons[key] = drawLayer(`<g `+glyphStyle+`>`+markup+`</g>`, art.px, scale)
	}
	return art.icons[key]
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

func (transition fade) value(now time.Time) float64 {
	p := float64(now.Sub(transition.at)) / float64(transition.length)
	return transition.from + (transition.to-transition.from)*cssEase(p)
}

func (transition fade) settled(now time.Time) bool {
	return now.Sub(transition.at) >= transition.length
}

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
func (ring *workRing) change(state string, fresh bool, now time.Time) {
	closed := state == "done" || state == "stopped"
	target := func(on bool) float64 {
		if on {
			return 1
		}
		return 0
	}
	if fresh { // first state after the brand artwork: no fade from nothing
		ring.open = fade{from: target(!closed), to: target(!closed), at: now, length: 250 * time.Millisecond}
		ring.full = fade{from: target(closed), to: target(closed), at: now, length: 250 * time.Millisecond}
		ring.glyphs, ring.last = nil, now
	} else {
		ring.open = fade{from: ring.open.value(now), to: target(!closed), at: now, length: 250 * time.Millisecond}
		ring.full = fade{from: ring.full.value(now), to: target(closed), at: now, length: 250 * time.Millisecond}
	}
	ring.fullColor = "#FFB000"
	if state == "stopped" {
		ring.fullColor = "#5C5C5C"
	}
	for i := range ring.glyphs {
		if !ring.glyphs[i].leaving {
			ring.glyphs[i].leaving, ring.glyphs[i].at = true, now
		}
	}
	ring.glyphs = append(ring.glyphs, glyphLayer{state: state, at: now})
	if state == "think" && ring.thinkOrigin.IsZero() {
		ring.thinkOrigin = now
	}
	ring.state = state
}

// frame composes the ring at now. It returns nil when nothing visible
// changed since the last frame, and still once a closed ring has settled.
func (ring *workRing) frame(now time.Time) (img image.Image, still bool) {
	closed := ring.state == "done" || ring.state == "stopped"
	if !closed {
		ring.angle = math.Mod(ring.angle+float64(now.Sub(ring.last))/float64(1100*time.Millisecond)*360, 360)
	}
	ring.last = now
	if now.Sub(ring.drawn) < 28*time.Millisecond { // ~33 frames a second is smooth
		return nil, false
	}
	ring.drawn = now
	px := ring.art.px
	out := image.NewNRGBA(image.Rect(0, 0, px, px))
	layer := func(src image.Image, opacity float64) {
		if opacity <= 0.004 {
			return
		}
		mask := image.NewUniform(color.Alpha{A: uint8(math.Min(1, opacity)*255 + 0.5)})
		draw.DrawMask(out, out.Bounds(), src, image.Point{}, mask, image.Point{}, draw.Over)
	}
	layer(ring.art.open(ring.angle), ring.open.value(now))
	if !closed { // .dot has no transition on the page: it goes at once
		layer(ring.art.dot(ring.angle), 1)
	}
	layer(ring.art.full(ring.fullColor), ring.full.value(now))
	settled := ring.open.settled(now) && ring.full.settled(now)
	kept := ring.glyphs[:0]
	for _, glyphLayer := range ring.glyphs {
		elapsed := float64(now.Sub(glyphLayer.at))
		opacity := cssEase(elapsed / float64(220*time.Millisecond))
		scale := 0.7 + 0.3*cssPop(elapsed/float64(320*time.Millisecond))
		if glyphLayer.leaving {
			opacity, scale = 1-opacity, 1.7-scale
		}
		if glyphLayer.leaving && elapsed >= float64(320*time.Millisecond) {
			continue // gone
		}
		if elapsed < float64(320*time.Millisecond) {
			settled = false
		}
		kept = append(kept, glyphLayer)
		if glyphLayer.state == "think" {
			// The page's think keyframes: each dot breathes over 1.2 s,
			// the next ones 0.15 s and 0.3 s later.
			for i, cx := range []string{"46", "60", "74"} {
				t := float64(now.Sub(ring.thinkOrigin))/float64(1200*time.Millisecond) - 0.125*float64(i)
				layer(ring.art.icon("dot"+cx, scale), opacity*thinkOpacity(t))
			}
			continue
		}
		layer(ring.art.icon(glyphLayer.state, scale), opacity)
	}
	ring.glyphs = kept
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

func (layoutSpec columnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.Size{}
	}
	size := objects[0].MinSize()
	size.Width = fyne.Min(size.Width, layoutSpec.width) + 2*layoutSpec.inset
	size.Height += 2 * layoutSpec.inset
	return size
}
func (layoutSpec columnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := fyne.Min(layoutSpec.width, fyne.Max(0, size.Width-2*layoutSpec.inset))
	for _, object := range objects {
		object.Move(fyne.NewPos((size.Width-width)/2, layoutSpec.inset))
		object.Resize(fyne.NewSize(width, fyne.Max(0, size.Height-2*layoutSpec.inset)))
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

func newLaunchScreen(canvasValue fyne.Canvas, done func()) *launchScreen {
	screen := &launchScreen{canvas: canvasValue, done: done, bg: canvas.NewRectangle(colBackground)}
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
	for i := range screen.parts {
		screen.cuts[i] = src.SubImage(image.Rect(edges[i], 0, edges[i+1], h))
		img := canvas.NewImageFromImage(screen.cuts[i])
		img.FillMode = canvas.ImageFillStretch
		img.ScaleMode = canvas.ImageScaleFastest // pre-resampled below; GPU does the 4 % settle
		img.Translucency = 1
		screen.parts[i] = img
		screen.bounds[i] = [2]float32{float32(edges[i]) * scale, float32(edges[i+1]-edges[i]) * scale}
	}
	screen.ExtendBaseWidget(screen)
	return screen
}

func (screen *launchScreen) show() {
	screen.Resize(screen.canvas.Size())
	screen.canvas.Overlays().Add(screen)
}

func (screen *launchScreen) play() {
	screen.anim = fyne.NewAnimation(launchLength*time.Millisecond, func(p float32) {
		screen.t = p * launchLength
		screen.Refresh()
		if p >= 1 {
			screen.finish()
		}
	})
	screen.anim.Curve = fyne.AnimationLinear
	screen.anim.Start()
}

func (screen *launchScreen) Tapped(*fyne.PointEvent) { screen.finish() }

func (screen *launchScreen) finish() {
	if screen.done == nil {
		return
	}
	if screen.anim != nil {
		screen.anim.Stop()
	}
	screen.canvas.Overlays().Remove(screen)
	// The sequence never plays again: release its decoded artwork.
	for i := range screen.parts {
		screen.parts[i].Image = nil
		screen.cuts[i], screen.scaled[i] = nil, nil
	}
	done := screen.done
	screen.done = nil
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

func (screen *launchScreen) CreateRenderer() fyne.WidgetRenderer { return &launchRenderer{screen} }

type launchRenderer struct{ launchScreen *launchScreen }

func (renderer *launchRenderer) Layout(size fyne.Size) {
	screen := renderer.launchScreen
	t := screen.t
	screen.bg.Resize(size)
	unit := float32(launchHeight) / 132.8 // display px per viewBox unit
	settle := 0.96 + 0.04*easeOut(t/1500)
	total := screen.bounds[3][0] + screen.bounds[3][1]
	left := (size.Width - total*settle) / 2
	top := (size.Height-launchHeight*settle)/2 - 20
	offsets := [4]fyne.Position{
		fyne.NewPos(0, -40*unit*(1-easeBack(t/520))),
		fyne.NewPos(0, 28*unit*(1-easeBack((t-120)/460))),
		{},
		fyne.NewPos(28*unit*(1-easeBack((t-760)/480)), 0),
	}
	for i, part := range screen.parts {
		width := screen.bounds[i][1]
		if i == 2 {
			width *= easeInOut((t - 280) / 700) // loop drawn left to right
		}
		// A zero-sized image cannot be painted, so keep it hidden until it has width.
		if width*settle < 1 {
			part.Hide()
			continue
		}
		part.Show()
		part.Move(fyne.NewPos(left+screen.bounds[i][0]*settle+offsets[i].X, top+offsets[i].Y))
		part.Resize(fyne.NewSize(width*settle, launchHeight*settle))
	}
}

func (renderer *launchRenderer) MinSize() fyne.Size { return fyne.Size{} }

// prepare resamples the glyphs once for the screen's pixel density.
func (screen *launchScreen) prepare() {
	px := devicePixels(screen.canvas, launchHeight)
	if px <= 0 || px == screen.px {
		return
	}
	screen.px = px
	for i, cut := range screen.cuts {
		b := cut.Bounds()
		w := int(float32(b.Dx())*float32(px)/float32(b.Dy()) + 0.5)
		screen.scaled[i] = scaleTo(cut, w, px)
		screen.parts[i].Image = screen.scaled[i]
		screen.parts[i].Refresh()
	}
}

func (renderer *launchRenderer) Refresh() {
	screen := renderer.launchScreen
	if screen.done == nil {
		return // finished: its artwork has been released
	}
	t := screen.t
	screen.prepare()
	out := easeOut((t - 1800) / 400)
	starts := [4][2]float32{{0, 520}, {120, 460}, {280, 120}, {760, 480}}
	for i, part := range screen.parts {
		in := clamp01((t - starts[i][0]) / starts[i][1])
		// Read at draw time: no texture rebuild for a fade.
		part.Translucency = float64(fyne.Max(1-in, out))
	}
	// Reveal the loop by cropping the original pixels, never by redrawing.
	loop := screen.scaled[2]
	if loop == nil {
		loop = screen.cuts[2]
	}
	b := loop.Bounds()
	reveal := easeInOut((t - 280) / 700)
	visible := b.Min.X + int(float32(b.Dx())*reveal+0.5)
	if visible <= b.Min.X {
		visible = b.Min.X + 1
	}
	if current := screen.parts[2].Image; current == nil || current.Bounds().Max.X != visible {
		screen.parts[2].Image = loop.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(image.Rect(b.Min.X, b.Min.Y, visible, b.Max.Y))
		screen.parts[2].Refresh()
	}
	screen.bg.FillColor = color.NRGBA{R: colBackground.R, G: colBackground.G, B: colBackground.B, A: uint8(255 * (1 - out))}
	renderer.Layout(screen.Size())
	screen.bg.Refresh() // marks the frame dirty; positions and fades are read at draw time
}

func (renderer *launchRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.launchScreen.bg, renderer.launchScreen.parts[0], renderer.launchScreen.parts[1], renderer.launchScreen.parts[2], renderer.launchScreen.parts[3]}
}

func (renderer *launchRenderer) Destroy() {}
