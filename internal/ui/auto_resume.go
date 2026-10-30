package ui

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

var (
	iconClock = lineIcon("clock", `<circle cx="8" cy="8" r="5.5"/><path d="M8 5v3.2l2.2 1.4"/>`)
	iconGrip  = lineIcon("grip", `<path d="M6 4h.01M10 4h.01M6 8h.01M10 8h.01M6 12h.01M10 12h.01" stroke-width="2.4"/>`)
)

// configureAutoResume edits what a rate limit does to a run, for every
// conversation: steps tried in order (wait for the reset, or go on with
// another account's model, the conversation sent as is or summarized),
// reordered by dragging them.
func (ui *desktopUI) configureAutoResume() {
	current := ui.autoResume
	policy := agent.AutoResume{}
	if current != nil {
		policy.Enabled, policy.Steps = current.Enabled, append([]agent.ResumeStep(nil), current.Steps...)
	}
	problem := note("")
	problem.Hide()
	say := func(text string) { problem.SetText(text); problem.Hidden = text == ""; problem.Refresh() }
	steps := &stepList{box: container.NewWithoutLayout(), dragging: -1}
	steps.box.Layout = stepLayout{steps}
	list := container.NewVScroll(steps.box)
	hint := textLine("Drag to reorder", 12, colFaint)
	var render func()
	render = func() {
		steps.rows = nil
		steps.box.Objects = nil
		for index, step := range policy.Steps {
			index := index
			row := newStepRow(steps, index, ui.resumeStepTitle(step), ui.resumeStepDetail(step), step.Action == "switch", func() {
				policy.Steps = append(policy.Steps[:index], policy.Steps[index+1:]...)
				render()
			})
			steps.rows = append(steps.rows, row)
			steps.box.Objects = append(steps.box.Objects, row)
		}
		if len(policy.Steps) == 0 {
			empty := surface(container.NewCenter(container.NewVBox(
				container.NewCenter(textLine("No steps yet", 13.5, colText)),
				container.NewCenter(textLine("Add a wait or another model below.", 12.5, colMuted)))), colField, colBorderSoft, 10, 18)
			steps.box.Objects = []fyne.CanvasObject{empty}
		}
		// As tall as the steps, up to four: more scroll.
		shown := float32(min(len(policy.Steps), 4))
		height := shown*stepHeight + (shown-1)*stepGap
		if len(policy.Steps) == 0 {
			height = steps.box.Objects[0].MinSize().Height
		}
		list.SetMinSize(fyne.NewSize(0, height))
		hint.Hidden = len(policy.Steps) < 2
		steps.box.Refresh()
	}
	steps.onDrop = func(from, to int) {
		step := policy.Steps[from]
		policy.Steps = append(policy.Steps[:from], policy.Steps[from+1:]...)
		policy.Steps = append(policy.Steps[:to], append([]agent.ResumeStep{step}, policy.Steps[to:]...)...)
		render()
	}
	render()
	add := func(step agent.ResumeStep) {
		policy.Steps = append(policy.Steps, step)
		say("")
		render()
	}
	full := func() bool {
		if len(policy.Steps) >= 16 {
			say("Use at most 16 steps.")
			return true
		}
		return false
	}
	wait := newRow("Wait for the reset", iconClock, styleOutline, func() {
		if !full() {
			add(agent.ResumeStep{Action: "wait"})
		}
	})
	model := newRow("Switch to a model", iconArrowTurn, styleOutline, func() {
		if full() {
			return
		}
		if len(ui.model.Options) == 0 {
			say("No model is available.")
			return
		}
		ui.addResumeModel(add)
	})
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	save := newRow("Save", nil, stylePrimary, func() {
		if err := policy.Validate(); err != nil {
			say(err.Error())
			return
		}
		copy := policy
		ui.autoResume = &copy
		ui.save()
		ui.remote.settingsChanged()
		dialog.dismiss()
	})
	enabled := card(settingRow("Resume automatically",
		"When a rate limit stops a conversation, 1loop tries these steps in order, in every conversation. Stop cancels a wait.",
		newToggle(policy.Enabled, func(on bool) { policy.Enabled = on })))
	heading := container.NewBorder(nil, nil, textLine("Steps", 13.5, colText), hint)
	body := container.NewVBox(enabled, padded(18, 8, 2, 2, heading), list,
		padded(10, 0, 0, 0, container.New(layout.NewCustomPaddedHBoxLayout(8), wait, model)), problem)
	dialog = ui.modal("Automatic resumption", body, 620, nil, cancel, save)
}

// resumeStepTitle and resumeStepDetail describe a step on its card.
func (ui *desktopUI) resumeStepTitle(step agent.ResumeStep) string {
	if step.Action != "switch" {
		return "Wait for the limit to reset"
	}
	for _, choice := range ui.choices {
		if choice.Connection.ID == step.ConnectionID && choice.ID == step.Model {
			return "Switch to " + choice.Display()
		}
	}
	return "Switch to " + step.Model
}

func (ui *desktopUI) resumeStepDetail(step agent.ResumeStep) string {
	if step.Action != "switch" {
		return "Resumes the same model when its quota comes back"
	}
	detail := step.ConnectionID
	for _, choice := range ui.choices {
		if choice.Connection.ID == step.ConnectionID {
			detail = choice.Connection.Name
			break
		}
	}
	if step.Handoff == "summary" {
		detail += " · conversation summarized"
	} else {
		detail += " · conversation sent as is"
	}
	if step.Effort != "" {
		detail += " · " + step.Effort + " reasoning"
	}
	return detail
}

const (
	stepHeight = 58
	stepGap    = 8
)

// stepList lays the steps out one under the other; the step being dragged
// follows the pointer and the others open the place it would drop into.
type stepList struct {
	box      *fyne.Container
	rows     []*stepRow
	dragging int     // the dragged step, -1 when none
	offset   float32 // how far it moved from its place
	onDrop   func(from, to int)
}

// target is where the dragged step would land.
func (list *stepList) target() int {
	to := list.dragging + int((list.offset+sign(list.offset)*(stepHeight+stepGap)/2)/(stepHeight+stepGap))
	return max(0, min(len(list.rows)-1, to))
}

func sign(value float32) float32 {
	if value < 0 {
		return -1
	}
	return 1
}

func (list *stepList) drag(index int, dy float32) {
	if list.dragging != index {
		list.dragging, list.offset = index, 0
		list.rows[index].setLifted(true)
		// Drawn last: above the steps it passes over.
		list.box.Objects = append(append(append([]fyne.CanvasObject(nil), list.box.Objects[:index]...), list.box.Objects[index+1:]...), list.rows[index])
	}
	list.offset += dy
	relayout(list.box)
}

func (list *stepList) drop() {
	if list.dragging < 0 {
		return
	}
	from, to := list.dragging, list.target()
	list.rows[from].setLifted(false)
	list.dragging, list.offset = -1, 0
	if from != to {
		list.onDrop(from, to)
		return
	}
	relayout(list.box)
}

type stepLayout struct{ list *stepList }

func (layout stepLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(layout.list.rows) == 0 {
		if len(objects) == 0 {
			return fyne.Size{}
		}
		return objects[0].MinSize()
	}
	n := float32(len(layout.list.rows))
	return fyne.NewSize(200, n*stepHeight+(n-1)*stepGap)
}

func (layout stepLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	list := layout.list
	if len(list.rows) == 0 {
		for _, object := range objects {
			object.Move(fyne.NewPos(0, 0))
			object.Resize(size)
		}
		return
	}
	slot := func(i int) float32 { return float32(i) * (stepHeight + stepGap) }
	to := -1
	if list.dragging >= 0 {
		to = list.target()
	}
	for i, row := range list.rows {
		row.Resize(fyne.NewSize(size.Width, stepHeight))
		place := i
		switch {
		case i == list.dragging:
			top := slot(i) + list.offset
			row.Move(fyne.NewPos(0, max(0, min(slot(len(list.rows)-1), top))))
			continue
		case list.dragging >= 0 && i > list.dragging && i <= to:
			place = i - 1
		case list.dragging >= 0 && i < list.dragging && i >= to:
			place = i + 1
		}
		row.Move(fyne.NewPos(0, slot(place)))
	}
}

// stepRow is one step's card: a grip, its number, what it does, and Remove.
// Dragging it anywhere but on Remove moves it.
type stepRow struct {
	widget.BaseWidget
	list    *stepList
	index   int
	lifted  bool
	hovered bool
	back    *canvas.Rectangle
	grip    *canvas.Image
	content fyne.CanvasObject
}

func newStepRow(list *stepList, index int, title, detail string, model bool, onRemove func()) *stepRow {
	row := &stepRow{list: list, index: index, back: canvas.NewRectangle(colSurface)}
	row.back.CornerRadius = 10
	row.back.StrokeWidth = 1
	row.grip = canvas.NewImageFromResource(tinted(iconGrip, colFaint))
	row.grip.SetMinSize(fyne.NewSquareSize(16))
	number := textLine(strconv.Itoa(index+1), 12, colText)
	number.TextStyle = fyne.TextStyle{Bold: true}
	badge := canvas.NewRectangle(colRaised)
	badge.CornerRadius = 11
	badge.SetMinSize(fyne.NewSquareSize(22))
	icon, tint := iconClock, colMuted
	if model {
		icon, tint = iconArrowTurn, colAmber
	}
	glyph := canvas.NewImageFromResource(tinted(icon, tint))
	glyph.SetMinSize(fyne.NewSquareSize(16))
	tile := canvas.NewRectangle(withAlpha(tint, 0.14))
	tile.CornerRadius = 8
	tile.SetMinSize(fyne.NewSquareSize(32))
	text := container.New(layout.NewCustomPaddedVBoxLayout(2), newFitLabel(title, 13.5, colText), newFitLabel(detail, 12, colMuted))
	remove := newIconButton(iconTrash, color.NRGBA{}, colMuted, onRemove)
	lead := container.New(layout.NewCustomPaddedHBoxLayout(10), container.NewCenter(row.grip),
		container.NewCenter(container.NewStack(badge, container.NewCenter(number))),
		container.NewCenter(container.NewStack(tile, container.NewCenter(glyph))))
	row.content = padded(0, 0, 10, 8, container.NewBorder(nil, nil, lead, container.NewCenter(remove),
		padded(0, 0, 12, 8, container.NewVBox(layout.NewSpacer(), text, layout.NewSpacer()))))
	row.ExtendBaseWidget(row)
	row.paint()
	return row
}

func (row *stepRow) paint() {
	switch {
	case row.lifted:
		row.back.FillColor, row.back.StrokeColor = colRaised, colAmber
	case row.hovered:
		row.back.FillColor, row.back.StrokeColor = colRaised, colBorderHot
	default:
		row.back.FillColor, row.back.StrokeColor = colSurface, colBorder
	}
	ink := colFaint
	if row.hovered || row.lifted {
		ink = colMuted
	}
	row.grip.Resource = tinted(iconGrip, ink)
	row.back.Refresh()
	row.grip.Refresh()
}

func (row *stepRow) setLifted(lifted bool) { row.lifted = lifted; row.paint() }

func (row *stepRow) Dragged(e *fyne.DragEvent) { row.list.drag(row.index, e.Dragged.DY) }
func (row *stepRow) DragEnd()                  { row.list.drop() }

func (row *stepRow) MouseIn(*desktop.MouseEvent)    { row.hovered = true; row.paint() }
func (row *stepRow) MouseMoved(*desktop.MouseEvent) {}
func (row *stepRow) MouseOut()                      { row.hovered = false; row.paint() }
func (row *stepRow) Cursor() desktop.Cursor         { return desktop.VResizeCursor }

func (row *stepRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(row.back, row.content))
}

// addResumeModel asks which model a step switches to, and how it receives
// the conversation.
func (ui *desktopUI) addResumeModel(added func(agent.ResumeStep)) {
	models := newPicker(ui.window, "Model", styleField, nil)
	models.Options = append([]string(nil), ui.model.Options...)
	models.SetSelected(models.Options[0])
	transfer := newPicker(ui.window, "Context", styleField, nil)
	transfer.Options = []string{"Send conversation as is", "Summarize with the new model"}
	transfer.SetSelected(transfer.Options[0])
	levels := newPicker(ui.window, "Summary", styleField, nil)
	levels.Options = []string{"Everything", "Without tool outputs", "Messages only", "Recent exchanges"}
	levels.SetSelected(levels.Options[0])
	levels.row.Hide()
	transfer.onChanged = func(value string) {
		if value == transfer.Options[1] {
			levels.row.Show()
		} else {
			levels.row.Hide()
		}
	}
	effort := newPicker(ui.window, "Reasoning", styleField, nil)
	updateEffort := func(label string) {
		choice := ui.choices[label]
		effort.Options = append([]string{"Default"}, choice.Efforts...)
		effort.SetSelected("Default")
	}
	models.onChanged = updateEffort
	updateEffort(models.Selected)
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	accept := newRow("Add", nil, stylePrimary, func() {
		choice, ok := ui.choices[models.Selected]
		if !ok {
			return
		}
		step := agent.ResumeStep{Action: "switch", ConnectionID: choice.Connection.ID, Model: choice.ID, Handoff: "as_is"}
		if effort.Selected != "Default" {
			step.Effort = effort.Selected
		}
		if transfer.Selected == transfer.Options[1] {
			step.Handoff = "summary"
			ids := []string{agent.LevelEverything, agent.LevelNoOutputs, agent.LevelMessages, agent.LevelRecent}
			for i, name := range levels.Options {
				if name == levels.Selected {
					step.Level = ids[i]
				}
			}
		}
		dialog.dismiss()
		added(step)
	})
	label := func(text string) fyne.CanvasObject { return padded(6, 2, 2, 0, textLine(text, 12.5, colMuted)) }
	summary := container.NewVBox(label("Summary includes"), levels.row)
	summary.Hide()
	transfer.onChanged = func(value string) { summary.Hidden = value != transfer.Options[1]; summary.Refresh() }
	levels.row.Show()
	dialog = ui.modal("Add a resume model", container.NewVBox(note("The new model receives the conversation as chosen below and uses its own account's quota."),
		label("Model"), models.row, label("Reasoning"), effort.row, label("Conversation sent to it"), transfer.row, summary), 550, nil, cancel, accept)
}
