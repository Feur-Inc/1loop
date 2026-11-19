package widget

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	col "fyne.io/fyne/v2/internal/color"
	"fyne.io/fyne/v2/theme"
)

var timeNow = time.Now // used in tests

const (
	cursorInterruptTime = 300 * time.Millisecond
	cursorFadeAlpha     = uint8(0x16)
	cursorFadeRatio     = 0.1
	// cursorBlinkFor is how long the caret blinks after the last key or
	// focus; then it stays solid and the window has nothing to redraw
	// (GTK's gtk-cursor-blink-timeout does the same, 10 s). (1loop)
	cursorBlinkFor = 10 * time.Second
)

// CaretBlinks, when set, says whether the caret may blink at all; when it
// returns false the caret stays solid. (1loop: Animations off.)
var CaretBlinks func() bool

type entryCursorAnimation struct {
	cursor            *canvas.Rectangle
	anim              *fyne.Animation
	lastInterruptTime time.Time
	blinking          bool // between start and stop (1loop)
}

func newEntryCursorAnimation(cursor *canvas.Rectangle) *entryCursorAnimation {
	return &entryCursorAnimation{cursor: cursor}
}

// creates fyne animation
func (a *entryCursorAnimation) createAnim(inverted bool) *fyne.Animation {
	cursorOpaque := theme.Color(theme.ColorNamePrimary)
	ri, gi, bi, ai := col.ToNRGBA(cursorOpaque)
	r := uint8(ri >> 8)
	g := uint8(gi >> 8)
	b := uint8(bi >> 8)
	endA := uint8(ai >> 8)
	startA := cursorFadeAlpha
	cursorDim := color.NRGBA{R: r, G: g, B: b, A: cursorFadeAlpha}
	if inverted {
		a.cursor.FillColor = cursorOpaque
		startA, endA = endA, startA
	} else {
		a.cursor.FillColor = cursorDim
	}

	deltaA := endA - startA
	fadeStart := float32(0.5 - cursorFadeRatio)
	fadeStop := float32(0.5 + cursorFadeRatio)

	interrupted := false
	anim := fyne.NewAnimation(time.Second/2, func(f float32) {
		since := timeNow().Sub(a.lastInterruptTime)
		if since > cursorBlinkFor || (CaretBlinks != nil && !CaretBlinks()) {
			// Solid until the next key: the animation stops, so the
			// runner and the window rest (1loop).
			a.cursor.FillColor = cursorOpaque
			a.cursor.Refresh()
			a.anim.Stop()
			a.anim = nil
			return
		}
		shouldInterrupt := since <= cursorInterruptTime
		if shouldInterrupt {
			if !interrupted {
				a.cursor.FillColor = cursorOpaque
				a.cursor.Refresh()
				interrupted = true
			}
			return
		}
		if interrupted {
			a.anim.Stop()
			if !inverted {
				a.anim = a.createAnim(true)
			}
			interrupted = false
			canStart := a.anim != nil
			if canStart {
				a.anim.Start()
			}
			return
		}

		alpha := uint8(0)
		if f < fadeStart {
			if _, _, _, al := a.cursor.FillColor.RGBA(); uint8(al>>8) == cursorFadeAlpha {
				return
			}

			a.cursor.FillColor = cursorDim
		} else if f >= fadeStop {
			if _, _, _, al := a.cursor.FillColor.RGBA(); al == 0xffff {
				return
			}

			a.cursor.FillColor = cursorOpaque
		} else {
			fade := (f + cursorFadeRatio - 0.5) * (1 / (cursorFadeRatio * 2))
			alpha = uint8(float32(deltaA) * fade)
			a.cursor.FillColor = color.NRGBA{R: r, G: g, B: b, A: alpha}
		}

		a.cursor.Refresh()
	})

	anim.RepeatCount = fyne.AnimationRepeatForever
	anim.AutoReverse = true
	return anim
}

// starts cursor animation.
func (a *entryCursorAnimation) start() {
	a.lastInterruptTime = timeNow()
	isStopped := a.anim == nil
	a.blinking = true
	if isStopped {
		a.anim = a.createAnim(false)
		a.anim.Start()
	}
}

// temporarily stops the animation by "cursorInterruptTime".
func (a *entryCursorAnimation) interrupt() {
	a.lastInterruptTime = timeNow()
	if a.anim == nil && a.blinking {
		// It had stopped blinking: blink again (1loop).
		a.anim = a.createAnim(true)
		a.anim.Start()
	}
}

// stops cursor animation.
func (a *entryCursorAnimation) stop() {
	a.blinking = false
	if a.anim != nil {
		a.anim.Stop()
		a.anim = nil
	}
}
