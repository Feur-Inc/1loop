package ui

import (
	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/platform"
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
	if _, err := agent.ClaudeExecutable(); err != nil {
		return
	}
	ui.claudeOffered = true
	id := core.Identifier()
	ui.connectionsList = append(ui.connectionsList, agent.Connection{ID: id, AuthProfile: id, Name: "Claude Code", Provider: "claudecode",
		Enabled: true, Models: []string{"opus", "sonnet", "fable", "haiku"}})
}

func (ui *desktopUI) initializeConnections() {
	if len(ui.connectionsList) > 0 || ui.cfg.Model != "" || ui.cfg.BaseURL != "" {
		defer ui.offerClaudeCode()
	}
	if ui.connectionsList == nil && (ui.cfg.Model != "" || ui.cfg.BaseURL != "") {
		id := core.Identifier()
		ui.connectionsList = []agent.Connection{{
			ID:        id,
			Name:      agent.ProviderName(ui.cfg.Provider),
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
				found = core.Identifier()
				ui.connectionsList = append(ui.connectionsList, agent.Connection{
					ID:        found,
					Name:      agent.ProviderName(chat.Config.Provider),
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

func (ui *desktopUI) selectModel(choice agent.ModelChoice) {
	previous := ui.cfg
	platform.Devf("ui", "model %s (%s)", choice.ID, choice.Display())
	cfg := choice.Connection.Config(previous.Workspace)
	cfg.MachineID = previous.MachineID // the project stays on its SSH machine
	cfg.Model = choice.ID
	cfg.ReasoningMode = choice.ReasoningMode
	cfg.Effort = nearestModelEffort(choice, previous.Effort)
	cfg.ServiceTier = agent.TierFor(choice, previous.ServiceTier)
	options := choice.Connection.ModelOptions[choice.ID]
	cfg.Routing = agent.MergeOptions(cfg.Routing, options.Provider)
	cfg.Parameters = agent.MergeOptions(cfg.Parameters, options.Parameters)
	cfg.Session, cfg.SessionMark = previous.Session, previous.SessionMark // the chat keeps its Claude Code session across model changes
	cfg.WebConversation = previous.WebConversation
	cfg.RanWith = previous.RanWith // its next turn knows the model changed
	if cfg.RanWith == nil && previous.Provider != "" && previous.Model != "" {
		identity := agent.IdentityOf(previous)
		cfg.RanWith = &identity
	}

	ui.cfg = cfg
	display := choice.Display()
	if runes := []rune(display); len(runes) > 32 {
		display = string(runes[:32]) + "…"
	}
	ui.model.row.SetText(display)
	if ui.current != nil {
		ui.current.Config = cfg
	}
	ui.model.row.SetTrailing(modelTrailing(choice, cfg))
	ui.updateApprovalControl() // Auto mode exists only with Claude Code
	if ui.model.relayout != nil {
		ui.model.relayout()
	}
	ui.updateHeader()
	if cfg.Provider == "chatgptweb" {
		ui.ensureWebTab()
	} else if ui.releaseWebTabs() {
		ui.panel.render()
	}
	ui.save()
	ui.remote.settingsChanged()
	ui.remote.metaChanged(ui.current)
}

// showChatModel puts the open chat's model in the picker, from the catalog
// already loaded; it is fetched again only when the model is not in it.
func (ui *desktopUI) showChatModel() {
	if ui.server != nil { // the server names it (remote_desktop.go)
		return
	}
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
