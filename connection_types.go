package main

// Connections and model choices, shared by the desktop and the headless
// server: what a connection is, and how a chat's settings become a request.

import (
	"context"
	"encoding/json"
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
	return config{
		ConnectionID: conn.ID,
		AuthProfile:  conn.AuthProfile,
		Provider:     conn.Provider,
		BaseURL:      conn.BaseURL,
		Workspace:    workspace,
		Routing:      conn.Routing,
		Parameters:   conn.Parameters,
	}
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
	if cfg.ApprovalMode == "auto" {
		prompt = strings.Replace(prompt, "Shell commands and file edits require user approval; never bypass a refusal.", "Shell commands and file edits go through an automatic review: safe ones run at once, risky ones wait for the user's approval. Never bypass a refusal or try to word an action so that it passes the review.", 1)
	}
	if strings.TrimSpace(cfg.UserInstructions) != "" {
		prompt += "\n\nAdditional instructions from the user:\n" + cfg.UserInstructions
	}
	return prompt + "\nWorkspace: " + cfg.Workspace + "\nShell: " + shellDescription() + "." + todoInstructions(cfg) + contextInstructions(cfg) + browserInstructions(cfg) + terminalInstructions(cfg) + computerInstructions(cfg)
}

type approvalContextKey struct{}

func toolContext(ctx context.Context, cfg config) context.Context {
	ctx = context.WithValue(ctx, computerContextKey{}, cfg.Computer)
	ctx = context.WithValue(ctx, terminalContextKey{}, cfg.Terminal)
	ctx = context.WithValue(ctx, browserContextKey{}, cfg.Browser)
	ctx = context.WithValue(ctx, recallContextKey{}, cfg.Recall)
	ctx = context.WithValue(ctx, todoContextKey{}, cfg.Todo)
	return context.WithValue(ctx, approvalContextKey{}, approvalPolicy{yolo: cfg.ApprovalMode == "yolo"})
}

// approvalFor is the approval mode a run with this configuration uses:
// the chosen one. "auto" is Claude Code's own mode for a Claude Code
// connection, 1loop's reviewer for every other (see auto_review.go).
func approvalFor(mode string, cfg config) string {
	return mode
}

func jsonText(value map[string]any) string {
	if len(value) == 0 {
		return "{}"
	}
	b, _ := json.MarshalIndent(value, "", "  ")
	return string(b)
}
