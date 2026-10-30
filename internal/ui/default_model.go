package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/agent"
)

func (ui *desktopUI) defaultModelFields(draft *agent.Connection, models []agent.ModelChoice) fyne.CanvasObject {
	rows := []fyne.CanvasObject{settingRow("Default for this account",
		"Optional: choose a model for each preset level. Select Default in the composer to use it. Only configured levels are offered; other accounts are unchanged.", nil)}
	if len(models) == 0 {
		rows = append(rows, settingRow("Load this account's models above", "Load the catalog to choose models, or clear levels below.", nil))
	}
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		effort := effort
		picker := newPicker(ui.window, "Not configured", styleField, nil)
		picker.Options = []string{"Not configured"}
		ids := map[string]string{}
		selected := "Not configured"
		for _, model := range models {
			if model.ID == "default" {
				continue
			}
			label := model.Name + " · " + model.ID
			picker.Options = append(picker.Options, label)
			ids[label] = model.ID
			if model.ID == draft.DefaultModels[effort] {
				selected = label
			}
		}
		if id := draft.DefaultModels[effort]; id != "" && selected == "Not configured" {
			selected = "Unavailable · " + id
			picker.Options = append(picker.Options, selected)
			ids[selected] = id
		}
		picker.SetSelected(selected)
		detail := note("")
		show := func() {
			choice := agent.ModelChoice{ID: "default", Connection: *draft, DefaultTargets: models}
			detail.SetText(choice.DefaultAbout(effort))
			if draft.DefaultModels[effort] == "" {
				detail.SetText("Not offered in Default")
			}
		}
		picker.onChanged = func(label string) {
			if draft.DefaultModels == nil {
				draft.DefaultModels = map[string]string{}
			}
			if id := ids[label]; id != "" {
				draft.DefaultModels[effort] = id
			} else {
				delete(draft.DefaultModels, effort)
			}
			show()
		}
		show()
		rows = append(rows, settingRow(agent.EffortLabel(effort), "", sized(340, container.NewVBox(picker.row, detail))))
	}
	return card(rows...)
}

func (remote *remoteDesktop) defaultPresetSettings() fyne.CanvasObject {
	ui := remote.parent
	rows := container.NewVBox(ui.prefs.pageTitle("Connections", "", nil),
		note("Default presets for the connected server. Changes are saved on that server, separately for each account."))
	if !remote.defaultPresets {
		rows.Add(note("Update the server to configure Default presets here."))
		return rows
	}
	for _, account := range remote.accounts {
		account := account
		edit := newRow("Default preset…", nil, styleOutline, func() {
			for _, latest := range remote.accounts {
				if latest.ID == account.ID {
					account = latest
					break
				}
			}
			draft := cloneConnection(agent.Connection{ID: account.ID, DefaultModels: account.DefaultModels})
			var models []agent.ModelChoice
			for _, model := range remote.models {
				if model.Account != account.ID || model.ID == "default" {
					continue
				}
				choice := agent.ModelChoice{Connection: draft, ID: model.ID, Name: model.Name, DefaultEffort: model.DefaultEffort}
				for _, effort := range model.Efforts {
					choice.Efforts = append(choice.Efforts, effort.Value)
				}
				models = append(models, choice)
			}
			var dialog *sheet
			problem := note("")
			cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
			save := newRow("Save", nil, stylePrimary, nil)
			save.onTap = func() {
				save.Disable()
				accountID, models := account.ID, cloneConnection(draft).DefaultModels
				go func() {
					err := remote.client.json(remote.ctx, "POST", "/api/model/defaults", map[string]any{"account": accountID, "models": models}, nil)
					fyne.Do(func() {
						save.Enable()
						if err != nil {
							problem.SetText(err.Error())
							return
						}
						dialog.dismiss()
						select {
						case remote.wake <- struct{}{}:
						default:
						}
					})
				}()
			}
			dialog = ui.modal(account.Name+" · Default", container.NewVBox(ui.defaultModelFields(&draft, models), problem), 620, nil, cancel, save)
		})
		rows.Add(card(settingRow(account.Name, account.Kind, edit)))
	}
	return rows
}
