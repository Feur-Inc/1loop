// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
)

// Claude Code reads the instructions once, when it starts: they must not
// promise a snapshot with every request, and the reminder after a compaction
// carries the whole live list (#204).
func TestTodoLiveListForClaudeCode(t *testing.T) {
	cfg := Config{Provider: "claudecode", Todo: &TodoState{}}
	if strings.Contains(todoInstructions(cfg), "comes with every request") {
		t.Fatal("Claude Code is told a snapshot comes with every request")
	}
	if _, err := cfg.Todo.update([]byte(`{"add":["Write the test","Run it"],"updates":[],"remove":[],"order":[],"clear":false}`)); err != nil {
		t.Fatal(err)
	}
	reminder := todoResumeReminder(cfg)
	for _, text := range []string{"Write the test", "Run it"} {
		if !strings.Contains(reminder, text) {
			t.Fatalf("reminder lacks %q: %s", text, reminder)
		}
	}
	if strings.Contains(reminder, "currently empty") {
		t.Fatalf("reminder: %s", reminder)
	}
}
