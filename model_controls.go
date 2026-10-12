package main

import (
	"context"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
)

func (u *desktopUI) selectedChoice() (modelChoice, bool) {
	for _, choice := range u.choices {
		if choice.ID == u.cfg.Model && choice.Connection.ID == u.cfg.ConnectionID {
			return choice, true
		}
	}
	return modelChoice{}, false
}

// modelMenu follows the HTML's “1 · Colonnes”; selecting an account
// updates its models while all three columns stay available.
func (u *desktopUI) modelMenu() {
	if u.modelsLoading {
		return
	}
	if u.model.open != nil {
		u.model.open.dismiss()
		return
	}
	var accounts []connection
	catalog := map[string][]string{}
	for _, label := range u.model.Options {
		choice := u.choices[label]
		catalog[choice.Connection.ID] = append(catalog[choice.Connection.ID], label)
	}
	for _, account := range u.connectionsList {
		if account.Enabled && (len(catalog[account.ID]) > 0 || u.modelUnavailable[account.ID]) {
			accounts = append(accounts, account)
		}
	}
	if len(accounts) == 0 {
		u.settings()
		return
	}
	lists := [3]*fyne.Container{}
	scrolls := [3]*kineticScroll{}
	columns := make([]fyne.CanvasObject, 3)
	for i, title := range []string{"ACCOUNT · RESTANT", "MODEL", "REASONING"} {
		lists[i] = container.New(layout.NewCustomPaddedVBoxLayout(0))
		var body fyne.CanvasObject = lists[i]
		if i < 2 {
			scrolls[i] = newKineticVScroll(lists[i])
			scrolls[i].SetMinSize(fyne.NewSize(0, 276))
			body = scrolls[i]
		}
		caption := textLine(title, 11, colFaint)
		caption.TextStyle = fyne.TextStyle{Bold: true}
		column := padded(7, 5, 3, 3, container.NewBorder(padded(0, 8, 9, 9, caption), nil, nil, nil, body))
		if i > 0 {
			line := canvas.NewRectangle(colBorderSoft)
			line.SetMinSize(fyne.NewSize(1, 0))
			columns[i] = container.NewBorder(nil, nil, line, nil, column)
		} else {
			columns[i] = column
		}
	}
	menu := newSheet(u.window.Canvas(), container.New(modelColumnsLayout{}, columns...), 674)
	menu.tips = newTipLayer()
	ctx, cancel := context.WithCancel(u.ctx)
	menu.menu, menu.columns, menu.anchor = true, true, u.model.row
	shown := ""
	groups := [3][]*tapRow{}
	column, cursor := 0, 0
	var update func(string)
	var reasoning *stepSlider
	row := func(title, detail string, icon fyne.Resource, run func()) *tapRow {
		r := newRow(title, icon, styleModel, run)
		r.detail, r.truncate, r.badge = detail, true, blankIcon
		return r
	}
	mark := func(r *tapRow, selected bool) {
		r.selected = selected
		r.badge = blankIcon
		if selected {
			r.badge = iconCheck
		}
		r.Refresh()
	}
	choose := func(label string) {
		if menu.closing {
			return
		}
		u.model.SetSelected(label)
		update(u.cfg.ConnectionID)
	}
	for _, account := range accounts {
		account := account
		r := row(account.Name, providerName(account.Provider), modelAccountBadge(account.Provider), func() {
			labels := catalog[account.ID]
			selected := labels[0]
			for _, label := range labels {
				if u.choices[label].ID == u.cfg.Model {
					selected = label
					break
				}
			}
			choose(selected)
		})
		if u.modelUnavailable[account.ID] {
			r.detail = "Injoignable"
			r.Disable()
		}
		groups[0] = append(groups[0], r)
		lists[0].Add(r)
	}
	update = func(accountID string) {
		if shown != accountID {
			shown = accountID
			groups[1] = nil
			lists[1].RemoveAll()
			for _, label := range catalog[shown] {
				label := label
				choice := u.choices[label]
				r := row(choice.Name, choice.ID, nil, func() { choose(label) })
				groups[1] = append(groups[1], r)
				lists[1].Add(r)
			}
			scrolls[1].ScrollToTop()
		}
		for i, r := range groups[0] {
			mark(r, !r.disabled && accounts[i].ID == u.cfg.ConnectionID)
		}
		for i, r := range groups[1] {
			mark(r, u.choices[catalog[shown][i]].ID == u.cfg.Model)
		}
		groups[2] = nil
		lists[2].RemoveAll()
		choice, ok := u.selectedChoice()
		disabled := !ok || len(choice.Efforts) == 0
		efforts := choice.Efforts
		if len(efforts) == 0 {
			efforts = effortOrder[2:]
		}
		index := slices.Index(efforts, u.cfg.Effort)
		if disabled || index < 0 {
			index = 0
		}
		title := textLine(effortLabel(efforts[index]), 18, colAmber)
		title.Alignment, title.TextStyle = fyne.TextAlignCenter, fyne.TextStyle{Bold: true}
		about := textLine(modelEffortAbout(efforts[index]), 12, colMuted)
		about.Alignment = fyne.TextAlignCenter
		if disabled {
			title.Text, about.Text = "Aucun", "Sans raisonnement"
			title.Color, about.Color = colFaint, colFaint
		}
		reasoning = newStepSlider(len(efforts), index, func(index int) {
			if menu.closing {
				return
			}
			u.cfg.Effort = efforts[index]
			u.selectModel(choice)
			title.Text, about.Text = effortLabel(u.cfg.Effort), modelEffortAbout(u.cfg.Effort)
			title.Refresh()
			about.Refresh()
			for i, label := range groups[2] {
				label.accent = i == index
				label.Refresh()
			}
		})
		reasoning.disabled = disabled
		controls := []fyne.CanvasObject{reasoning}
		for i, effort := range efforts {
			i := i
			r := newRow(effortLabel(effort), nil, styleChip, func() { reasoning.set(i) })
			r.size, r.truncate, r.accent = 12, true, !disabled && i == index
			if disabled {
				r.Disable()
			}
			groups[2] = append(groups[2], r)
			controls = append(controls, r)
		}
		lists[2].Add(padded(10, 3, 6, 6, container.New(layout.NewCustomPaddedVBoxLayout(3), title, about)))
		lists[2].Add(padded(14, 9, 5, 5, container.New(modelReasoningLayout{}, controls...)))
		menu.items = groups[column]
		menu.active = -1
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
			if column == 2 {
				return true
			}
			if n := len(groups[column]); n > 0 {
				groups[column][(cursor%n+n)%n].Tapped(nil)
			}
			return true
		default:
			return false
		}
		if column == 2 {
			menu.items, menu.active = groups[2], -1
			return true
		}
		if n := len(groups[column]); n > 0 {
			cursor = (cursor%n + n) % n
			menu.items = groups[column]
			menu.highlight(cursor)
			item, scroll := groups[column][cursor], scrolls[column]
			if y := item.Position().Y; y < scroll.Offset.Y {
				scroll.ScrollToOffset(fyne.NewPos(0, y))
			} else if bottom := y + item.MinSize().Height; bottom > scroll.Offset.Y+scroll.Size().Height {
				scroll.ScrollToOffset(fyne.NewPos(0, bottom-scroll.Size().Height))
			}
		}
		return true
	}
	menu.onClosed = func() {
		cancel()
		menu.tips.hide()
		if u.tips != nil {
			u.tips.hide()
		}
		if u.model.open == menu {
			u.model.open = nil
			u.model.setOpen(false)
		}
	}
	update(u.cfg.ConnectionID)
	u.model.open = menu
	u.model.setOpen(true)
	menu.Show()
	u.window.Canvas().Focus(menu)
	for i, account := range accounts {
		u.loadAccountLimits(ctx, menu, account, groups[0][i])
	}
}

type modelColumnsLayout struct{}

func (modelColumnsLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(420, 320) }
func (modelColumnsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for i, weight := range []float32{240, 220, 214} {
		width := size.Width * weight / 674
		objects[i].Move(fyne.NewPos(x, 0))
		objects[i].Resize(fyne.NewSize(width, size.Height))
		x += width
	}
}

type modelReasoningLayout struct{}

func (modelReasoningLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(130, 202)
}
func (modelReasoningLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	slider := objects[0].(*stepSlider)
	width := fyne.Min(90, fyne.Max(0, size.Width-sliderWidth-18))
	x := (size.Width - sliderWidth - 18 - width) / 2
	y := (size.Height - 202) / 2
	slider.Move(fyne.NewPos(x, y))
	slider.Resize(fyne.NewSize(sliderWidth, 202))
	height := fyne.Min(26, slider.span()/fyne.Max(1, float32(slider.steps-1)))
	for i, label := range objects[1:] {
		label.Move(fyne.NewPos(x+sliderWidth+18, y+slider.stopY(float32(i))-height/2))
		label.Resize(fyne.NewSize(width, height))
	}
}

func modelAccountBadge(provider string) fyne.Resource {
	switch provider {
	case "openai", "claudecode", "openrouter", "vllm":
		data, _ := artwork.ReadFile("assets/provider-" + provider + ".svg")
		return fyne.NewStaticResource("1loop-account-"+provider+".svg", data)
	default:
		return iconPlug
	}
}

func modelEffortAbout(effort string) string {
	return map[string]string{"none": "No reasoning", "enabled": "Reasoning enabled", "minimal": "Answers at once", "low": "Quick, light checks", "medium": "Balanced", "high": "Thinks it through", "xhigh": "Slow and thorough", "max": "As deep as it goes"}[effort]
}

func nearestModelEffort(choice modelChoice, wanted string) string {
	if len(choice.Efforts) == 0 {
		return ""
	}
	if wanted == "" {
		wanted = choice.DefaultEffort
		if wanted == "" {
			wanted = "medium"
		}
	}
	at := slices.Index(effortOrder, wanted)
	best, distance := choice.DefaultEffort, len(effortOrder)+1
	for _, effort := range choice.Efforts {
		delta := slices.Index(effortOrder, effort) - at
		if delta < 0 {
			delta = -delta
		}
		if delta < distance {
			best, distance = effort, delta
		}
	}
	return best
}

// approvalFor is the approval mode a run with this configuration uses.
// "auto" is Claude Code's own permission mode (it runs what it judges safe
// and blocks or asks for the rest); 1loop's harness has no such judge, so
// with any other model it falls back to asking.
func approvalFor(mode string, cfg config) string {
	if mode == "auto" && cfg.Provider != "claudecode" {
		return "approval"
	}
	return mode
}

func (u *desktopUI) updateApprovalControl() {
	if u.approvalControl == nil {
		return
	}
	mode := approvalFor(u.approvalMode, u.cfg)
	u.approvalControl.icon = iconShield
	u.approvalControl.danger = mode == "yolo"
	u.approvalControl.SetText("Approval required")
	switch mode {
	case "yolo":
		u.approvalControl.icon = iconBolt
		u.approvalControl.SetText("YOLO mode")
	case "auto":
		u.approvalControl.icon = iconShieldCheck
		u.approvalControl.SetText("Auto mode")
	}
	if u.controls != nil {
		u.controls.Refresh()
	}
	u.remote.settingsChanged()
}

func (u *desktopUI) approvalMenu() {
	set := func(mode string) func() {
		return func() {
			devf("ui", "approval mode %s", mode)
			u.approvalMode = mode
			u.updateApprovalControl()
			u.save()
		}
	}
	mode := approvalFor(u.approvalMode, u.cfg)
	actions := []menuAction{{label: "Approval required", detail: "Ask before each command and edit", icon: iconShield,
		checked: mode == "approval", run: set("approval")}}
	if u.cfg.Provider == "claudecode" { // only Claude Code's harness has this mode
		actions = append(actions, menuAction{label: "Auto mode", detail: "Claude Code runs what it judges safe", icon: iconShieldCheck,
			checked: mode == "auto", run: set("auto")})
	}
	actions = append(actions, menuAction{label: "YOLO mode", detail: "Run commands and edits automatically", icon: iconBolt,
		checked: mode == "yolo", run: set("yolo")})
	showMenu(u.window, u.approvalControl, actions)
}
