//go:build !server

package main

import (
	"os/exec"
)

// offerClaudeCode adds a Claude Code connection the first time 1loop finds
// Claude Code installed, with its current models; the user can edit or
// remove it like any other connection, and it is never added again.
func (ui *desktopUI) offerClaudeCode() {
	if ui.claudeOffered {
		return
	}
	for _, conn := range ui.connectionsList {
		if conn.Provider == "claudecode" {
			ui.claudeOffered = true
			return
		}
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return
	}
	ui.claudeOffered = true
	id := identifier()
	ui.connectionsList = append(ui.connectionsList, connection{ID: id, AuthProfile: id, Name: "Claude Code", Provider: "claudecode",
		Enabled: true, Models: []string{"opus", "sonnet", "fable", "haiku"}})
}

func (ui *desktopUI) initializeConnections() {
	defer ui.offerClaudeCode()
	if ui.connectionsList == nil {
		id := identifier()
		ui.connectionsList = []connection{{
			ID:        id,
			Name:      providerName(ui.cfg.Provider),
			Provider:  ui.cfg.Provider,
			BaseURL:   ui.cfg.BaseURL,
			Enabled:   true,
			AllModels: true,
		}}
		ui.cfg.ConnectionID = id
		for _, chat := range ui.chats {
			found := ""
			for _, conn := range ui.connectionsList {
				if conn.Provider == chat.Config.Provider && conn.BaseURL == chat.Config.BaseURL {
					found = conn.ID
					break
				}
			}
			if found == "" {
				found = identifier()
				ui.connectionsList = append(ui.connectionsList, connection{
					ID:        found,
					Name:      providerName(chat.Config.Provider),
					Provider:  chat.Config.Provider,
					BaseURL:   chat.Config.BaseURL,
					Enabled:   true,
					AllModels: true,
				})
			}
			chat.Config.ConnectionID = found
		}
	}
	if ui.approvalMode == "" {
		ui.approvalMode = "approval"
	}
}

func (ui *desktopUI) selectModel(choice modelChoice) {
	previous := ui.cfg
	devf("ui", "model %s (%s)", choice.ID, choice.display())
	cfg := choice.Connection.config(previous.Workspace)
	cfg.Model = choice.ID
	cfg.ReasoningMode = choice.ReasoningMode
	cfg.Effort = nearestModelEffort(choice, previous.Effort)
	options := choice.Connection.ModelOptions[choice.ID]
	cfg.Routing = mergeOptions(cfg.Routing, options.Provider)
	cfg.Parameters = mergeOptions(cfg.Parameters, options.Parameters)
	cfg.Session = previous.Session // the chat keeps its Claude Code session across model changes
	ui.cfg = cfg
	display := choice.display()
	if runes := []rune(display); len(runes) > 32 {
		display = string(runes[:32]) + "…"
	}
	ui.model.row.SetText(display)
	if ui.current != nil {
		ui.current.Config = cfg
	}
	if len(choice.Efforts) > 0 {
		ui.model.row.SetTrailing(effortLabel(cfg.Effort))
	} else {
		ui.model.row.SetTrailing("")
	}
	ui.updateApprovalControl() // Auto mode exists only with Claude Code
	if ui.model.relayout != nil {
		ui.model.relayout()
	}
	ui.updateHeader()
	ui.save()
	ui.remote.settingsChanged()
	ui.remote.metaChanged(ui.current)
}

// showChatModel puts the open chat's model in the picker, from the catalog
// already loaded; it is fetched again only when the model is not in it.
func (ui *desktopUI) showChatModel() {
	for label, choice := range ui.choices {
		if choice.Connection.ID == ui.cfg.ConnectionID && choice.ID == ui.cfg.Model {
			ui.model.Selected = label
			ui.selectModel(choice)
			return
		}
	}
	if !ui.modelsLoading {
		ui.refreshModels()
	}
}
