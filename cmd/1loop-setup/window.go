// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"
)

// The window, in units of a 96 dpi screen.
const windowWidth, windowHeight = 720.0, 480.0

type view int

const (
	viewWelcome view = iota
	viewWorking
	viewDone
	viewRunning // 1loop must be quit first
	viewFailed
	viewRemove // the uninstaller's question
	viewRemoved
)

// control is something to press: where, and what it does.
type control struct {
	id         string
	x, y, w, h float64
	press      func()
}

// setupWindow is the state of the window and draws it. The functions are
// what the platform gives (main_windows.go); every method runs on the
// window's thread, except progressed, through post.
type setupWindow struct {
	version  string
	dir      string
	menu     bool // Start menu shortcut
	desktop  bool
	removing bool // the uninstaller
	failure  string

	install  func(dir string, menu, desktop bool, progress func(float64)) error
	remove   func() error
	browse   func(dir string) string
	launch   func()
	quit     func()
	minimize func()
	licence  func()
	post     func(func()) // runs on the window's thread

	view     view
	shownAt  time.Time
	progress float64 // what is done
	drawn    float64 // what the ring shows, following it
	controls []control
	hover    string
	pressed  string
	focus    string
	keyboard bool // the focus shows once the keyboard is used
	glow     map[string]float64
	last     time.Time
	fonts    *fontSet
}

func (w *setupWindow) show(next view) {
	w.view, w.shownAt, w.hover, w.pressed, w.focus = next, time.Now(), "", "", ""
}

// startInstall installs away from the window's thread.
func (w *setupWindow) startInstall() {
	w.progress, w.drawn = 0, 0
	w.show(viewWorking)
	dir, menu, desktop := w.dir, w.menu, w.desktop
	go func() {
		err := w.install(dir, menu, desktop, func(done float64) { w.post(func() { w.progress = done }) })
		w.post(func() { w.ended(err, viewDone) })
	}()
}

func (w *setupWindow) startRemove() {
	go func() {
		err := w.remove()
		w.post(func() { w.ended(err, viewRemoved) })
	}()
}

func (w *setupWindow) ended(err error, next view) {
	switch {
	case err == errRunning:
		w.show(viewRunning)
	case err != nil:
		w.failure = err.Error()
		w.show(viewFailed)
	default:
		w.progress = 1
		w.show(next)
	}
}

// retry is what to do again once 1loop is closed.
func (w *setupWindow) retry() {
	if w.removing {
		w.startRemove()
	} else {
		w.startInstall()
	}
}

// moving says the next frame differs from this one.
func (w *setupWindow) moving() bool {
	if w.view == viewWorking || time.Since(w.shownAt) < 900*time.Millisecond {
		return true
	}
	for id, level := range w.glow {
		if id == w.hover && level < 1 || id != w.hover && level > 0 {
			return true
		}
	}
	return false
}

// draw draws the window at scale, and where its controls are.
func (w *setupWindow) draw(img *image.RGBA, scale float64, now time.Time) {
	step := 1.0
	if !w.last.IsZero() {
		step = min(now.Sub(w.last).Seconds(), 0.1)
	}
	w.last = now
	if w.glow == nil {
		w.glow = map[string]float64{}
	}
	if w.hover != "" {
		w.glow[w.hover] += 0 // it starts to light
	}
	for id, level := range w.glow {
		if id == w.hover {
			w.glow[id] = min(1, level+step*9)
		} else if level = level - step*7; level > 0 {
			w.glow[id] = level
		} else {
			delete(w.glow, id)
		}
	}
	// The ring closes as fast as it can be followed, never at once.
	w.drawn += (w.progress - w.drawn) * min(1, step*8)

	w.controls = w.controls[:0]
	p := &painter{img: img, scale: scale, alpha: 1, fonts: w.fonts}
	p.backdrop(windowWidth, windowHeight, windowWidth/2, 130)
	w.titleBar(p)

	since := now.Sub(w.shownAt).Seconds()
	shown := 1 - math.Pow(1-min(since/0.38, 1), 3)
	p.alpha, p.dy = shown, (1-shown)*14
	switch w.view {
	case viewWelcome:
		w.drawWelcome(p)
	case viewWorking:
		w.drawWorking(p)
	case viewDone:
		w.drawDone(p, since)
	case viewRunning:
		action := "install"
		if w.removing {
			action = "uninstall"
		}
		w.drawQuestion(p, "1loop is running", "Quit it first, here or in the notification area: an agent may be working.", colText,
			w.button("retry", "Try again", w.retry), w.button("cancel", "Cancel "+action, w.quit))
	case viewFailed:
		w.drawQuestion(p, "Something went wrong", w.failure, colDanger,
			w.button("retry", "Try again", w.retry), w.button("cancel", "Close", w.quit))
	case viewRemove:
		w.drawQuestion(p, "Uninstall 1loop?", "Your chats and settings stay on this PC.", colText,
			w.button("remove", "Uninstall", w.startRemove), w.button("cancel", "Cancel", w.quit))
	case viewRemoved:
		w.drawQuestion(p, "1loop is uninstalled", "Your chats and settings are still on this PC, should you come back.", colText,
			w.button("close", "Close", w.quit))
	}
}

// titleBar is the two buttons of a window without a frame.
func (w *setupWindow) titleBar(p *painter) {
	for i, id := range []string{"minimize", "close-window"} {
		x := windowWidth - 92 + float64(i)*44
		press := w.minimize
		if id == "close-window" {
			press = w.quit
			if w.view == viewWorking { // half an installation is no use
				press = nil
			}
		}
		level := w.glow[id]
		if press == nil {
			level = 0
		}
		p.roundRect(x, 8, 36, 30, 8, faded(colRaised, level))
		ink := mix(colMuted, colText, level)
		if press == nil {
			ink = faded(ink, .35)
		}
		cx, cy := x+18, 23.0
		if id == "minimize" {
			p.stroke([][2]float64{{cx - 5, cy}, {cx + 5, cy}}, 1.3, ink)
		} else {
			p.stroke([][2]float64{{cx - 4.5, cy - 4.5}, {cx + 4.5, cy + 4.5}}, 1.3, ink)
			p.stroke([][2]float64{{cx + 4.5, cy - 4.5}, {cx - 4.5, cy + 4.5}}, 1.3, ink)
		}
		if press != nil {
			w.controls = append(w.controls, control{id, x, 8, 36, 30, press})
		}
	}
}

func (w *setupWindow) drawWelcome(p *painter) {
	p.artwork("1loop-logo.png", windowWidth/2, 118, 92)
	p.text("Your project. Your model. One loop.", windowWidth/2, 184, 15, false, colMuted, .5)

	x, y, width := 140.0, 220.0, windowWidth-280
	p.card(x, y, width, 110, 14, colSurface, colBorder)
	p.text("Install in", x+20, y+28, 13, false, colMuted, 0)
	p.text(p.cut(w.dir, width-190, 13), x+92, y+28, 13, false, colText, 0)
	w.chip(p, "change", "Change", x+width-78, y+13, 62, 30, func() {
		if dir := w.browse(w.dir); dir != "" {
			w.dir = dir
		}
	})
	p.roundRect(x+20, y+55, width-40, 1, 0, colBorder)
	w.check(p, "menu", "Start menu shortcut", x+20, y+67, &w.menu)
	w.check(p, "desktop", "Desktop shortcut", x+width/2+6, y+67, &w.desktop)

	w.bigButton(p, w.button("install", "Install", w.startInstall), windowWidth/2-100, 358, 200, true)
	foot := "Version " + w.version + "  ·  for you alone, no administrator rights  ·  "
	link := "MPL-2.0 licence"
	total := p.width(foot, 12, false) + p.width(link, 12, false)
	fx := (windowWidth - total) / 2
	p.text(foot, fx, 440, 12, false, faded(colMuted, .8), 0)
	lx := fx + p.width(foot, 12, false)
	w.ring(p, "licence", lx-5, 428, p.width(link, 12, false)+10, 24, 6)
	p.text(link, lx, 440, 12, false, mix(colMuted, colText, w.glow["licence"]), 0)
	p.roundRect(lx, 448, p.width(link, 12, false), 1, 0, faded(colMuted, .3+.5*w.glow["licence"]))
	w.controls = append(w.controls, control{"licence", lx - 4, 428, p.width(link, 12, false) + 8, 24, w.licence})
}

func (w *setupWindow) drawWorking(p *painter) {
	cx, cy := windowWidth/2, 200.0
	p.arc(cx, cy, 74, 14, 0, 2*math.Pi, colRaised)
	p.arc(cx, cy, 74, 14, 0, max(w.drawn, .012)*2*math.Pi, colAmber)
	p.text(fmt.Sprintf("%d%%", int(math.Round(w.drawn*100))), cx, cy, 34, true, colText, .5)
	p.text("Installing 1loop", cx, 326, 22, true, colText, .5)
	status := "Writing the app"
	if w.progress >= .97 {
		status = "Adding it to Windows"
	}
	p.text(status, cx, 358, 14, false, colMuted, .5)
}

func (w *setupWindow) drawDone(p *painter, since float64) {
	cx, cy := windowWidth/2, 160.0
	w.drawn = 1
	p.arc(cx, cy, 62, 12, 0, 2*math.Pi, colAmber)
	// The check is drawn from its left end.
	t := math.Min(max(since-.2, 0)/.35, 1)
	t = 1 - math.Pow(1-t, 3)
	points := [][2]float64{{cx - 25, cy + 2}, {cx - 7, cy + 20}, {cx + 26, cy - 18}}
	first := 25.5 / (25.5 + 50.3)
	if t > 0 {
		a := min(t/first, 1)
		line := [][2]float64{points[0], {points[0][0] + (points[1][0]-points[0][0])*a, points[0][1] + (points[1][1]-points[0][1])*a}}
		if t > first {
			b := (t - first) / (1 - first)
			line = append(line, [2]float64{points[1][0] + (points[2][0]-points[1][0])*b, points[1][1] + (points[2][1]-points[1][1])*b})
		}
		p.stroke(line, 11, colText)
	}
	p.text("1loop is ready", cx, 268, 26, true, colText, .5)
	p.text("It keeps itself up to date: Settings → Updates.", cx, 304, 14.5, false, colMuted, .5)
	w.bigButton(p, w.button("open", "Open 1loop", func() { w.launch(); w.quit() }), cx-100, 350, 200, true)
	w.bigButton(p, w.button("close", "Close", w.quit), cx-60, 404, 120, false)
}

// drawQuestion is the artwork, what happens, why, and one or two buttons.
func (w *setupWindow) drawQuestion(p *painter, title, text string, ink color.NRGBA, buttons ...button) {
	cx := windowWidth / 2
	p.artwork("1loop-ring.png", cx, 140, 104)
	p.text(title, cx, 238, 25, true, ink, .5)
	for i, line := range w.lines(p, text, 500, 14.5) {
		p.text(line, cx, 276+float64(i)*22, 14.5, false, colMuted, .5)
	}
	for i, b := range buttons {
		w.bigButton(p, b, cx-100, 350+float64(i)*56, 200, i == 0)
	}
}

// lines breaks text at spaces, four lines at most.
func (w *setupWindow) lines(p *painter, text string, width, size float64) []string {
	var lines []string
	line := ""
	start := 0
	for i := 0; i <= len(text); i++ {
		if i < len(text) && text[i] != ' ' && text[i] != '\n' {
			continue
		}
		word := text[start:i]
		start = i + 1
		next := word
		if line != "" {
			next = line + " " + word
		}
		if line != "" && p.width(next, size, false) > width {
			lines = append(lines, line)
			next = word
		}
		line = next
	}
	lines = append(lines, line)
	if len(lines) > 4 {
		lines = append(lines[:3], lines[3]+"…")
	}
	return lines
}

type button struct {
	id, label string
	press     func()
}

func (w *setupWindow) button(id, label string, press func()) button { return button{id, label, press} }

func (w *setupWindow) bigButton(p *painter, b button, x, y, width float64, primary bool) {
	height, level := 46.0, w.glow[b.id]
	if !primary {
		height = 40
	}
	if w.pressed == b.id && w.hover == b.id {
		x, y, width, height = x+1, y+1, width-2, height-2
	}
	w.ring(p, b.id, x-4, y-4, width+8, height+8, 15)
	if primary {
		for i := 4; i > 0; i-- { // its light on the window
			grow := float64(i) * 4
			p.roundRect(x-grow, y-grow+3, width+2*grow, height+2*grow, 12+grow, faded(colAmber, .015+.02*level))
		}
		p.roundRect(x, y, width, height, 12, mix(colAmber, color.NRGBA{0xFF, 0xC6, 0x45, 0xFF}, level))
		p.text(b.label, x+width/2, y+height/2, 16, true, colOnAmber, .5)
	} else {
		p.roundRect(x, y, width, height, 11, faded(colRaised, level))
		p.text(b.label, x+width/2, y+height/2, 14.5, false, mix(colMuted, colText, level), .5)
	}
	w.controls = append(w.controls, control{b.id, x, y, width, height, b.press})
}

func (w *setupWindow) chip(p *painter, id, label string, x, y, width, height float64, press func()) {
	level := w.glow[id]
	w.ring(p, id, x-2, y-2, width+4, height+4, 11)
	p.card(x, y, width, height, 9, mix(colSurface, colRaised, .5+.5*level), colBorder)
	p.text(label, x+width/2, y+height/2, 12.5, false, mix(colMuted, colText, .4+.6*level), .5)
	w.controls = append(w.controls, control{id, x, y, width, height, press})
}

func (w *setupWindow) check(p *painter, id, label string, x, y float64, on *bool) {
	level := w.glow[id]
	width := 40 + p.width(label, 14, false)
	w.ring(p, id, x-5, y, width+6, 30, 8)
	if *on {
		p.roundRect(x, y+5, 20, 20, 6, colAmber)
		p.stroke([][2]float64{{x + 5.5, y + 15.3}, {x + 8.8, y + 18.6}, {x + 14.8, y + 11.6}}, 2.3, colOnAmber)
	} else {
		p.card(x, y+5, 20, 20, 6, colRaised, mix(color.NRGBA{0x45, 0x45, 0x4B, 0xFF}, colMuted, level))
	}
	p.text(label, x+32, y+15, 14, false, mix(colText, color.NRGBA{255, 255, 255, 255}, level), 0)
	w.controls = append(w.controls, control{id, x, y, width, 30, func() { *on = !*on }})
}

// ring shows the keyboard's focus under a control, before it is drawn.
func (w *setupWindow) ring(p *painter, id string, x, y, width, height, radius float64) {
	if w.keyboard && w.focus == id {
		p.card(x, y, width, height, radius, colSurface, colAmber)
	}
}

// at is the control under a point, "" for none.
func (w *setupWindow) at(x, y float64) *control {
	for i := len(w.controls) - 1; i >= 0; i-- {
		c := &w.controls[i]
		if x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h {
			return c
		}
	}
	return nil
}

func (w *setupWindow) mouseMove(x, y float64) {
	w.hover = ""
	if c := w.at(x, y); c != nil {
		w.hover = c.id
	}
}

func (w *setupWindow) mouseDown(x, y float64) {
	w.mouseMove(x, y)
	w.pressed, w.keyboard = w.hover, false
}

func (w *setupWindow) mouseUp(x, y float64) {
	w.mouseMove(x, y)
	pressed := w.pressed
	w.pressed = ""
	if c := w.at(x, y); c != nil && c.id == pressed {
		c.press()
	}
}

// key is the keyboard: tab, enter, space, escape. Enter without a focus is
// the screen's main button.
func (w *setupWindow) key(name string, shift bool) {
	var order []control
	for _, c := range w.controls {
		if c.id != "minimize" && c.id != "close-window" {
			order = append(order, c)
		}
	}
	at := -1
	for i, c := range order {
		if c.id == w.focus {
			at = i
		}
	}
	switch name {
	case "tab":
		if len(order) == 0 {
			return
		}
		w.keyboard = true
		if shift {
			at = max(at, 0) + len(order) - 1
		} else {
			at++
		}
		w.focus = order[at%len(order)].id
	case "enter", "space":
		if at >= 0 {
			order[at].press()
		} else if name == "enter" {
			for _, c := range order {
				switch c.id {
				case "install", "open", "retry", "remove", "close":
					c.press()
					return
				}
			}
		}
	case "escape":
		if w.view != viewWorking {
			w.quit()
		}
	}
}
