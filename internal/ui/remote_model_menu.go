// SPDX-License-Identifier: MPL-2.0

package ui

// The model picker for a server (remote_desktop.go): the same three
// columns as this computer's (model_controls.go), account, model by
// family, reasoning, filled from what the server lists, with the shown
// account's limits below them and the model's speed button.

import (
	"context"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

func (remote *remoteDesktop) modelMenu() {
	ui := remote.parent
	if ui.model.open != nil {
		ui.model.open.dismiss()
		return
	}
	current, effort, tier := remote.model, remote.effort, remote.tier
	if remote.current != "" {
		current, effort, tier = remote.meta.Model, remote.meta.Effort, remote.meta.Tier
	}
	if len(remote.models) == 0 && len(remote.accounts) == 0 {
		showMenu(ui.window, ui.model.row, []menuAction{{label: "The server has no model yet"}})
		return
	}
	accounts := slices.Clone(remote.accounts)
	catalog := map[string][]remoteapi.Model{}
	for _, model := range remote.models {
		catalog[model.Account] = append(catalog[model.Account], model)
		if !slices.ContainsFunc(accounts, func(account remoteapi.Account) bool { return account.ID == model.Account }) {
			accounts = append(accounts, remoteapi.Account{ID: model.Account, Name: model.Detail})
		}
	}
	selected := func() (remoteapi.Model, bool) {
		for _, model := range remote.models {
			if model.Label == current {
				return model, true
			}
		}
		return remoteapi.Model{}, false
	}
	lists := [3]*fyne.Container{}
	scrolls := [2]*kineticScroll{}
	columns := make([]fyne.CanvasObject, 3)
	speed := container.NewStack() // on the REASONING caption's line, as on the desktop
	for i, title := range []string{"ACCOUNT", "MODEL", "REASONING"} {
		lists[i] = container.New(layout.NewCustomPaddedVBoxLayout(0))
		var body fyne.CanvasObject = lists[i]
		if i < 2 {
			scrolls[i] = newKineticVScroll(lists[i])
			scrolls[i].SetMinSize(fyne.NewSize(0, 276-modelLimitsStripHeight))
			body = scrolls[i]
		}
		caption := textLine(title, 11, colFaint)
		caption.TextStyle = fyne.TextStyle{Bold: true}
		var header fyne.CanvasObject = caption
		if i == 2 {
			header = container.New(captionActionLayout{}, caption, speed)
		}
		column := padded(7, 5, 3, 3, container.NewBorder(padded(0, 8, 9, 9, header), nil, nil, nil, body))
		if i > 0 {
			line := canvas.NewRectangle(colBorderSoft)
			line.SetMinSize(fyne.NewSize(1, 0))
			columns[i] = container.NewBorder(nil, nil, line, nil, column)
		} else {
			columns[i] = column
		}
	}
	strip := container.NewStack()
	menu := newSheet(ui.window.Canvas(), container.New(modelColumnsLayout{}, append(columns, strip)...), 674)
	menu.menu, menu.columns, menu.anchor = true, true, ui.model.row
	groups := [3][]*tapRow{}
	shown := ""
	values := map[string]agent.AccountLimits{}
	refreshStrip := func() {
		for _, account := range accounts {
			if account.ID == shown {
				limits, ok := values[account.ID]
				if !ok {
					limits = agent.AccountLimits{Note: "Loading limits…"}
				}
				conn := agent.Connection{ID: account.ID, Name: account.Name, Provider: account.Provider}
				strip.Objects = []fyne.CanvasObject{modelLimitsStrip(conn, limits, account.Problem)}
			}
		}
		strip.Refresh()
	}
	column, cursor := 0, 0
	var reasoning *stepSlider
	row := func(title, detail string, icon fyne.Resource, run func()) *tapRow {
		r := newRow(title, icon, styleModel, run)
		r.detail, r.truncate, r.badge = detail, true, blankIcon
		return r
	}
	mark := func(r *tapRow, on bool) {
		r.selected, r.badge = on, blankIcon
		if on {
			r.badge = iconCheck
		}
		r.Refresh()
	}
	var update func(string)
	choose := func(model remoteapi.Model) {
		if menu.closing {
			return
		}
		current = model.Label
		if !slices.ContainsFunc(model.Efforts, func(e remoteapi.Effort) bool { return e.Value == effort }) {
			effort = model.DefaultEffort
		}
		remote.action("/api/model", map[string]string{"chat": remote.current, "label": model.Label})
		update(model.Account)
	}
	for _, account := range accounts {
		account := account
		detail := account.Kind
		if account.Problem != "" {
			detail = account.Problem
		}
		r := row(account.Name, detail, modelAccountBadge(account.Provider), func() {
			models := catalog[account.ID]
			if len(models) == 0 {
				update(account.ID)
				return
			}
			pick := models[0]
			for _, model := range models {
				if model.Label == current {
					pick = model
				}
			}
			choose(pick)
		})
		groups[0] = append(groups[0], r)
		lists[0].Add(container.New(modelAccountLayout{}, r))
	}
	update = func(accountID string) {
		if shown != accountID {
			shown = accountID
			groups[1] = nil
			lists[1].RemoveAll()
			models := catalog[shown]
			// Sorted by family, newest first; a caption per family once
			// the account has more than one.
			grouped := slices.ContainsFunc(models, func(model remoteapi.Model) bool { return model.Family != models[0].Family })
			family := ""
			for _, model := range models {
				model := model
				if grouped && model.Family != family {
					family = model.Family
					lists[1].Add(modelFamilyCaption(family, len(groups[1]) == 0))
				}
				name := strings.TrimSuffix(model.Name, " · "+model.Detail)
				r := row(name, model.ID, nil, func() { choose(model) })
				groups[1] = append(groups[1], r)
				lists[1].Add(r)
			}
			scrolls[1].ScrollToTop()
			if len(groups[1]) == 0 {
				lists[1].Add(padded(7, 7, 9, 9, note("No models on this account.")))
			}
		}
		for i, r := range groups[0] {
			mark(r, accounts[i].ID == shown)
		}
		for i, r := range groups[1] {
			mark(r, catalog[shown][i].Label == current)
		}
		groups[2] = nil
		lists[2].RemoveAll()
		model, ok := selected()
		efforts := model.Efforts
		disabled := !ok || model.Account != shown || len(efforts) == 0
		if len(efforts) == 0 {
			efforts = []remoteapi.Effort{{Label: "None"}}
		}
		index := max(0, slices.IndexFunc(efforts, func(e remoteapi.Effort) bool { return e.Value == effort }))
		title := textLine(efforts[index].Label, 18, colAmber)
		title.Alignment, title.TextStyle = fyne.TextAlignCenter, fyne.TextStyle{Bold: true}
		about := textLine(fitText(efforts[index].About, 12, fyne.TextStyle{}, 180), 12, colMuted)
		about.Alignment = fyne.TextAlignCenter
		if disabled {
			title.Text, about.Text = "None", "No reasoning"
			title.Color, about.Color = colFaint, colFaint
		}
		reasoning = newStepSlider(len(efforts), index, func(index int) {
			if menu.closing {
				return
			}
			effort = efforts[index].Value
			remote.action("/api/model", map[string]string{"chat": remote.current, "effort": effort})
			title.Text, about.Text = efforts[index].Label, fitText(efforts[index].About, 12, fyne.TextStyle{}, 180)
			title.Refresh()
			about.Refresh()
			for i, label := range groups[2] {
				label.accent = i == index
				label.Refresh()
			}
		})
		reasoning.disabled = disabled
		controls := []fyne.CanvasObject{reasoning}
		for i, entry := range efforts {
			i := i
			r := newRow(entry.Label, nil, styleChip, func() { reasoning.set(i) })
			r.size, r.truncate, r.accent = 12, true, !disabled && i == index
			if disabled {
				r.Disable()
			}
			groups[2] = append(groups[2], r)
			controls = append(controls, r)
		}
		lists[2].Add(padded(10, 3, 6, 6, container.New(layout.NewCustomPaddedVBoxLayout(3), title, about)))
		lists[2].Add(padded(14, 9, 5, 5, container.New(modelReasoningLayout{}, controls...)))
		speed.Objects = nil
		if ok && model.Account == shown && len(model.ServiceTiers) > 0 {
			speed.Objects = []fyne.CanvasObject{remote.speedToggle(model, &tier)}
		}
		speed.Refresh()
		refreshStrip()
		menu.items, menu.active = groups[column], -1
	}
	menu.onKey = func(e *fyne.KeyEvent) bool {
		if column == 2 && reasoning.key(e) {
			return true
		}
		switch e.Name {
		case fyne.KeyLeft:
			column, cursor = (column+2)%3, 0
		case fyne.KeyRight, fyne.KeyTab:
			column, cursor = (column+1)%3, 0
		case fyne.KeyDown:
			cursor++
		case fyne.KeyUp:
			cursor--
		case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
			if n := len(groups[column]); column < 2 && n > 0 {
				groups[column][(cursor%n+n)%n].Tapped(nil)
			}
			return true
		default:
			return false
		}
		menu.items, menu.active = groups[column], -1
		if n := len(groups[column]); column < 2 && n > 0 {
			cursor = (cursor%n + n) % n
			menu.highlight(cursor)
		}
		return true
	}
	ctx, cancel := context.WithCancel(remote.ctx)
	menu.onClosed = func() {
		cancel()
		if ui.model.open == menu {
			ui.model.open = nil
			ui.model.setOpen(false)
		}
	}
	initial := ""
	if model, ok := selected(); ok {
		initial = model.Account
	} else if len(accounts) > 0 {
		initial = accounts[0].ID
	}
	update(initial)
	ui.model.open = menu
	ui.model.setOpen(true)
	menu.Show()
	ui.window.Canvas().Focus(menu)
	go remote.watchLimits(ctx, func(limits []remoteapi.Limits) {
		if menu.closing {
			return
		}
		for _, account := range limits {
			value := agent.AccountLimits{Note: account.Note, Checked: time.Now()}
			for _, window := range account.Windows {
				reset := time.Time{}
				if window.Reset > 0 {
					reset = time.UnixMilli(window.Reset)
				}
				value.Windows = append(value.Windows, agent.NewAccountLimitWindow(window.Label, window.Left, reset))
			}
			values[account.Account] = value
		}
		refreshStrip()
	})
}

// watchLimits reads the server's account limits while the picker is open:
// at once, then every minute, as the desktop does.
func (remote *remoteDesktop) watchLimits(ctx context.Context, show func([]remoteapi.Limits)) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		var limits []remoteapi.Limits
		if remote.client.json(ctx, "GET", "/api/limits", nil, &limits) == nil {
			fyne.Do(func() {
				if ctx.Err() == nil {
					show(limits)
				}
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// speedToggle is the desktop's bolt button (model_controls.go) for a
// server's model: each tap asks the server for the next speed.
func (remote *remoteDesktop) speedToggle(model remoteapi.Model, tier *string) fyne.CanvasObject {
	choice := agent.ModelChoice{ServiceTiers: model.ServiceTiers, DefaultServiceTier: model.DefaultServiceTier}
	standard := ""
	if choice.DefaultServiceTier != "" {
		standard = agent.StandardTier
	}
	ids, names := []string{standard}, []string{"Standard"}
	for _, t := range choice.ServiceTiers {
		ids, names = append(ids, t.ID), append(names, t.Name)
	}
	current := agent.TierFor(choice, *tier)
	if current == "" {
		current = choice.DefaultServiceTier
	}
	index := max(0, slices.Index(ids, current))
	var row *tapRow
	row = newRow(names[index], iconBolt, styleChip, func() {
		index = (index + 1) % len(ids)
		*tier = ids[index]
		remote.action("/api/model", map[string]any{"chat": remote.current, "tier": ids[index]})
		row.text, row.accent = names[index], index > 0
		row.Refresh()
	})
	row.size, row.accent = 12, index > 0
	return row
}
