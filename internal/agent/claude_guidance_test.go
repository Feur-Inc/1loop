// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Where Claude Code's instructions and 1loop's meet, 1loop says which wins,
// and only about what this session has (#211).
func TestClaudeCodeGuidance(t *testing.T) {
	bare := claudeCodeGuidance(Config{Provider: "claudecode"})
	if !strings.Contains(bare, "Assisted-by") || strings.Contains(bare, "Memory:") || strings.Contains(bare, "Shells:") {
		t.Fatalf("without memory or terminals: %s", bare)
	}
	full := claudeCodeGuidance(Config{
		Provider:    "claudecode",
		Todo:        &TodoState{},
		Preferences: func(context.Context, func(string) (string, error)) error { return nil },
		Terminal:    func(context.Context, string, json.RawMessage) (PanelToolResult, error) { return PanelToolResult{}, nil },
	})
	for _, want := range []string{"Memory:", "AGENTS.md", "Shells:", "1loop's approval mode"} {
		if !strings.Contains(full, want) {
			t.Errorf("guidance lacks %q: %s", want, full)
		}
	}
}

// The user's text has its own heading, apart from 1loop's rules.
func TestUserPreferenceHeading(t *testing.T) {
	text := userPreferenceInstructions(Config{UserInstructions: "Likes coffee."})
	if !strings.Contains(text, "the user's own text") || !strings.HasSuffix(text, "\nLikes coffee.") {
		t.Fatalf("preferences: %q", text)
	}
}
