// SPDX-License-Identifier: MPL-2.0

package agent

// Assistant tools: tools a host adds for one agent, with their
// instructions, and runs itself. Loopy, the personal agent of 1loop's
// Loopy tab, remembers, reacts to messages, shows cards and hands tasks to
// coding agents through them (internal/ui/loopy*.go).

import (
	"context"
	"time"
)

// AssistantTools are a host's own tools for a run.
type AssistantTools struct {
	// Prompt comes first in the system prompt: who the agent is. The rest of
	// the prompt says how 1loop's tools work.
	Prompt string
	// Definitions are the tools, as chat completions declares them.
	Definitions []map[string]any
	// Run executes one of them; its text and pictures go back to the model.
	Run PanelToolHandler
}

// has says whether name is one of the assistant's tools.
func (tools *AssistantTools) has(name string) bool {
	if tools == nil {
		return false
	}
	for _, definition := range tools.Definitions {
		if function, ok := definition["function"].(map[string]any); ok && function["name"] == name {
			return true
		}
	}
	return false
}

// assistantOf is the run's assistant tools, from a tool's context.
func assistantOf(ctx context.Context) *AssistantTools {
	cfg, _ := ctx.Value(configContextKey{}).(Config)
	return cfg.Assistant
}

// Ask asks cfg's model one thing, without tools, apart from any chat.
func Ask(ctx context.Context, backend *App, cfg Config, system, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cfg.ClearSessions()
	cfg.RanWith, cfg.ContextKey, cfg.ServiceTier, cfg.toolSet, cfg.Assistant = nil, "ask", "", nil, nil
	switch cfg.Provider {
	case "claudecode":
		return claudeHandoffNote(ctx, cfg, system+"\n\n"+prompt)
	case "openai":
		var err error
		if cfg, err = backend.openAIConfig(ctx, cfg); err != nil {
			return "", err
		}
	}
	out, err := completion(ctx, cfg, []Message{{Role: "system", Content: system}, {Role: "user", Content: prompt}}, false, func(string, any) {})
	return out.Content, err
}
