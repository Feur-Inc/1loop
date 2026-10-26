package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// configureAutoResume edits what a rate limit does to a run, for every
// conversation: options tried in order (wait for the reset, or go on with
// another account's model, the conversation sent as is or summarized).
func (ui *desktopUI) configureAutoResume() {
	current := ui.autoResume
	policy := agent.AutoResume{}
	if current != nil {
		policy.Enabled, policy.Steps = current.Enabled, append([]agent.ResumeStep(nil), current.Steps...)
	}
	rows := container.New(layout.NewCustomPaddedVBoxLayout(5))
	problem := note("")
	var render func()
	render = func() {
		rows.Objects = nil
		for index, step := range policy.Steps {
			index, step := index, step
			label := "Wait for the rate limit to reset"
			if step.Action == "switch" {
				method := "send as is"
				if step.Handoff == "summary" {
					method = "summarize"
				}
				label = step.Model + " · " + method
				for _, choice := range ui.choices {
					if choice.Connection.ID == step.ConnectionID && choice.ID == step.Model {
						label = choice.Display() + " · " + choice.Connection.Name + " · " + method
						break
					}
				}
			}
			up := newRow("↑", nil, styleChip, func() {
				if index > 0 {
					policy.Steps[index], policy.Steps[index-1] = policy.Steps[index-1], policy.Steps[index]
					render()
				}
			})
			down := newRow("↓", nil, styleChip, func() {
				if index+1 < len(policy.Steps) {
					policy.Steps[index], policy.Steps[index+1] = policy.Steps[index+1], policy.Steps[index]
					render()
				}
			})
			remove := newRow("Remove", nil, styleChip, func() { policy.Steps = append(policy.Steps[:index], policy.Steps[index+1:]...); render() })
			if index == 0 {
				up.Disable()
			}
			if index+1 == len(policy.Steps) {
				down.Disable()
			}
			rows.Add(container.NewBorder(nil, nil, nil, container.NewHBox(up, down, remove), newFitLabel(fmt.Sprintf("%d. %s", index+1, label), 13, colText)))
		}
		if len(policy.Steps) == 0 {
			rows.Add(note("No resume options yet."))
		}
		rows.Refresh()
	}
	render()
	wait := newRow("Add wait", nil, styleOutline, func() {
		if len(policy.Steps) >= 16 {
			problem.SetText("Use at most 16 resume options.")
			return
		}
		policy.Steps = append(policy.Steps, agent.ResumeStep{Action: "wait"})
		render()
	})
	add := newRow("Add model", nil, styleOutline, func() {
		if len(policy.Steps) >= 16 {
			problem.SetText("Use at most 16 resume options.")
			return
		}
		if len(ui.model.Options) == 0 {
			problem.SetText("No model is available.")
			return
		}
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
			policy.Steps = append(policy.Steps, step)
			dialog.dismiss()
			render()
		})
		label := func(text string) fyne.CanvasObject { return padded(6, 2, 2, 0, textLine(text, 12.5, colMuted)) }
		summary := container.NewVBox(label("Summary includes"), levels.row)
		summary.Hide()
		transfer.onChanged = func(value string) { summary.Hidden = value != transfer.Options[1]; summary.Refresh() }
		levels.row.Show()
		dialog = ui.modal("Add a resume model", container.NewVBox(note("The new model receives the conversation as chosen below and uses its own account's quota."),
			label("Model"), models.row, label("Reasoning"), effort.row, label("Conversation sent to it"), transfer.row, summary), 550, nil, cancel, accept)
	})
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	save := newRow("Save", nil, stylePrimary, func() {
		if err := policy.Validate(); err != nil {
			problem.SetText(err.Error())
			return
		}
		copy := policy
		ui.autoResume = &copy
		ui.save()
		ui.remote.settingsChanged()
		dialog.dismiss()
	})
	list := container.NewVScroll(rows)
	list.SetMinSize(fyne.NewSize(0, 140))
	body := container.NewVBox(note("When a rate limit interrupts a conversation, 1loop tries these options in order, for every conversation. Stop cancels a wait. Changes apply to the next run."),
		container.NewHBox(widget.NewLabel("Automatic resumption"), newToggle(policy.Enabled, func(on bool) { policy.Enabled = on })), list,
		container.NewHBox(wait, add), problem)
	dialog = ui.modal("Automatic resumption", body, 650, nil, cancel, save)
}
