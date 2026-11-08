// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Connections while a server's chats are on screen: the
// server's accounts (internal/server: /api/connections), which its chats
// use. Their keys stay on the server: the page only says one is saved, and
// sends a new one when it is typed.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// serverConnection is a row of GET /api/connections.
type serverConnection struct {
	Connection agent.Connection
	HasKey     bool
	Problem    string
	SignedIn   bool
	Email      string
}

// The types a connection can be added with from here: a ChatGPT sign-in
// comes back to the server itself.
var serverConnectionTypes = []string{"OpenAI-compatible API", "OpenRouter", "Local vLLM", "Claude Code", "Antigravity CLI"}

func (remote *remoteDesktop) connectionSettings() fyne.CanvasObject {
	ui := remote.parent
	content := container.NewStack(note("Loading the server's connections…"))
	add := newRow("Add connection", iconPlus, stylePrimary, func() {
		remote.connectionEditor(serverConnection{Connection: agent.Connection{Name: "New connection", Provider: "compatible", Enabled: true, AllModels: true}})
	})
	add.Hide()
	page := container.NewVBox(ui.prefs.pageTitle("Connections", "", container.NewCenter(add)),
		padded(0, 10, 2, 0, plainFlow("The server's accounts: chats on "+remote.name+" use the models of every enabled one. Their keys stay on the server.", 13, colMuted)),
		content)
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 20*time.Second)
		defer cancel()
		var list struct{ Connections []serverConnection }
		err := remote.client.json(ctx, "GET", "/api/connections", nil, &list)
		fyne.Do(func() {
			switch {
			case err != nil && strings.HasPrefix(err.Error(), "404"): // an older server: its Default presets only
				content.Objects = []fyne.CanvasObject{remote.defaultPresetSettings()}
				page.Objects = []fyne.CanvasObject{content}
				page.Refresh()
				return
			case err != nil:
				content.Objects = []fyne.CanvasObject{note(err.Error())}
			default:
				add.Show()
				content.Objects = []fyne.CanvasObject{remote.connectionList(list.Connections)}
			}
			content.Refresh()
		})
	}()
	return page
}

func (remote *remoteDesktop) connectionList(connections []serverConnection) fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, row := range connections {
		row := row
		enabled := newToggle(row.Connection.Enabled, func(on bool) {
			connection := cloneConnection(row.Connection)
			connection.Enabled = on
			remote.saveConnection(map[string]any{"connection": connection}, nil)
		})
		edit := newRow("Edit", iconChevronRight, styleChip, func() { remote.connectionEditor(row) })
		edit.iconAfter = true
		rows = append(rows, settingRow(row.Connection.Name, connectionDetail(row), container.New(layout.NewCustomPaddedHBoxLayout(14), container.NewCenter(enabled), edit)))
	}
	if len(rows) == 0 {
		rows = append(rows, settingRow("No connection yet", "Add one to get started.", nil))
	}
	box := container.NewVBox(card(rows...))
	if remote.defaultPresets && len(remote.accounts) > 0 {
		box.Add(sectionTitle("Default presets"))
		for _, row := range remote.presetRows() {
			box.Add(row)
		}
	}
	return box
}

func connectionDetail(row serverConnection) string {
	parts := []string{agent.ProviderName(row.Connection.Provider)}
	switch {
	case row.Email != "":
		parts = append(parts, row.Email)
	case row.Connection.Provider == "openai" || row.Connection.Provider == "claudecode":
		if !row.SignedIn {
			parts = append(parts, "not signed in on the server")
		}
	case row.HasKey:
		parts = append(parts, "key saved on the server")
	}
	if row.Problem != "" {
		parts = append(parts, row.Problem)
	}
	return strings.Join(parts, " · ")
}

// saveConnection posts a connection, then has the models read again and
// calls done (with the error) on the interface's thread.
func (remote *remoteDesktop) saveConnection(body map[string]any, done func(error)) {
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 30*time.Second)
		defer cancel()
		err := remote.client.json(ctx, "POST", "/api/connections", body, nil)
		remote.notify() // the picker: the server reads the models again
		fyne.Do(func() {
			if done != nil {
				done(err)
				return
			}
			if err != nil {
				remote.parent.notice("Connections", err.Error())
			}
			remote.parent.prefs.show("Connections")
		})
	}()
}

// connectionEditor is a server connection's page: a draft, saved as a whole.
func (remote *remoteDesktop) connectionEditor(row serverConnection) {
	ui := remote.parent
	settings := ui.prefs
	draft := cloneConnection(row.Connection)
	existing := draft.ID != ""
	name := widget.NewEntry()
	name.SetText(draft.Name)
	base := widget.NewEntry()
	base.SetText(draft.BaseURL)
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder("The service's API key")
	if row.HasKey {
		key.SetPlaceHolder("Saved on the server: type a new one to replace it")
	}
	enabled := newToggle(draft.Enabled, func(on bool) { draft.Enabled = on })
	status := plainFlow("", 13, colMuted)
	say := func(text string) { setFlow(status, text) }

	types := serverConnectionTypes
	if !agent.Contains([]string{"compatible", "openrouter", "vllm", "claudecode", "antigravity"}, draft.Provider) {
		types = []string{agent.ProviderName(draft.Provider)} // signed in on the server: it stays what it is
	}
	typePicker := newPicker(ui.window, "Connection type", styleField, nil)
	typePicker.Options = types
	apiFields := card(
		settingRow("API base URL", "The address of the service's /v1 API, as the server reaches it.", sized(340, base)),
		settingRow("API key", "", sized(340, key)))
	harness := note("")
	harnessFields := card(settingStack("On the server", "", harness))

	// Models: which ones of the catalog appear in the composer.
	var models []agent.ModelChoice
	search := widget.NewEntry()
	search.SetPlaceHolder("Search the models…")
	modelRows := container.New(layout.NewCustomPaddedVBoxLayout(0))
	allModels := newToggle(draft.AllModels, nil)
	var listModels func()
	listModels = func() {
		const shown = 150 // more rows than anyone reads: the search narrows them
		modelRows.Objects = nil
		query := strings.ToLower(strings.TrimSpace(search.Text))
		count, matching := 0, 0
		for _, model := range models {
			if !strings.Contains(strings.ToLower(model.Name+" "+model.ID), query) {
				continue
			}
			if matching++; count >= shown {
				continue
			}
			count++
			model := model
			include := newToggle(draft.Includes(model.ID), func(on bool) {
				if draft.AllModels { // choosing one model ends "all of them"
					draft.Models = nil
					for _, choice := range models {
						draft.Models = append(draft.Models, choice.ID)
					}
					draft.AllModels = false
					allModels.set(false)
				}
				chosen := []string{}
				for _, id := range draft.Models {
					if id != model.ID {
						chosen = append(chosen, id)
					}
				}
				if on {
					chosen = append(chosen, model.ID)
				}
				draft.Models = chosen
			})
			if count > 1 {
				rule := canvas.NewRectangle(colBorderSoft)
				rule.SetMinSize(fyne.NewSize(0, 1))
				modelRows.Add(padded(0, 0, 16, 16, rule))
			}
			modelRows.Add(padded(8, 8, 16, 12, container.NewBorder(nil, nil, container.NewCenter(include), nil,
				padded(0, 0, 12, 8, newFitLabel(model.Name, 13.5, colText)))))
		}
		switch {
		case len(models) == 0:
			modelRows.Add(settingRow("No catalog loaded", "Load the models to choose which ones to show.", nil))
		case matching == 0:
			modelRows.Add(settingRow("No model matches", "", nil))
		case matching > count:
			modelRows.Add(settingRow(fmt.Sprintf("%d more", matching-count), "Search to narrow the list.", nil))
		}
		modelRows.Refresh()
	}
	search.OnChanged = func(string) { listModels() }
	allModels.onChange = func(on bool) {
		draft.AllModels = on
		listModels()
	}
	var loadGeneration int
	load := newRow("Load models", nil, styleOutline, nil)
	load.onTap = func() {
		loadGeneration++
		generation := loadGeneration
		snapshot := cloneConnection(draft)
		snapshot.BaseURL = strings.TrimSpace(base.Text)
		body := map[string]any{"connection": snapshot}
		if key.Text != "" {
			body["apiKey"] = key.Text
		}
		say("The server is reading the model catalog…")
		go func() {
			ctx, cancel := context.WithTimeout(remote.ctx, 45*time.Second)
			defer cancel()
			var answer struct{ Models []struct{ ID, Name string } }
			err := remote.client.json(ctx, "POST", "/api/connections/models", body, &answer)
			fyne.Do(func() {
				if generation != loadGeneration {
					return
				}
				if err != nil {
					say(err.Error())
					return
				}
				models = nil
				for _, model := range answer.Models {
					models = append(models, agent.ModelChoice{ID: model.ID, Name: model.Name})
				}
				listModels()
				say(fmt.Sprintf("%d models loaded.", len(models)))
			})
		}()
	}

	typePicker.onChanged = func(label string) {
		previous := draft.Provider
		for _, id := range []string{"compatible", "openrouter", "vllm", "claudecode", "antigravity", draft.Provider} {
			if agent.ProviderName(id) == label {
				draft.Provider = id
				break
			}
		}
		apiFields.Hide()
		harnessFields.Hide()
		switch draft.Provider {
		case "claudecode":
			harness.SetText("Uses the Claude Code installed on the server, with its sign-in there (run claude once where the server runs, as its user).")
			harnessFields.Show()
		case "antigravity":
			harness.SetText("Uses the agy command installed on the server, with its Google sign-in there (run agy once where the server runs, as its user).")
			harnessFields.Show()
		case "openai", "chatgptweb":
			harness.SetText("Signed in where the server runs.")
			harnessFields.Show()
		default:
			apiFields.Show()
		}
		if draft.Provider == "openrouter" {
			base.SetText("https://openrouter.ai/api/v1")
			base.Disable()
		} else {
			base.Enable()
		}
		if draft.Provider == "vllm" && previous != draft.Provider {
			base.SetText(agent.DefaultVLLMURL)
		}
		if previous != draft.Provider { // another service: another catalog
			loadGeneration++
			models = nil
			draft.Models, draft.AllModels, draft.ModelOptions, draft.DefaultModels = nil, true, nil, nil
			allModels.set(true)
			listModels()
		}
	}
	typePicker.SetSelected(agent.ProviderName(draft.Provider))
	typePicker.onChanged(agent.ProviderName(draft.Provider))
	listModels()

	title := draft.Name
	if !existing {
		title = "New connection"
	}
	page := container.NewVBox(
		settings.pageTitle(title, "Connections", nil),
		card(
			settingRow("Name", "As it appears in the settings.", sized(340, name)),
			settingRow("Type", "", sized(340, typePicker.row)),
			settingRow("Enabled", "Its models are offered in the composer.", enabled)),
		sectionTitle("Access"),
		container.NewStack(apiFields, harnessFields),
		sectionTitle("Models"),
		card(
			settingRow("Show every model", "Turn off to choose them one by one below.", allModels),
			settingRow("Catalog", "Ask the service, from the server, which models it offers.", load),
			padded(10, 10, 16, 16, search),
			modelRows),
	)
	if existing {
		remove := newRow("Remove", iconTrash, styleOutline, func() {
			ui.confirm("Remove connection?", "Remove “"+draft.Name+"” from the server? Its chats are kept.", "Remove", func() {
				remote.saveConnection(map[string]any{"connection": row.Connection, "remove": true}, func(err error) {
					if err != nil {
						say(err.Error())
						return
					}
					settings.done()
				})
			})
		})
		remove.danger = true
		page.Add(sectionTitle("Remove"))
		page.Add(card(settingRow("Remove this connection", "Existing chats are kept.", remove)))
	}
	page.Add(spacer(0, 16))

	save := newRow("Save connection", nil, stylePrimary, nil)
	save.onTap = func() {
		draft.Name = strings.TrimSpace(name.Text)
		draft.BaseURL = strings.TrimSpace(base.Text)
		if draft.Name == "" {
			say("Enter a connection name.")
			return
		}
		if (draft.Provider == "compatible" || draft.Provider == "vllm") && draft.BaseURL == "" {
			say("Enter the service's API base URL.")
			return
		}
		body := map[string]any{"connection": cloneConnection(draft)}
		if key.Text != "" {
			body["apiKey"] = key.Text
		}
		save.Disable()
		say("Saving on the server…")
		remote.saveConnection(body, func(err error) {
			save.Enable()
			if err != nil {
				say(err.Error())
				return
			}
			settings.done()
		})
	}
	cancel := newRow("Cancel", nil, styleOutline, settings.back)
	settings.push(settingsFrame{content: page, actions: []fyne.CanvasObject{
		container.NewCenter(sized(360, status)),
		cancel,
		save,
	}, leave: func() { loadGeneration++ }})
}
