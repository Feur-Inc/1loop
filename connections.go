package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type modelPreferences struct {
	Provider   map[string]any `json:"provider,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type connection struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Provider     string                      `json:"provider"`
	BaseURL      string                      `json:"baseURL"`
	AuthProfile  string                      `json:"authProfile,omitempty"`
	Enabled      bool                        `json:"enabled"`
	AllModels    bool                        `json:"allModels"`
	Models       []string                    `json:"models,omitempty"`
	Routing      map[string]any              `json:"routing,omitempty"`
	Parameters   map[string]any              `json:"parameters,omitempty"`
	ModelOptions map[string]modelPreferences `json:"modelOptions,omitempty"`
}

type modelChoice struct {
	Connection                           connection
	ID, Name                             string
	Efforts                              []string
	DefaultEffort, ReasoningMode, Source string
}

func (conn connection) config(workspace string) config {
	return config{ConnectionID: conn.ID, AuthProfile: conn.AuthProfile, Provider: conn.Provider, BaseURL: conn.BaseURL, Workspace: workspace, Routing: conn.Routing, Parameters: conn.Parameters}
}
func (conn connection) includes(id string) bool {
	if conn.AllModels {
		return true
	}
	for _, selected := range conn.Models {
		if id == selected {
			return true
		}
	}
	return false
}
func mergeOptions(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
func requestParameters(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("Invalid JSON object: %w", err)
	}
	if params == nil {
		return nil, fmt.Errorf("Expected a JSON object")
	}
	for _, key := range []string{"model", "messages", "input", "instructions", "tools", "tool_choice", "stream", "store", "reasoning", "reasoning_effort", "provider"} {
		if _, ok := params[key]; ok {
			return nil, fmt.Errorf("%s is managed by 1loop; use its dedicated setting", key)
		}
	}
	return params, nil
}

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
		ui.connectionsList = []connection{{ID: id, Name: providerName(ui.cfg.Provider), Provider: ui.cfg.Provider, BaseURL: ui.cfg.BaseURL, Enabled: true, AllModels: true}}
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
				ui.connectionsList = append(ui.connectionsList, connection{ID: found, Name: providerName(chat.Config.Provider), Provider: chat.Config.Provider, BaseURL: chat.Config.BaseURL, Enabled: true, AllModels: true})
			}
			chat.Config.ConnectionID = found
		}
	}
	if ui.approvalMode == "" {
		ui.approvalMode = "approval"
	}
}
func providerName(id string) string {
	switch id {
	case "vllm":
		return "Local vLLM"
	case "openrouter":
		return "OpenRouter"
	case "openai":
		return "ChatGPT account"
	case "compatible":
		return "OpenAI-compatible API"
	case "claudecode":
		return "Claude Code"
	}
	return id
}

// display is the model's name as the composer shows it. A Claude Code model
// says so: there the work runs in Claude Code's harness, not in 1loop's.
func (choice modelChoice) display() string {
	if choice.Connection.Provider == "claudecode" {
		return choice.Name + " · Claude Code"
	}
	return choice.Name
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

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func systemInstructions(cfg config) string {
	prompt := instructions
	if cfg.ApprovalMode == "yolo" {
		prompt = strings.Replace(prompt, "Shell commands and file edits require user approval; never bypass a refusal.", "The user enabled YOLO mode: shell commands (deletions included) and edits run without asking. Stay inside the workspace.", 1)
	}
	if strings.TrimSpace(cfg.UserInstructions) != "" {
		prompt += "\n\nAdditional instructions from the user:\n" + cfg.UserInstructions
	}
	return prompt + "\nWorkspace: " + cfg.Workspace + todoInstructions(cfg) + contextInstructions(cfg) + browserInstructions(cfg) + terminalInstructions(cfg) + computerInstructions(cfg)
}

type approvalContextKey struct{}

func toolContext(ctx context.Context, cfg config) context.Context {
	ctx = context.WithValue(ctx, computerContextKey{}, cfg.Computer)
	ctx = context.WithValue(ctx, terminalContextKey{}, cfg.Terminal)
	ctx = context.WithValue(ctx, browserContextKey{}, cfg.Browser)
	ctx = context.WithValue(ctx, recallContextKey{}, cfg.Recall)
	ctx = context.WithValue(ctx, todoContextKey{}, cfg.Todo)
	return context.WithValue(ctx, approvalContextKey{}, cfg.ApprovalMode == "yolo")
}
