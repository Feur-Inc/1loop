// SPDX-License-Identifier: MPL-2.0

package ui

// Loopy's face: the ring of the 1loop logo, its dot as one eye and a second
// eye inside, as the art direction draws it (issue #352, "Loopy Mascot
// Directions"). Loopy shows what it does by how it moves, not by a status
// dot: each state is a set of poses over time, and springs carry the face
// from pose to pose, so a state never cuts into the next. The two eyes move
// together, stay in the ring's hollow and change shape with the mood.
// Frames are drawn at the exact screen size with oksvg, about 30 a second
// while the face is on screen.

import (
	"fmt"
	"image"
	"math"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

// The drawing, in the art direction's 120 unit grid.
const (
	faceR  = 40.0 // the ring's radius
	faceSW = 15.0 // its stroke
	faceER = 10.0 // an eye's radius
	faceC  = 60.0
)

var (
	faceAmber = [3]float64{255, 176, 0}
	faceRed   = [3]float64{232, 106, 95}
	faceGreen = [3]float64{127, 200, 143}
	faceE1    = [2]float64{50, 47} // the inner eye
	faceE2    = facePoint(-60)     // the dot on the ring
	faceMid   = [2]float64{(faceE1[0] + faceE2[0]) / 2, (faceE1[1] + faceE2[1]) / 2}
	// The eye shapes lean a little with the pair.
	faceLean = math.Atan2(faceE2[1]-faceE1[1], faceE2[0]-faceE1[0]) * 180 / math.Pi * .45
)

func facePoint(deg float64) [2]float64 {
	a := deg * math.Pi / 180
	return [2]float64{faceC + faceR*math.Cos(a), faceC + faceR*math.Sin(a)}
}

// facePose is everything that moves.
type facePose struct {
	tx, ty, sx, sy, rot, turn, blink, ringScale, red, green float64
	lx, ly, ew, eh, lid, slant, happy, spread, eyeRot       float64
}

func restPose() facePose {
	return facePose{sx: 1, sy: 1, blink: 1, ringScale: 1, ew: 1, eh: 1, spread: 1}
}

// fields lists the pose's values for the springs, with whether each one is
// bouncy (the body) or quick (the eyes).
func (p *facePose) fields() []*float64 {
	return []*float64{&p.tx, &p.ty, &p.sx, &p.sy, &p.rot, &p.turn, &p.blink, &p.ringScale, &p.red, &p.green,
		&p.lx, &p.ly, &p.ew, &p.eh, &p.lid, &p.slant, &p.happy, &p.spread, &p.eyeRot}
}

var faceBouncy = map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true, 7: true} // tx ty sx sy rot ringScale

// faceState is one way Loopy behaves, looping over period seconds.
type faceState struct {
	name   string
	period float64
	smooth bool // slow springs: the eyes glide (reading, copying)
	pose   func(t float64) facePose
}

func faceEase(x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	return x * x * (3 - 2*x)
}

func faceBump(t, a, b float64) float64 {
	if t > a && t < b {
		return math.Sin(math.Pi * (t - a) / (b - a))
	}
	return 0
}

// faceBlink: quick, both eyes at once, sometimes twice.
func faceBlink(t, period float64, double bool) float64 {
	one := func(u float64) float64 {
		if u > 0 && u < .14 {
			return 1 - .95*math.Sin(math.Pi*u/.14)
		}
		return 1
	}
	x := math.Mod(t, period)
	if double {
		return math.Min(one(x), one(x-.24))
	}
	return one(x)
}

// keyframes holds each pose from its time on; the springs make the move.
type keyframe struct {
	at  float64
	set func(*facePose)
}

func hold(t float64, base facePose, frames ...keyframe) facePose {
	for _, frame := range frames {
		if t >= frame.at {
			frame.set(&base)
		}
	}
	return base
}

// faceHash is a fixed pseudo-random number in [0, 1).
func faceHash(n float64) float64 {
	x := math.Sin(n*127.1+311.7) * 43758.5453
	return x - math.Floor(x)
}

// faceStates are the art direction's states, with its timings.
var faceStates = map[string]faceState{
	"idle": {name: "idle", period: 8, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{0, func(p *facePose) { p.lx, p.ly, p.rot = 0, 0, 0 }},
			keyframe{1.2, func(p *facePose) { p.lx, p.ly, p.rot = 5, -1, 6 }},
			keyframe{2.6, func(p *facePose) { p.lx, p.ly, p.rot = -5, 1, -5 }},
			keyframe{4.0, func(p *facePose) { p.lx, p.ly, p.rot = -2, -5, -2 }},
			keyframe{5.4, func(p *facePose) { p.lx, p.ly, p.rot = 3, 3, 3 }},
			keyframe{6.8, func(p *facePose) { p.lx, p.ly, p.rot = 0, 0, 0 }})
		p.sy, p.sx = 1+.02*math.Sin(t*2.2), 1-.012*math.Sin(t*2.2)
		p.blink = math.Min(faceBlink(t+1, 8, true), faceBlink(t+5, 8, false))
		return p
	}},
	"hello": {name: "hello", period: 3.2, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{0, func(p *facePose) { p.sy, p.sx, p.ty, p.ew, p.eh = .85, 1.1, 3, 1, 1 }},
			keyframe{.18, func(p *facePose) { p.ty, p.sy, p.sx, p.ew, p.eh = -12, 1.12, .92, 1.25, 1.25 }},
			keyframe{.45, func(p *facePose) { p.ty, p.sy, p.sx = 2, .88, 1.08 }},
			keyframe{.62, func(p *facePose) { p.ty, p.sy, p.sx, p.rot = -8, 1.08, .95, 8 }},
			keyframe{.9, func(p *facePose) { p.ty, p.sy, p.sx, p.rot, p.ew, p.eh = 0, 1, 1, -6, 1.12, 1.12 }},
			keyframe{1.4, func(p *facePose) { p.rot = 4 }},
			keyframe{1.9, func(p *facePose) { p.rot = 0 }},
			keyframe{2.6, func(p *facePose) { p.ew, p.eh = 1, 1 }})
		p.turn = 360 * faceEase((t-.15)/.75)
		p.blink = math.Min(1-.5*faceBump(t, 1.2, 1.36), 1-.5*faceBump(t, 1.45, 1.61))
		return p
	}},
	"ding": {name: "ding", period: 3, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{.3, func(p *facePose) { p.sy, p.sx, p.ty, p.eh = .82, 1.12, 4, .7 }},
			keyframe{.48, func(p *facePose) {
				p.ty, p.sy, p.sx, p.ew, p.eh, p.lx, p.ly, p.ringScale = -14, 1.15, .9, 1.25, 1.35, 3, -6, 1.08
			}},
			keyframe{.95, func(p *facePose) { p.ty, p.sy, p.sx, p.ringScale = 0, .9, 1.08, 1 }},
			keyframe{1.15, func(p *facePose) { p.sy, p.sx = 1, 1 }},
			keyframe{1.9, func(p *facePose) { p.ew, p.eh, p.lx, p.ly = 1, 1, 0, 0 }})
		p.blink = faceBlink(t, 3, false)
		return p
	}},
	"ack": {name: "ack", period: 2, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{.1, func(p *facePose) { p.ty, p.ly, p.sy, p.ew, p.eh = 5, 4, .94, 1.15, 1.15 }},
			keyframe{.32, func(p *facePose) { p.ty, p.ly, p.sy = -2, -1, 1.03 }},
			keyframe{.52, func(p *facePose) { p.ty, p.ly, p.sy = 5, 4, .94 }},
			keyframe{.74, func(p *facePose) { p.ty, p.ly, p.sy = 0, 0, 1 }},
			keyframe{1.2, func(p *facePose) { p.ew, p.eh = 1, 1 }})
		p.blink = 1 - .9*faceBump(t, 1.3, 1.44)
		return p
	}},
	"thinking": {name: "thinking", period: 5, pose: func(t float64) facePose {
		a, found := math.Pi*(1.1+.8*math.Sin(t*1.2)), faceBump(t, 3.9, 4.5)
		p := restPose()
		p.lx, p.ly = 5*math.Cos(a)*(1-found), -3.5+3.5*math.Sin(a)*(1-found)
		p.ty, p.ew, p.eh = -4*found, 1+.2*found, 1+.2*found
		p.turn = -22 * math.Sin(t*math.Pi*2/5) * (1 - found)
		p.blink = faceBlink(t+2.2, 5, false)
		return p
	}},
	"reading": {name: "reading", period: 12.6, smooth: true, pose: func(t float64) facePose {
		const L = 1.9
		line := math.Min(5, math.Floor(t/L))
		x, top := math.Mod(t, L)/L, t > 6*L
		read, back := faceEase(x/.74), faceEase((x-.74)/.26)
		lx := -5 + 10*read
		if x >= .74 {
			lx = 5 - 10*back
		}
		ly := -3 + line*1.4
		if line < 5 {
			ly += 1.4 * faceEase((x-.6)/.4)
		}
		p := restPose()
		if top {
			u := faceEase((t - 6*L) / 1.2)
			lx, ly = -5*u+5*(1-u), -3*u+4*(1-u)
			p.blink = 1 - .9*faceBump(t-6*L, .1, .25)
		}
		p.lx, p.ly, p.rot, p.tx, p.ty, p.ew, p.eh = lx, ly, lx*1.3, lx*.5, ly*.8+1, .95, .92
		return p
	}},
	"copy": {name: "copy", period: 3.6, smooth: true, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{0, func(p *facePose) { p.lx, p.ly, p.rot, p.tx = -5, 3, -8, -2 }},
			keyframe{1.5, func(p *facePose) { p.lx, p.ly, p.rot, p.tx = 5, -3, 8, 2 }},
			keyframe{3, func(p *facePose) { p.lx, p.ly, p.rot, p.tx = -5, 3, -8, -2 }})
		p.blink = 1 - .9*faceBump(t, 1.35, 1.5)
		return p
	}},
	"typing": {name: "typing", period: 600, pose: func(t float64) facePose {
		p := restPose()
		chunk := math.Floor(t / 1.25)
		if faceHash(chunk*3.1) < .3 { // a pause: it looks up at the screen to read again
			p.lx, p.ly, p.rot = (faceHash(chunk)-.5)*3, -3, -3
			p.blink = 1 - .9*faceBump(math.Mod(t, 1.25), .5, .65)
			return p
		}
		speed := .1 + .1*faceHash(chunk*7.7)
		slot, within := math.Floor(t/speed), math.Mod(t, speed)/speed
		kx, ky, tap := (faceHash(slot)-.5)*9, 2.5+2*faceHash(slot*1.7), 0.0
		if within < .35 {
			tap = 1 - within/.35
		}
		p.lx, p.ly, p.ty, p.rot, p.eh = kx, ky+1.2*tap, 1.4*tap, kx*.5, 1-.15*tap
		return p
	}},
	"waiting": {name: "waiting", period: 3.4, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{0, func(p *facePose) { p.rot, p.ty = -10, 0 }},
			keyframe{.6, func(p *facePose) { p.ty, p.sy, p.sx = -6, 1.06, .96 }},
			keyframe{.85, func(p *facePose) { p.ty, p.sy, p.sx = 0, .92, 1.06 }},
			keyframe{1.05, func(p *facePose) { p.sy, p.sx = 1, 1 }},
			keyframe{1.7, func(p *facePose) { p.rot = 10 }},
			keyframe{2.3, func(p *facePose) { p.ty, p.sy, p.sx = -6, 1.06, .96 }},
			keyframe{2.55, func(p *facePose) { p.ty, p.sy, p.sx = 0, .92, 1.06 }},
			keyframe{2.75, func(p *facePose) { p.sy, p.sx = 1, 1 }})
		p.lx, p.ly, p.ew, p.eh = -2, 3, 1.12, 1.18
		p.blink = faceBlink(t+1.2, 3.4, true)
		return p
	}},
	"blocked": {name: "blocked", period: 2.6, pose: func(t float64) facePose {
		base := restPose()
		base.eh = 1.1
		p := hold(t, base,
			keyframe{0, func(p *facePose) { p.lx, p.ly = -4, -1 }},
			keyframe{.35, func(p *facePose) { p.lx, p.ly = 4, -2 }},
			keyframe{.7, func(p *facePose) { p.lx, p.ly = -3, 1 }},
			keyframe{1.05, func(p *facePose) { p.lx, p.ly = 3, -1 }},
			keyframe{1.6, func(p *facePose) { p.lx, p.ly, p.eh = 0, 2, 1.25 }},
			keyframe{2.2, func(p *facePose) { p.eh = 1.1 }})
		shake := faceBump(t, 1.5, 2.1)
		p.red, p.slant, p.ew = 1, .45, 1.08
		p.tx, p.sy = 2.4*math.Sin(t*38)*shake, 1-.03*shake
		return p
	}},
	"done": {name: "done", period: 3.4, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{.25, func(p *facePose) { p.sy, p.sx, p.ty, p.eh = .78, 1.16, 5, .7 }},
			keyframe{.45, func(p *facePose) { p.ty, p.sy, p.sx, p.green, p.ew, p.eh = -16, 1.18, .88, 1, 1.25, 1.25 }},
			keyframe{.9, func(p *facePose) { p.ty, p.sy, p.sx = 2, .84, 1.12 }},
			keyframe{1.1, func(p *facePose) { p.ty, p.sy, p.sx, p.rot, p.ew, p.eh = 0, 1, 1, 9, 1.12, 1.12 }},
			keyframe{1.6, func(p *facePose) { p.rot = -8 }},
			keyframe{2.1, func(p *facePose) { p.rot = 5 }},
			keyframe{2.6, func(p *facePose) { p.rot = 0 }},
			keyframe{3, func(p *facePose) { p.green, p.ew, p.eh = 0, 1, 1 }})
		p.turn = 360 * faceEase((t-.4)/.65)
		p.blink = math.Min(1-.5*faceBump(t, 1.3, 1.46), 1-.5*faceBump(t, 1.55, 1.71))
		return p
	}},
	"listening": {name: "listening", period: 6, pose: func(t float64) facePose {
		v := math.Abs(math.Sin(t*7.3))*.6 + math.Abs(math.Sin(t*11.7))*.4
		nod := faceBump(t, 1.6, 1.9) + faceBump(t, 1.95, 2.25) + faceBump(t, 4.4, 4.7) + faceBump(t, 4.75, 5.05)
		p := hold(t, restPose(),
			keyframe{0, func(p *facePose) { p.rot, p.tx = -11, -2 }},
			keyframe{3, func(p *facePose) { p.rot, p.tx = 9, 2 }})
		p.ty, p.ly, p.ew, p.eh = 4*nod, 1-1.5*v+3*nod, 1.12, 1.2+.1*v-.25*nod
		p.ringScale, p.lx, p.blink = 1+.05*v*(1-nod), -1, faceBlink(t+3, 6, false)
		return p
	}},
	"sleep": {name: "sleep", period: 7, pose: func(t float64) facePose {
		p := hold(t, restPose(),
			keyframe{0, func(p *facePose) { p.lid, p.ly, p.rot = .72, 3, -14 }},
			keyframe{4.2, func(p *facePose) { p.lid, p.ly, p.rot = .4, 0, -8 }},
			keyframe{5.2, func(p *facePose) { p.lid, p.ly, p.rot = .72, 3, -14 }})
		p.happy, p.sy, p.sx, p.ty = .25, 1+.022*math.Sin(t*1.15), 1-.011*math.Sin(t*1.15), .7*math.Sin(t*1.15)
		return p
	}},
}

// loopyFace is Loopy drawn at one size, following one state.
type loopyFace struct {
	image  *canvas.Image
	size   float32
	plate  bool // the dark rounded square behind it, as on its own
	mu     sync.Mutex
	state  string
	since  time.Time
	pose   facePose
	speed  facePose // the springs' velocities
	last   time.Time
	anim   *fyne.Animation
	px     int
	shown  bool
	frozen bool // drawn once and kept: an old message's avatar
}

// newLoopyFace makes a face size points wide. A frozen face is drawn once
// in its state's first pose and never moves (the avatars of past messages).
func newLoopyFace(size float32, state string, plate, frozen bool) *loopyFace {
	face := &loopyFace{size: size, plate: plate, frozen: frozen, pose: restPose()}
	face.image = canvas.NewImageFromImage(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	face.image.FillMode = canvas.ImageFillContain
	face.image.ScaleMode = canvas.ImageScaleSmooth
	face.image.SetMinSize(fyne.NewSquareSize(size))
	if frozen {
		// The avatars of past messages are all the same picture: drawn once
		// for each size and state.
		face.state, face.pose = state, faceStates[state].pose(1.5)
		face.pose.blink = 1
		face.image.Image = frozenFace(face.pose, int(2*size), state, plate)
		return face
	}
	face.set(state)
	return face
}

var (
	frozenMu    sync.Mutex
	frozenFaces = map[string]image.Image{}
)

func frozenFace(pose facePose, px int, state string, plate bool) image.Image {
	key := fmt.Sprintf("%s/%d/%v", state, px, plate)
	frozenMu.Lock()
	defer frozenMu.Unlock()
	if frozenFaces[key] == nil {
		frozenFaces[key] = drawLoopy(pose, px, plate)
	}
	return frozenFaces[key]
}

// set moves the face to a state; the springs carry it there.
func (face *loopyFace) set(state string) {
	if _, ok := faceStates[state]; !ok {
		state = "idle"
	}
	face.mu.Lock()
	if face.state == state {
		face.mu.Unlock()
		return
	}
	face.state, face.since = state, time.Now()
	face.mu.Unlock()
	if face.frozen || !motionOn() {
		face.pose = faceStates[state].pose(1.5)
		face.pose.blink = 1
		face.render()
		return
	}
	face.start()
}

// start runs the animation while the face can be seen.
func (face *loopyFace) start() {
	if face.anim != nil {
		return
	}
	face.last = time.Now()
	face.anim = &fyne.Animation{Duration: time.Hour, RepeatCount: fyne.AnimationRepeatForever, Tick: func(float32) { face.tick() }}
	face.anim.Start()
	face.render()
}

// stop ends the animation (the face left the screen).
func (face *loopyFace) stop() {
	if face.anim != nil {
		face.anim.Stop()
		face.anim = nil
	}
}

// tick moves the springs and draws about 30 frames a second.
func (face *loopyFace) tick() {
	now := time.Now()
	dt := math.Min(.05, now.Sub(face.last).Seconds())
	if dt < 1.0/32 {
		return
	}
	face.last = now
	if !face.image.Visible() || fyne.CurrentApp().Driver().CanvasForObject(face.image) == nil {
		return // not on screen: no drawing
	}
	face.mu.Lock()
	state := faceStates[face.state]
	t := math.Mod(now.Sub(face.since).Seconds(), state.period)
	face.mu.Unlock()
	target := state.pose(t)
	for i := 0; i < 4; i++ {
		face.step(target, state, dt/4)
	}
	face.render()
}

// step moves every value toward its target on a spring.
func (face *loopyFace) step(target facePose, state faceState, dt float64) {
	values, speeds, targets := face.pose.fields(), face.speed.fields(), target.fields()
	for i := range values {
		stiff, damp := 420.0, 30.0
		switch {
		case state.smooth:
			stiff, damp = 70, 17
		case faceBouncy[i]:
			stiff, damp = 210, 13
		}
		diff := *targets[i] - *values[i]
		accel := diff*stiff - *speeds[i]*damp
		switch i {
		case 5: // turn: the short way round, so a full turn never unwinds
			diff = math.Mod(math.Mod(diff, 360)+540, 360) - 180
			accel = diff*160 - *speeds[i]*25
		case 6: // blink
			accel = diff*900 - *speeds[i]*60
		}
		*speeds[i] += accel * dt
		*values[i] += *speeds[i] * dt
	}
}

// render draws the current pose at the screen's pixel size.
func (face *loopyFace) render() {
	px := devicePixels(fyne.CurrentApp().Driver().CanvasForObject(face.image), face.size)
	if px <= 0 {
		px = int(2 * face.size)
	}
	face.image.Image = drawLoopy(face.pose, px, face.plate)
	face.image.Refresh()
}

// drawLoopy draws one pose: every point is placed here, in pixels, so oksvg
// only fills and strokes plain paths.
func drawLoopy(p facePose, px int, plate bool) image.Image {
	k := float64(px) / 120
	// The art direction draws inside translate(6 6) scale(.9).
	out := func(x, y float64) (float64, float64) { return (6 + .9*x) * k, (6 + .9*y) * k }
	turn := (p.rot + p.turn) * math.Pi / 180
	body := func(x, y float64) (float64, float64) {
		// rotate around the centre, squash from the bottom, then move
		dx, dy := x-faceC, y-faceC
		x, y = faceC+dx*math.Cos(turn)-dy*math.Sin(turn), faceC+dx*math.Sin(turn)+dy*math.Cos(turn)
		x, y = 60+(x-60)*p.sx, 100+(y-100)*p.sy
		return out(x+p.tx, y+p.ty)
	}
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d">`, px, px)
	if plate {
		fmt.Fprintf(&svg, `<rect width="%d" height="%d" rx="%.2f" fill="#121212"/>`, px, px, 27*k)
	}
	// The ring: from -12° the long way round to -108°.
	var ring strings.Builder
	for i := 0; i <= 64; i++ {
		deg := -12 + 264*float64(i)/64
		a := deg * math.Pi / 180
		x, y := faceC+faceR*p.ringScale*math.Cos(a), faceC+faceR*p.ringScale*math.Sin(a)
		x, y = body(x, y)
		if i == 0 {
			fmt.Fprintf(&ring, "M%.2f %.2f", x, y)
		} else {
			fmt.Fprintf(&ring, "L%.2f %.2f", x, y)
		}
	}
	color := faceMix(faceAmber, faceRed, unit(p.red))
	color = faceMix(color, faceGreen, unit(p.green))
	width := faceSW * .9 * k * p.ringScale * (p.sx + p.sy) / 2
	fmt.Fprintf(&svg, `<path d="%s" fill="none" stroke="rgb(%d,%d,%d)" stroke-width="%.2f" stroke-linecap="round" stroke-linejoin="round"/>`,
		ring.String(), int(color[0]), int(color[1]), int(color[2]), width)
	// The eyes, kept in the ring's hollow.
	shape := eyeShape{ew: math.Max(.2, p.ew), eh: math.Max(.05, p.eh), lid: math.Min(.97, math.Max(0, p.lid)),
		slant: p.slant, happy: unit(p.happy), blink: p.blink, rot: p.eyeRot}
	at := func(e [2]float64) [2]float64 {
		return [2]float64{faceMid[0] + (e[0]-faceMid[0])*p.spread + p.lx, faceMid[1] + (e[1]-faceMid[1])*p.spread + p.ly}
	}
	a1, a2 := at(faceE1), at(faceE2)
	reach := faceER*math.Max(shape.ew, shape.eh) + 2
	if d, room := math.Hypot(a1[0]-faceC, a1[1]-faceC), faceR-faceSW/2-reach; d > room {
		k := (d - room) / d
		dx, dy := (faceC-a1[0])*k, (faceC-a1[1])*k
		a1 = [2]float64{a1[0] + dx, a1[1] + dy}
		a2 = [2]float64{a2[0] + dx*.5, a2[1] + dy*.5}
	}
	for i, eye := range [][2]float64{a1, a2} {
		fmt.Fprintf(&svg, `<path d="%s" fill="#F4F4F4"/>`, eyePath(eye, shape, i == 1, body))
	}
	svg.WriteString(`</svg>`)
	icon, err := oksvg.ReadIconStream(strings.NewReader(svg.String()))
	img := image.NewNRGBA(image.Rect(0, 0, px, px))
	if err != nil {
		return img
	}
	icon.SetTarget(0, 0, float64(px), float64(px))
	icon.Draw(rasterx.NewDasher(px, px, rasterx.NewScannerGV(px, px, img, img.Bounds())), 1)
	return img
}

type eyeShape struct{ ew, eh, lid, slant, happy, blink, rot float64 }

// eyePath is one eye: an ellipse cut by an upper lid (closing, tilted for
// worry or focus) and pushed up from below (a smile), as the art direction
// draws it.
func eyePath(c [2]float64, s eyeShape, mirror bool, place func(x, y float64) (float64, float64)) string {
	rx, ry := faceER*s.ew, faceER*s.eh*s.blink
	slant := s.slant
	if mirror {
		slant = -slant
	}
	ang := (faceLean + s.rot) * math.Pi / 180
	co, si := math.Cos(ang), math.Sin(ang)
	var path strings.Builder
	const n = 36
	for i := 0; i < n; i++ {
		a := float64(i) / n * math.Pi * 2
		x, y := rx*math.Cos(a), ry*math.Sin(a)
		top := -ry + s.lid*2*ry + slant*x*.9
		bottom := ry - s.happy*ry*1.55*(1-(x/rx)*(x/rx))
		if y < top {
			y = top
		}
		if y > bottom {
			y = bottom
		}
		px, py := place(c[0]+x*co-y*si, c[1]+x*si+y*co)
		if i == 0 {
			fmt.Fprintf(&path, "M%.2f %.2f", px, py)
		} else {
			fmt.Fprintf(&path, "L%.2f %.2f", px, py)
		}
	}
	return path.String() + "Z"
}

func faceMix(a, b [3]float64, k float64) [3]float64 {
	return [3]float64{a[0] + (b[0]-a[0])*k, a[1] + (b[1]-a[1])*k, a[2] + (b[2]-a[2])*k}
}

func unit(v float64) float64 { return math.Max(0, math.Min(1, v)) }
