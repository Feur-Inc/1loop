// SPDX-License-Identifier: MPL-2.0

package ui

// Motion: the interface's short animations (hover, press, menus, arrivals)
// all start here, so one switch stops them. They are off when the user
// turned Animations off in Settings, or when the desktop asks for reduced
// motion (motion_*.go): each change then lands at once.

import (
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/platform"
)

var (
	animationsOff bool // Settings: Animations off
	systemReduced bool // the desktop asks for reduced motion
)

// motionOn says whether changes animate.
func motionOn() bool { return !animationsOff && !systemReduced }

// motion runs tick from 0 to 1 over d along curve, and returns the running
// animation. With motion off it calls tick(1) at once and returns nil, so
// nothing is redrawn for frames that would not move.
func motion(d time.Duration, curve fyne.AnimationCurve, tick func(float32)) *fyne.Animation {
	if !motionOn() {
		tick(1)
		return nil
	}
	anim := fyne.NewAnimation(d, tick)
	anim.Curve = curve
	anim.Start()
	return anim
}

// easeOutCubic slows down gently: things that travel (rows, the
// selection) stay visible on their way, where easeOutQuint would all but
// jump there.
func easeOutCubic(x float32) float32 {
	x = 1 - x
	return 1 - x*x*x
}

// stopMotion stops an animation motion returned, if it still runs.
func stopMotion(anim **fyne.Animation) {
	if *anim != nil {
		(*anim).Stop()
		*anim = nil
	}
}

// watchReducedMotion reads the desktop's reduced-motion preference and
// follows its changes.
func watchReducedMotion() {
	go followReducedMotion(func(reduced bool) {
		fyne.Do(func() {
			if reduced != systemReduced {
				platform.Devf("ui", "system reduced motion %v", reduced)
			}
			systemReduced = reduced
		})
	})
}
