// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

// namingChoice is the chat naming model (Settings → Models & accounts),
// when one is chosen and still offered by an enabled connection.
func (ui *desktopUI) namingChoice() (agent.ModelChoice, bool) {
	model := ui.namingModel
	if !model.Set() {
		return agent.ModelChoice{}, false
	}
	for _, choice := range ui.choices {
		if choice.Connection.ID == model.ConnectionID && choice.ID == model.Model && choice.Connection.Enabled {
			return choice, true
		}
	}
	return agent.ModelChoice{}, false
}

// nameNewChat has the naming model, when one is chosen, sum a new chat's
// first prompt up into its title: only the words typed, never its pictures
// or files (agent.NamingPrompt). The title shows when it comes, unless the
// chat was renamed or deleted meanwhile; until then, and on a failure, the
// chat keeps the start of its prompt.
func (ui *desktopUI) nameNewChat(chat *conversation, prompt agent.Message) {
	choice, ok := ui.namingChoice()
	if !ok || prompt.From != nil || prompt.Internal || agent.NamingPrompt(prompt).Content == "" {
		return
	}
	cfg := agent.ResumeConfig(chat.Config, choice, ui.keys[choice.Connection.ID], "")
	placeholder := chat.Title
	ctx := ui.ctx
	go func() {
		title, err := ui.backend.NameChat(ctx, cfg, prompt)
		if err != nil {
			platform.Devf("ui", "chat naming model: %v", err)
			return
		}
		ui.onUI(ctx, func() {
			if ui.chatByID(chat.ID) == chat && chat.Title == placeholder {
				ui.setChatTitle(chat, title)
			}
		})
	}()
}

// namingOption is a model the naming model may be, as its picker shows it.
type namingOption struct {
	label string
	model *agent.NamingModel // nil: None
}

const namingNone = "None"

const namingDetail = "Names new chats from their first message, never its files."

// namingPicker chooses the chat naming model among options; set saves a
// choice (nil for None), and calls failed on the interface thread when it
// could not, which puts the previous choice back.
func namingPicker(window fyne.Window, options []namingOption, current *agent.NamingModel, set func(model *agent.NamingModel, failed func(error))) *picker {
	options = append([]namingOption{{label: namingNone}}, options...)
	selected := namingNone
	if current.Set() {
		selected = current.Model + " (unavailable)"
		found := false
		for _, option := range options {
			if option.model != nil && *option.model == *current {
				selected, found = option.label, true
			}
		}
		if !found {
			options = append(options, namingOption{label: selected, model: current})
		}
	}
	control := newPicker(window, "Chat naming model", styleField, nil)
	for _, option := range options {
		control.Options = append(control.Options, option.label)
	}
	control.SetSelected(selected)
	control.onChanged = func(label string) {
		for _, option := range options {
			if option.label != label {
				continue
			}
			previous := selected
			selected = label
			set(option.model, func(err error) {
				platform.Devf("ui", "chat naming model: %v", err)
				selected, control.Selected = previous, previous // without saving it again
				control.refresh()
			})
			return
		}
	}
	return control
}

// namingOptions are this computer's models the naming model may be.
func (ui *desktopUI) namingOptions() []namingOption {
	var options []namingOption
	seen := map[string]bool{}
	for _, choice := range ui.choices {
		if !choice.Connection.Enabled {
			continue
		}
		label := choice.Display() + " · " + choice.Connection.Name
		if seen[label] {
			continue
		}
		seen[label] = true
		options = append(options, namingOption{label: label, model: &agent.NamingModel{ConnectionID: choice.Connection.ID, Model: choice.ID}})
	}
	slices.SortFunc(options, func(a, b namingOption) int { return strings.Compare(a.label, b.label) })
	return options
}

// namingSetting is the Chat naming model row of Models & accounts.
func (ui *desktopUI) namingSetting() fyne.CanvasObject {
	control := namingPicker(ui.window, ui.namingOptions(), ui.namingModel, func(model *agent.NamingModel, _ func(error)) {
		ui.namingModel = model
		ui.save()
	})
	return settingRow("Chat naming model", namingDetail, sized(260, control.row))
}

// namingSetting is the server's Chat naming model, for its chats.
func (remote *remoteDesktop) namingSetting() fyne.CanvasObject {
	var options []namingOption
	for _, model := range remote.models {
		label := model.Name
		if model.Detail != "" {
			label += " · " + model.Detail
		}
		options = append(options, namingOption{label: label, model: &agent.NamingModel{ConnectionID: model.Account, Model: model.ID}})
	}
	control := namingPicker(remote.parent.window, options, remote.namingModel, func(model *agent.NamingModel, failed func(error)) {
		go func() { // off the interface thread
			ctx, cancel := context.WithTimeout(remote.ctx, 10*time.Second)
			defer cancel()
			err := remote.client.json(ctx, "POST", "/api/naming-model", map[string]any{"model": model}, nil)
			fyne.Do(func() {
				if err != nil {
					failed(err)
					return
				}
				remote.namingModel = model
			})
		}()
	})
	return settingRow("Chat naming model", namingDetail, sized(260, control.row))
}
