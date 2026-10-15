package ui

// The task list's tab in the side panel, each task with its ring.

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

func (panel *toolPanel) openTodo() {
	for i, tab := range panel.tabs {
		if tab.kind == "todo" {
			panel.choose(i)
			panel.unfold()
			return
		}
	}
	panel.tabs = append(panel.tabs, panel.todoTab(panel.owner))
	panel.todoChanged(panel.owner)
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

func (panel *toolPanel) todoTab(chat *conversation) panelTab {
	view := container.NewVBox()
	footer := container.NewVBox()
	return panelTab{kind: "todo", title: "Tasks", icon: iconCheck, todoChat: chat, todoView: view, todoContext: footer,
		content: container.NewBorder(nil, padded(12, 14, 18, 18, footer), nil, nil, newKineticVScroll(padded(16, 18, 18, 18, view)))}
}

// todoChanged draws a chat's task list again in its tab, when the panel on
// screen is that chat's.
//
// Each task carries the 1loop ring as its mark: an empty grey ring while it
// waits, the ring turning with its dot while the agent works on it, the
// ring closed around a tick once it is done.
func (panel *toolPanel) todoChanged(chat *conversation) {
	panel.ui.remote.metaChanged(chat) // the phone shows the list too
	if chat != panel.owner {
		return // drawn when its chat comes back on screen
	}
	for n := range panel.tabs {
		tab := &panel.tabs[n]
		if tab.kind != "todo" {
			continue
		}
		tab.todoChat = chat
		box := tab.todoView
		tab.todoTimes = map[string]*canvas.Text{}
		box.Objects = nil
		for _, mark := range tab.todoMarks {
			mark.stop()
		}
		tab.todoMarks = nil
		var items []agent.TodoItem
		if chat != nil {
			items = chat.Todo.Snapshot()
		}
		done := 0
		for _, item := range items {
			if item.Status == "completed" {
				done++
			}
		}
		title := textLine("Tasks", 16, colText)
		title.TextStyle = fyne.TextStyle{Bold: true}
		if len(items) == 0 {
			box.Add(title)
			box.Add(padded(8, 0, 0, 0, plainFlow("No tasks yet. The agent lists them here when it plans your request, and ticks them off as it goes.", 13, colMuted)))
			box.Refresh()
			panel.refreshTodoStatus()
			continue
		}
		// The whole list at a glance: how many are done, and a line that fills.
		track, fill := canvas.NewRectangle(colBorder), canvas.NewRectangle(colAmber)
		track.CornerRadius, fill.CornerRadius = 1.5, 1.5
		progress := container.New(progressLayout{float32(done) / float32(len(items))}, track, fill)
		box.Add(container.NewBorder(nil, nil, title, textLine(fmt.Sprintf("%d of %d done", done, len(items)), 12.5, colMuted)))
		box.Add(padded(10, 12, 0, 0, progress))
		live := func() bool { // the ring only turns where it is seen, while the agent works
			return panel.owner == chat && chat != nil && chat.run != nil && panel.shown > 0 &&
				panel.active >= 0 && panel.active < len(panel.tabs) && panel.tabs[panel.active].todoView == box
		}
		for _, item := range items {
			ink, style := color.Color(colText), fyne.TextStyle{}
			switch item.Status {
			case "completed":
				ink = colMuted
			case "in_progress":
				style.Bold = true
			}
			text := plainFlow(item.Text, 13.5, ink)
			text.bold = style.Bold
			mark := newTaskMark(item.Status, live)
			tab.todoMarks = append(tab.todoMarks, mark)
			clock := textLine(taskElapsed(item, time.Now()), 11.5, colMuted)
			tab.todoTimes[item.ID] = clock
			box.Add(padded(7, 7, 0, 0, container.NewBorder(nil, nil, container.NewVBox(mark), container.NewVBox(clock), padded(1, 0, 12, 8, text))))
		}
		box.Refresh()
	}
	panel.refreshTodoStatus()
}

// progressLayout draws a thin line filled up to a share of its width.
type progressLayout struct{ share float32 }

func (layoutSpec progressLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(40, 3) }
func (layoutSpec progressLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(fyne.NewSize(size.Width, 3))
	objects[1].Resize(fyne.NewSize(size.Width*layoutSpec.share, 3))
}

// taskMark is the 1loop ring as a task's mark, drawn from the same layers
// as the work ring (branding.go) at the mark's own size.
type taskMark struct {
	widget.BaseWidget
	status string
	live   func() bool
	image  *canvas.Image
	anim   *fyne.Animation
	px     int
	drawn  time.Time
}

const taskMarkSize = 20

var taskArt = map[int]*ringArt{} // the ring's layers, by size in pixels

func newTaskMark(status string, live func() bool) *taskMark {
	mark := &taskMark{status: status, live: live, image: canvas.NewImageFromImage(nil)}
	mark.image.FillMode = canvas.ImageFillContain
	mark.ExtendBaseWidget(mark)
	return mark
}

func (mark *taskMark) art() *ringArt {
	px := devicePixels(fyne.CurrentApp().Driver().CanvasForObject(mark), taskMarkSize)
	if px <= 0 {
		px = 2 * taskMarkSize // not on a window yet: right on a dense screen
	}
	mark.px = px
	if taskArt[px] == nil {
		taskArt[px] = newRingArt(px)
	}
	return taskArt[px]
}

// draw composes the mark. The turning ring follows the clock, not the mark:
// a list drawn again in the middle of a turn goes on from the same angle.
func (mark *taskMark) draw() {
	art := mark.art()
	out := image.NewNRGBA(image.Rect(0, 0, art.px, art.px))
	layer := func(src image.Image) { draw.Draw(out, out.Bounds(), src, image.Point{}, draw.Over) }
	switch mark.status {
	case "completed":
		layer(art.full("#FFB000"))
		layer(art.icon("done", 1))
	case "in_progress":
		angle := 0.0
		if mark.live() {
			angle = float64(time.Now().UnixNano()%int64(1100*time.Millisecond)) / float64(1100*time.Millisecond) * 360
		}
		layer(art.open(angle))
		layer(art.dot(angle))
	default:
		layer(art.full("#3A3A3A"))
	}
	mark.image.Image = out
	mark.image.Refresh()
}

func (mark *taskMark) CreateRenderer() fyne.WidgetRenderer {
	mark.draw()
	if mark.status == "in_progress" {
		mark.anim = fyne.NewAnimation(time.Second, func(float32) {
			if now := time.Now(); mark.live() && now.Sub(mark.drawn) >= 30*time.Millisecond {
				mark.drawn = now
				mark.draw()
			}
		})
		mark.anim.RepeatCount = fyne.AnimationRepeatForever
		mark.anim.Start()
	}
	return &taskMarkRenderer{mark}
}

func (mark *taskMark) stop() {
	if mark.anim != nil {
		mark.anim.Stop()
		mark.anim = nil
	}
}

type taskMarkRenderer struct{ taskMark *taskMark }

func (renderer *taskMarkRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(taskMarkSize) }
func (renderer *taskMarkRenderer) Layout(size fyne.Size) {
	renderer.taskMark.image.Resize(fyne.NewSquareSize(taskMarkSize))
	if px := devicePixels(fyne.CurrentApp().Driver().CanvasForObject(renderer.taskMark), taskMarkSize); px > 0 && px != renderer.taskMark.px {
		renderer.taskMark.draw() // now on a window: at its real density
	}
}

func (renderer *taskMarkRenderer) Refresh() { renderer.taskMark.draw() }
func (renderer *taskMarkRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.taskMark.image}
}

func (renderer *taskMarkRenderer) Destroy() { renderer.taskMark.stop() }

func taskElapsed(item agent.TodoItem, now time.Time) string {
	if item.StartedAt == nil {
		return ""
	}
	end := now
	if item.CompletedAt != nil {
		end = *item.CompletedAt
	}
	seconds := max(0, int(end.Sub(*item.StartedAt).Seconds()))
	if seconds >= 3600 {
		return fmt.Sprintf("%d h %02d min", seconds/3600, (seconds%3600)/60)
	}
	return fmt.Sprintf("%d min %02d s", seconds/60, seconds%60)
}

func (panel *toolPanel) refreshTodoStatus() {
	if panel.active < 0 || panel.active >= len(panel.tabs) || panel.target == 0 {
		return
	}
	tab := &panel.tabs[panel.active]
	if tab.kind != "todo" || tab.todoContext == nil {
		return
	}
	now := time.Now()
	if panel.owner != nil {
		for _, item := range panel.owner.Todo.Snapshot() {
			if clock := tab.todoTimes[item.ID]; clock != nil {
				value := taskElapsed(item, now)
				if value != clock.Text {
					clock.Text = value
					clock.Refresh()
				}
			}
		}
	}
	cfg := panel.ui.cfg
	state := ""
	if panel.owner != nil {
		cfg = panel.owner.Config
		if panel.owner.run != nil {
			state = panel.owner.run.turn.mark
		}
	}
	text := agent.TaskContextText(cfg, panel.ui.contextRing.used, panel.ui.contextRing.window, panel.ui.classicCompaction, state)
	if text != tab.todoContextText {
		tab.todoContextText = text
		rule := canvas.NewRectangle(colBorderSoft)
		rule.SetMinSize(fyne.NewSize(0, 1))
		tab.todoContext.Objects = []fyne.CanvasObject{rule}
		for _, line := range strings.Split(text, "\n") {
			tab.todoContext.Objects = append(tab.todoContext.Objects, plainFlow(line, 12, colMuted))
		}
		tab.todoContext.Refresh()
	}
}
