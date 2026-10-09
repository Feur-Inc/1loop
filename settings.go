package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func note(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.Importance = widget.LowImportance
	return label
}
func jsonText(value map[string]any) string {
	if len(value) == 0 {
		return "{}"
	}
	b, _ := json.MarshalIndent(value, "", "  ")
	return string(b)
}
func cloneConnection(c connection) connection {
	b, _ := json.Marshal(c)
	var copy connection
	_ = json.Unmarshal(b, &copy)
	return copy
}
func jsonEntry(value map[string]any) *widget.Entry {
	entry := widget.NewMultiLineEntry()
	entry.SetMinRowsVisible(4)
	entry.SetText(jsonText(value))
	return entry
}

func (u *desktopUI) settings() {
	body := container.NewStack()
	nav := container.NewVBox()
	var show func(string)
	show = func(page string) {
		body.Objects = nil
		switch page {
		case "General":
			body.Add(u.generalSettings())
		case "Connections":
			body.Add(u.connectionSettings())
		case "Instructions":
			body.Add(u.instructionSettings())
		}
		for _, raw := range nav.Objects {
			row := raw.(*tapRow)
			row.SetSelected(row.text == page)
		}
		body.Refresh()
	}
	for _, page := range []string{"General", "Connections", "Instructions"} {
		page := page
		nav.Add(newRow(page, nil, styleNav, func() { show(page) }))
	}
	sidebar := container.NewGridWrap(fyne.NewSize(160, 540), nav)
	content := container.NewBorder(nil, nil, sidebar, nil, padded(0, 0, 20, 0, body))
	show("Connections")
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(880, 540))
	u.modal("Settings", scroll, 960, nil)
}
func (u *desktopUI) generalSettings() fyne.CanvasObject {
	workspace := widget.NewEntry()
	workspace.SetText(u.cfg.Workspace)
	choose := newIconButton(theme.FolderOpenIcon(), color.NRGBA{}, color.NRGBA{}, func() {
		u.pick(pickRequest{title: "Choose the workspace", start: strings.TrimSpace(workspace.Text), folder: true}, func(paths []string) {
			workspace.SetText(paths[0])
		})
	})
	mode := newPicker(u.window, "Tool permissions", styleField, nil)
	mode.Options = []string{"Approval required", "YOLO mode (automatic approval)"}
	selected := "Approval required"
	if u.approvalMode == "yolo" {
		selected = mode.Options[1]
	}
	mode.SetSelected(selected)
	status := note("")
	save := newRow("Save general settings", nil, stylePrimary, func() {
		root, err := resolveWorkspace(strings.TrimSpace(workspace.Text))
		if err != nil {
			status.SetText(err.Error())
			return
		}
		u.approvalMode = "approval"
		if mode.Selected == mode.Options[1] {
			u.approvalMode = "yolo"
		}
		u.updateApprovalControl()
		if root != u.cfg.Workspace {
			u.selectProject(root)
		}
		u.save()
		status.SetText("General settings saved.")
	})
	return container.NewVBox(textLine("General", 20, colText), spacer(0, 12), field("Workspace", container.NewBorder(nil, nil, nil, choose, workspace)), field("Default tool permissions", mode.row), note("Approval asks before each shell command or file edit. YOLO executes them automatically with your user permissions."), spacer(0, 12), container.NewHBox(save), status)
}
func (u *desktopUI) instructionSettings() fyne.CanvasObject {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(13)
	entry.SetText(u.userInstructions)
	entry.SetPlaceHolder("Describe your preferences: language, tone, coding conventions, level of detail…")
	status := note("")
	save := newRow("Save instructions", nil, stylePrimary, func() {
		u.userInstructions = strings.TrimSpace(entry.Text)
		u.save()
		status.SetText("Instructions saved. They apply to the next request in every chat.")
	})
	return container.NewVBox(textLine("Your instructions", 20, colText), note("Added to 1loop’s existing system prompt. The built-in workspace and tool rules remain in place."), entry, container.NewHBox(save), status)
}
func (u *desktopUI) connectionSettings() fyne.CanvasObject {
	list := container.NewVBox()
	var refresh func()
	refresh = func() {
		list.Objects = nil
		for _, c := range u.connectionsList {
			c := c
			state := "Enabled"
			if !c.Enabled {
				state = "Disabled"
			}
			edit := newRow("Edit", iconRename, styleChip, func() { u.connectionEditor(c, refresh) })
			remove := newRow("", iconTrash, styleWindow, func() {
				u.confirm("Remove connection?", "Remove “"+c.Name+"” from 1loop? Existing chats are kept. ChatGPT sessions will be disconnected.", "Remove", func() {
					finish := func() {
						var kept []connection
						for _, entry := range u.connectionsList {
							if entry.ID != c.ID {
								kept = append(kept, entry)
							}
						}
						u.connectionsList = kept
						if u.connectionsList == nil {
							u.connectionsList = []connection{}
						}
						delete(u.keys, c.ID)
						u.save()
						refresh()
						u.refreshModels()
					}
					if c.Provider != "openai" {
						finish()
						return
					}
					go func() {
						ctx, cancel := context.WithTimeout(u.ctx, 25*time.Second)
						defer cancel()
						warning, err := u.backend.disconnectOpenAI(ctx, c.AuthProfile)
						fyne.Do(func() {
							if err != nil {
								u.notice("Unable to disconnect", err.Error())
								return
							}
							finish()
							if warning != "" {
								u.notice("Account disconnected", warning)
							}
						})
					}()
				})
			})
			title := container.NewVBox(textLine(c.Name, 15, colText), textLine(providerName(c.Provider)+" · "+state, 12, colMuted))
			list.Add(surface(container.NewBorder(nil, nil, nil, container.NewHBox(edit, remove), title), colRaised, colBorderSoft, 10, 10))
		}
		if len(u.connectionsList) == 0 {
			list.Add(note("No connections yet. Add one to get started."))
		}
		list.Refresh()
	}
	add := newRow("Add connection", iconPlus, stylePrimary, func() {
		id := identifier()
		u.connectionEditor(connection{ID: id, AuthProfile: id, Name: "New connection", Provider: "compatible", Enabled: true, AllModels: true}, refresh)
	})
	refresh()
	return container.NewVBox(container.NewBorder(nil, nil, nil, add, textLine("Connections", 20, colText)), note("Connect multiple services and accounts at once. Their selected models appear together in the composer."), list)
}

func (u *desktopUI) connectionEditor(original connection, changed func()) {
	draft := cloneConnection(original)
	name := widget.NewEntry()
	name.SetText(draft.Name)
	base := widget.NewEntry()
	base.SetText(draft.BaseURL)
	key := widget.NewPasswordEntry()
	key.SetText(u.keys[draft.ID])
	key.SetPlaceHolder("API key (kept in memory until 1loop closes)")
	enabled := widget.NewCheck("Enable this connection", nil)
	enabled.SetChecked(draft.Enabled)
	typePicker := newPicker(u.window, "Connection type", styleField, nil)
	typePicker.Options = []string{"Local vLLM", "OpenRouter", "ChatGPT account", "OpenAI-compatible API"}
	ids := map[string]string{"Local vLLM": "vllm", "OpenRouter": "openrouter", "ChatGPT account": "openai", "OpenAI-compatible API": "compatible"}
	apiFields := container.NewVBox(field("API base URL", base), field("API key", key))
	loginLink := widget.NewHyperlink("Open sign-in in your browser", nil)
	loginLink.Hide()
	status := note("")
	account := note("")
	if draft.Provider == "openai" {
		if auth, err := u.backend.getOpenAI(draft.AuthProfile); err == nil {
			auth.mu.Lock()
			account.SetText(auth.Account.Email)
			auth.mu.Unlock()
		}
	}
	var cancelLogin context.CancelFunc
	login := newRow("Continue with ChatGPT", nil, styleOutline, nil)
	login.onTap = func() {
		if cancelLogin != nil {
			cancelLogin()
		}
		ctx, cancel := context.WithTimeout(u.ctx, 5*time.Minute)
		cancelLogin = cancel
		profile := draft.AuthProfile
		login.Disable()
		status.SetText("Preparing sign-in…")
		go func() {
			defer cancel()
			raw, done, err := u.backend.beginOpenAI(ctx, profile)
			fyne.Do(func() {
				if err != nil {
					login.Enable()
					status.SetText(err.Error())
					return
				}
				parsed, _ := url.Parse(raw)
				loginLink.SetURL(parsed)
				loginLink.Show()
				status.SetText("Open the link, authorize 1loop, then return here.")
			})
			if err != nil {
				return
			}
			err = <-done
			fyne.Do(func() {
				login.Enable()
				loginLink.Hide()
				if err != nil {
					status.SetText(err.Error())
					return
				}
				status.SetText("Account connected. Load models to choose which ones to show.")
				auth, e := u.backend.getOpenAI(profile)
				if e == nil {
					auth.mu.Lock()
					email := auth.Account.Email
					auth.mu.Unlock()
					account.SetText(email)
				}
			})
		}()
	}
	oauthFields := container.NewVBox(note("Use your ChatGPT subscription with browser sign-in (OAuth + PKCE)."), account, container.NewHBox(login), loginLink)
	routing := jsonEntry(draft.Routing)
	parameters := jsonEntry(draft.Parameters)
	routingSection := field("Default OpenRouter routing (JSON)", routing)
	typePicker.onChanged = func(label string) {
		previous := draft.Provider
		draft.Provider = ids[label]
		if draft.Provider == "openai" {
			apiFields.Hide()
			oauthFields.Show()
		} else {
			apiFields.Show()
			oauthFields.Hide()
		}
		if draft.Provider == "openrouter" {
			base.SetText("https://openrouter.ai/api/v1")
			base.Disable()
			routingSection.Show()
		} else {
			base.Enable()
			routingSection.Hide()
		}
		if draft.Provider == "vllm" && previous != draft.Provider {
			base.SetText(defaultVLLMURL)
		}
	}
	typePicker.SetSelected(providerName(draft.Provider))
	allModels := widget.NewCheck("Show all models from this connection", nil)
	allModels.SetChecked(draft.AllModels)
	search := widget.NewEntry()
	search.SetPlaceHolder("Search loaded models…")
	var models, filtered []modelChoice
	var modelList *widget.List
	modelList = widget.NewList(func() int { return len(filtered) }, func() fyne.CanvasObject {
		return container.NewBorder(nil, nil, widget.NewCheck("", nil), newRow("Configure", nil, styleChip, nil), newFitLabel("", 13, colText))
	}, func(index widget.ListItemID, obj fyne.CanvasObject) {
		model := filtered[index]
		row := obj.(*fyne.Container)
		label := row.Objects[0].(*fitLabel)
		check := row.Objects[1].(*widget.Check)
		configure := row.Objects[2].(*tapRow)
		label.SetText(model.Name)
		check.OnChanged = nil
		check.SetChecked(draft.includes(model.ID))
		check.OnChanged = func(value bool) {
			if draft.AllModels {
				draft.Models = nil
				for _, m := range models {
					draft.Models = append(draft.Models, m.ID)
				}
				draft.AllModels = false
				allModels.SetChecked(false)
			}
			selected := draft.Models[:0]
			for _, id := range draft.Models {
				if id != model.ID {
					selected = append(selected, id)
				}
			}
			if value {
				selected = append(selected, model.ID)
			}
			draft.Models = selected
			modelList.Refresh()
		}
		configure.onTap = func() { u.modelPreferencesEditor(&draft, model, key.Text) }
	})

	filter := func() {
		filtered = nil
		query := strings.ToLower(search.Text)
		for _, m := range models {
			if strings.Contains(strings.ToLower(m.Name+" "+m.ID), query) {
				filtered = append(filtered, m)
			}
		}
		modelList.Refresh()
	}
	search.OnChanged = func(string) { filter() }
	allModels.OnChanged = func(value bool) { draft.AllModels = value; modelList.Refresh() }
	var loadGeneration int
	load := newRow("Load / refresh models", nil, styleOutline, func() {
		loadGeneration++
		generation := loadGeneration
		snapshot := draft
		snapshot.BaseURL = strings.TrimSpace(base.Text)
		apiKey := key.Text
		status.SetText("Loading model catalog…")
		go func() {
			choices, err := u.backend.connectionModels(u.ctx, snapshot, apiKey)
			fyne.Do(func() {
				if generation != loadGeneration {
					return
				}
				if err != nil {
					status.SetText(err.Error())
					return
				}
				models = choices
				filter()
				status.SetText(fmt.Sprintf("%d models loaded. Check the models to show; Configure sets per-model routing and parameters.", len(models)))
			})
		}()
	})
	previousTypeChange := typePicker.onChanged
	typePicker.onChanged = func(label string) {
		oldProvider := draft.Provider
		previousTypeChange(label)
		if draft.Provider != oldProvider {
			loadGeneration++
			models = nil
			draft.Models = nil
			draft.AllModels = true
			draft.ModelOptions = nil
			allModels.SetChecked(true)
			filter()
		}
	}
	modelsSection := container.NewVBox(textLine("Model visibility", 16, colText), allModels, container.NewHBox(load), search, container.NewGridWrap(fyne.NewSize(600, 220), modelList))
	content := container.NewVBox(field("Connection name", name), field("Type", typePicker.row), enabled, apiFields, oauthFields, modelsSection, routingSection, field("Default request parameters (JSON)", parameters), note("Examples: temperature, top_p, max_tokens, seed, chat_template_kwargs. Model, tools and streaming are managed by 1loop."), status)
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(640, 490))
	var modal *sheet
	save := newRow("Save connection", nil, stylePrimary, func() {
		if strings.TrimSpace(name.Text) == "" {
			status.SetText("Enter a connection name.")
			return
		}
		params, err := requestParameters(parameters.Text)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		var route map[string]any
		if draft.Provider == "openrouter" {
			if err = json.Unmarshal([]byte(routing.Text), &route); err != nil || route == nil {
				status.SetText("Routing must be a JSON object.")
				return
			}
		}
		draft.Name = strings.TrimSpace(name.Text)
		draft.BaseURL = strings.TrimSpace(base.Text)
		draft.Enabled = enabled.Checked
		draft.Routing = route
		draft.Parameters = params
		if draft.Provider != "openai" {
			cfg := draft.config("")
			cfg.APIKey = key.Text
			if _, _, err = endpoint(cfg); err != nil {
				status.SetText(err.Error())
				return
			}
		}
		found := false
		for i, c := range u.connectionsList {
			if c.ID == draft.ID {
				u.connectionsList[i] = cloneConnection(draft)
				found = true
				break
			}
		}
		if !found {
			u.connectionsList = append(u.connectionsList, cloneConnection(draft))
		}
		u.keys[draft.ID] = key.Text
		u.save()
		if cancelLogin != nil {
			cancelLogin()
		}
		modal.Hide()
		changed()
		u.refreshModels()
	})
	cancel := newRow("Cancel", nil, styleOutline, func() { modal.dismiss() })
	modal = u.modal("Connection", scroll, 720, func() {
		loadGeneration++
		if cancelLogin != nil {
			cancelLogin()
		}
	}, cancel, save)
}

func (u *desktopUI) modelPreferencesEditor(c *connection, model modelChoice, key string) {
	preferences := c.ModelOptions[model.ID]
	routing := jsonEntry(preferences.Provider)
	parameters := jsonEntry(preferences.Parameters)
	status := note("")
	reasoningInfo := "Reasoning: not advertised by this model."
	if len(model.Efforts) > 0 {
		labels := make([]string, len(model.Efforts))
		for i, effort := range model.Efforts {
			labels[i] = effortLabel(effort)
		}
		reasoningInfo = "Reasoning: " + strings.Join(labels, ", ") + " · " + model.Source
	}
	content := container.NewVBox(note(model.ID), note(reasoningInfo))
	if c.Provider == "openrouter" {
		upstream := newPicker(u.window, "Automatic provider routing", styleField, nil)
		upstream.Options = []string{"Automatic"}
		upstream.SetSelected("Automatic")
		if order := stringsFrom(preferences.Provider["order"]); len(order) > 0 {
			upstream.Options = append(upstream.Options, order[0])
			upstream.SetSelected(order[0])
		}
		fallbacks := widget.NewCheck("Allow fallback providers", nil)
		value, ok := preferences.Provider["allow_fallbacks"].(bool)
		fallbacks.SetChecked(!ok || value)
		// JSON remains the source of truth for all routing options.
		update := func() {
			var route map[string]any
			if json.Unmarshal([]byte(routing.Text), &route) != nil || route == nil {
				route = map[string]any{}
			}
			route["order"] = []string{}
			if upstream.Selected != "Automatic" {
				route["order"] = []string{upstream.Selected}
			}
			route["allow_fallbacks"] = fallbacks.Checked
			routing.SetText(jsonText(route))
		}
		upstream.onChanged = func(string) { update() }
		fallbacks.OnChanged = func(bool) { update() }
		load := newRow("Fetch providers", nil, styleOutline, func() {
			snapshot := *c
			go func() {
				providers, err := u.backend.upstreamProviders(u.ctx, snapshot, key, model.ID)
				fyne.Do(func() {
					if err != nil {
						status.SetText(err.Error())
						return
					}
					upstream.Options = append([]string{"Automatic"}, providers...)
					status.SetText(fmt.Sprintf("%d providers available", len(providers)))
				})
			}()
		})
		content.Add(field("Upstream provider", upstream.row))
		content.Add(container.NewHBox(load))
		content.Add(fallbacks)
		content.Add(field("Routing overrides (JSON)", routing))
	}
	content.Add(field("Request parameter overrides (JSON)", parameters))
	content.Add(note("Save the connection afterwards to apply these model settings. Overrides replace the matching connection defaults."))
	content.Add(status)
	var modal *sheet
	save := newRow("Save model settings", nil, stylePrimary, func() {
		params, err := requestParameters(parameters.Text)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		var route map[string]any
		if c.Provider == "openrouter" {
			if err = json.Unmarshal([]byte(routing.Text), &route); err != nil || route == nil {
				status.SetText("Routing must be a JSON object.")
				return
			}
		}
		if c.ModelOptions == nil {
			c.ModelOptions = map[string]modelPreferences{}
		}
		c.ModelOptions[model.ID] = modelPreferences{Provider: route, Parameters: params}
		modal.Hide()
	})
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(560, 420))
	modal = u.modal("Model settings", scroll, 640, nil, newRow("Cancel", nil, styleOutline, func() { modal.dismiss() }), save)
}
