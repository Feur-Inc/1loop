// SPDX-License-Identifier: MPL-2.0

package ui

// What a server window shows while it waits for its server (#591): a thin
// line along the bottom of the title bar once a request has taken longer
// than 300 ms, and a light placeholder in a chat opened before its items
// came.

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// slowRequest is how long a request runs before the activity line shows.
const slowRequest = 300 * time.Millisecond

// activityLine is a short accent segment gliding along a 2 px line; still,
// with the animations off.
type activityLine struct {
	widget.BaseWidget
	bar   *canvas.Rectangle
	anim  *fyne.Animation
	phase float32
}

func newActivityLine() *activityLine {
	line := &activityLine{bar: canvas.NewRectangle(colAmber)}
	line.ExtendBaseWidget(line)
	line.Hide()
	return line
}

func (line *activityLine) CreateRenderer() fyne.WidgetRenderer {
	return &activityLineRenderer{line}
}

func (line *activityLine) start() {
	if line.Visible() {
		return
	}
	line.Show()
	line.phase = 0.4
	if !motionOn() {
		line.Refresh()
		return
	}
	line.anim = fyne.NewAnimation(1200*time.Millisecond, func(progress float32) {
		line.phase = progress
		line.Refresh()
	})
	line.anim.RepeatCount = fyne.AnimationRepeatForever
	line.anim.Curve = fyne.AnimationEaseInOut
	line.anim.Start()
}

func (line *activityLine) stop() {
	if line == nil {
		return
	}
	if line.anim != nil {
		line.anim.Stop()
		line.anim = nil
	}
	line.Hide()
}

type activityLineRenderer struct{ line *activityLine }

func (r *activityLineRenderer) Layout(size fyne.Size) {
	// Cut to the line's own width: nothing clips it, and it would run over
	// the sidebar as it comes in.
	width := size.Width * 0.3
	left := (size.Width+width)*r.line.phase - width
	right := fyne.Min(size.Width, left+width)
	left = fyne.Max(0, left)
	r.line.bar.Move(fyne.NewPos(left, size.Height-2))
	r.line.bar.Resize(fyne.NewSize(fyne.Max(0, right-left), 2))
}

func (r *activityLineRenderer) MinSize() fyne.Size { return fyne.NewSize(0, 2) }

func (r *activityLineRenderer) Refresh() {
	r.line.bar.FillColor = colAmber // the theme's, as it is now
	r.Layout(r.line.Size())
	r.line.bar.Refresh()
}

func (r *activityLineRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.line.bar} }
func (r *activityLineRenderer) Destroy()                     {}

// busy counts the requests on their way, on the window's thread: the
// activity line shows when one takes longer than slowRequest, and goes
// when none is left.
func (remote *remoteDesktop) busy(delta int) {
	remote.live.inflight = max(0, remote.live.inflight+delta)
	line := remote.parent.serverBusy
	if line == nil {
		return
	}
	if remote.live.inflight == 0 {
		remote.live.slow++ // a timer started before does nothing
		line.stop()
		return
	}
	if delta > 0 && remote.live.inflight == 1 {
		remote.live.slow++
		started := remote.live.slow
		time.AfterFunc(slowRequest, func() {
			fyne.Do(func() {
				if remote.live.slow == started && remote.live.inflight > 0 && remote.parent.server == remote {
					line.start()
				}
			})
		})
	}
}

// showLoading shows the open chat's placeholder until its items came.
func (remote *remoteDesktop) showLoading(on bool) {
	ui := remote.parent
	if ui.chatWaitView == nil || ui.loadingChat != nil { // a long chat being drawn shows its own
		return
	}
	if on {
		ui.welcome.Hide()
		ui.scroll.Show()
		ui.chatWaitView.Show()
		ui.chatWait.setState("wait")
		return
	}
	ui.chatWaitView.Hide()
	ui.chatWait.setState("brand")
}
