// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
)

func TestTodoWatchRemindsOnce(t *testing.T) {
	cfg := Config{Todo: &TodoState{}}
	var watch todoWatch
	watch.observe("shell")
	watch.observe("read_file")
	if watch.reminder(cfg) != "" {
		t.Fatal("reminded after two calls")
	}
	watch.observe("shell")
	if text := watch.reminder(cfg); !strings.Contains(text, "without a TODO list") || !strings.Contains(text, "update_todo") {
		t.Fatalf("no reminder after three calls without a list: %q", text)
	}
	watch.observe("shell")
	if watch.reminder(cfg) != "" {
		t.Fatal("reminded twice")
	}
	// A list that stops moving.
	cfg.Todo = &TodoState{items: []TodoItem{{ID: "a", Text: "write the code", Status: "in_progress"}, {ID: "b", Text: "test", Status: "pending"}}}
	watch = todoWatch{}
	watch.observe("mcp__1loop_todo__update_todo")
	for i := 0; i < todoWatchStale; i++ {
		watch.observe("Bash")
	}
	if text := watch.reminder(cfg); !strings.Contains(text, "has not changed") {
		t.Fatalf("no stale reminder: %q", text)
	}
	if text := watch.endReminder(cfg); !strings.Contains(text, "in_progress: write the code") || !strings.Contains(text, "pending: test") {
		t.Fatalf("no end check: %q", text)
	}
	if watch.endReminder(cfg) != "" {
		t.Fatal("end check twice")
	}
	// A list all done ends quietly, as does a turn without tools or list.
	done := Config{Todo: &TodoState{items: []TodoItem{{ID: "a", Text: "x", Status: "completed"}}}}
	if (&todoWatch{calls: 5}).endReminder(done) != "" {
		t.Fatal("end check on a completed list")
	}
	if (&todoWatch{}).endReminder(cfg) != "" || (&todoWatch{calls: 5}).endReminder(Config{}) != "" {
		t.Fatal("end check without work or list")
	}
}

// Under Claude Code, a deferred tool's rules travel in its description,
// not in the system prompt.
func TestClaudeRulesInDescriptions(t *testing.T) {
	cfg := fullConfig()
	definitions := claudeMCPWithRules(cfg, claudeMCPDefinitions(cfg))
	descriptions := map[string]string{}
	for _, definition := range definitions {
		function := definition["function"].(map[string]any)
		descriptions[function["name"].(string)] = function["description"].(string)
	}
	if !strings.Contains(descriptions["memory"], "Long-term memory") {
		t.Errorf("memory's rules are missing: %q", descriptions["memory"])
	}
	if !strings.Contains(descriptions["browser_tabs"], browserInstructions(cfg)[2:40]) {
		t.Errorf("the browser rules are missing: %q", descriptions["browser_tabs"])
	}
	if strings.Contains(descriptions["update_todo"], "\n\n") {
		t.Error("update_todo got rules: its are in the prompt")
	}
	// The originals are untouched.
	for _, definition := range claudeMCPDefinitions(cfg) {
		function := definition["function"].(map[string]any)
		if function["name"] == "memory" && strings.Contains(function["description"].(string), "Long-term memory") {
			t.Fatal("the shared definition was changed")
		}
	}
}

func TestClaudeTurnNote(t *testing.T) {
	cfg := fullConfig()
	cfg.Todo = &TodoState{items: []TodoItem{{ID: "a", Text: "step one", Status: "in_progress"}}}
	note := claudeTurnNote(cfg, Message{Role: "user", Content: "hello"})
	if !strings.Contains(note, "step one") || !strings.HasPrefix(note, "[1loop context") {
		t.Fatalf("turn note: %q", note)
	}
	if claudeTurnNote(Config{}, Message{Content: "hi"}) != "" {
		t.Fatal("a note without list or mention")
	}
}
