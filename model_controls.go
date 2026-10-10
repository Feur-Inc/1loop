package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

func (u *desktopUI) selectedChoice() (modelChoice, bool) {
	for _, choice := range u.choices {
		if choice.ID == u.cfg.Model && choice.Connection.ID == u.cfg.ConnectionID {
			return choice, true
		}
	}
	return modelChoice{}, false
}

// modelMenu opens the model popover: the model on top (a click lists the
// others), then its reasoning effort when the model has levels to choose.
func (u *desktopUI) modelMenu() {
	if len(u.model.Options) == 0 && !u.modelsLoading {
		u.settings() // nothing to choose from yet: connections live there
		return
	}
	choice, ok := u.selectedChoice()
	if !ok || len(choice.Efforts) == 0 {
		u.model.toggle()
		return
	}
	if u.model.open != nil {
		u.model.open.dismiss()
		return
	}
	var menu *sheet
	current := 0
	for i, value := range choice.Efforts {
		if value == u.cfg.Effort {
			current = i
		}
	}
	// Codex layout: the level as the title, the model under it (a click
	// lists the others), then the track.
	level := textLine(effortLabel(choice.Efforts[current]), 15, colAmber)
	level.TextStyle = fyne.TextStyle{Bold: true}
	name := newRow(choice.Name, iconChevronRight, styleChip, func() { menu.Hide(); u.model.show() })
	name.iconAfter = true
	name.size = 12.5
	slider := newStepSlider(len(choice.Efforts), current, func(index int) {
		u.cfg.Effort = choice.Efforts[index]
		if u.current != nil {
			u.current.Config.Effort = u.cfg.Effort
		}
		level.Text = effortLabel(u.cfg.Effort)
		level.Refresh()
		u.model.row.SetTrailing(level.Text)
		if u.model.relayout != nil {
			u.model.relayout()
		}
		u.save()
		u.remote.settingsChanged()
		u.remote.metaChanged(u.current)
	})
	var control fyne.CanvasObject = slider
	if len(choice.Efforts) == 1 {
		control = note("Reasoning is required by this model.")
	}
	content := padded(10, 10, 10, 10, container.NewVBox(container.NewCenter(level), container.NewCenter(name), spacer(0, 6), control))
	menu = newSheet(u.window.Canvas(), content, 300)
	menu.menu = true
	menu.anchor = u.model.row
	menu.onKey = slider.key
	menu.onClosed = func() {
		if u.model.open == menu {
			u.model.open = nil
			u.model.setOpen(false)
		}
	}
	u.model.open = menu
	u.model.setOpen(true)
	menu.Show()
	u.window.Canvas().Focus(menu)
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
