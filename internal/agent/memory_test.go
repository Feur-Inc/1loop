package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMemory calls the memory tool in Auto mode with a reviewer that records
// what it was asked and runs during, then allows it.
func runMemory(t *testing.T, cfg Config, workspace string, during func(), arguments map[string]string) (output, diff string, err error, reviewed []string) {
	t.Helper()
	review := func(_ context.Context, _ emitter, title, preview string) (bool, string) {
		reviewed = append(reviewed, title+"\n"+preview)
		if during != nil {
			during()
		}
		return true, ""
	}
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{review: review})
	ctx = context.WithValue(ctx, configContextKey{}, cfg)
	for _, field := range []string{"text", "old_text"} {
		if _, ok := arguments[field]; !ok {
			arguments[field] = ""
		}
	}
	raw, _ := json.Marshal(arguments)
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = "memory", string(raw)
	output, diff, err = (&App{Approvals: map[string]chan bool{}}).runTool(ctx, workspace, call, func() {}, func(string, any) {})
	return output, diff, err, reviewed
}

func TestMemoryProject(t *testing.T) {
	workspace := t.TempDir()
	agents := filepath.Join(workspace, "AGENTS.md")
	read := func() string { data, _ := os.ReadFile(agents); return string(data) }

	output, _, err, _ := runMemory(t, Config{}, workspace, nil, map[string]string{"action": "view", "scope": "project"})
	if err != nil || !strings.Contains(output, "Nothing is saved") {
		t.Fatalf("view of a missing AGENTS.md = %q, %v", output, err)
	}
	// The first entry creates the file, after the reviewer saw the diff.
	_, diff, err, reviewed := runMemory(t, Config{}, workspace, nil, map[string]string{"action": "add", "scope": "project", "text": "- Run tests with go test ./..."})
	if err != nil || read() != "- Run tests with go test ./...\n" {
		t.Fatalf("add = %q, %v", read(), err)
	}
	if len(reviewed) != 1 || !strings.HasPrefix(reviewed[0], "Save to AGENTS.md?\n") || !strings.Contains(diff, "+- Run tests with go test ./...") {
		t.Fatalf("add was reviewed as %q with diff %q", reviewed, diff)
	}
	if _, _, err, _ = runMemory(t, Config{}, workspace, nil, map[string]string{"action": "add", "scope": "project", "text": "- Format with gofmt"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err, _ = runMemory(t, Config{}, workspace, nil, map[string]string{"action": "replace", "scope": "project", "old_text": "go test ./...", "text": "./ci.sh"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err, _ = runMemory(t, Config{}, workspace, nil, map[string]string{"action": "remove", "scope": "project", "old_text": "- Format with gofmt"}); err != nil {
		t.Fatal(err)
	}
	if read() != "- Run tests with ./ci.sh\n" {
		t.Fatalf("AGENTS.md after replace and remove = %q", read())
	}
	if _, _, err, _ = runMemory(t, Config{}, workspace, nil, map[string]string{"action": "remove", "scope": "project", "old_text": "absent"}); err == nil {
		t.Fatal("removing text that is not saved succeeded")
	}
	// A change made while the user reads the diff is never overwritten.
	edit := func() { _ = os.WriteFile(agents, []byte("Edited by hand\n"), 0o644) }
	if _, _, err, _ = runMemory(t, Config{}, workspace, edit, map[string]string{"action": "add", "scope": "project", "text": "late"}); err == nil || read() != "Edited by hand\n" {
		t.Fatalf("a stale change was written: %q, %v", read(), err)
	}
}

func TestMemoryPreferences(t *testing.T) {
	if _, _, err, _ := runMemory(t, Config{}, t.TempDir(), nil, map[string]string{"action": "view", "scope": "user"}); err == nil {
		t.Fatal("the preferences were read without a store")
	}
	saved := "Answer in French."
	cfg := Config{Preferences: func(_ context.Context, update func(string) (string, error)) error {
		next, err := update(saved)
		if err == nil {
			saved = next
		}
		return err
	}}
	output, _, err, reviewed := runMemory(t, cfg, t.TempDir(), nil, map[string]string{"action": "add", "scope": "user", "text": "Call me Mopi.\n\n"})
	if err != nil || saved != "Answer in French.\nCall me Mopi." {
		t.Fatalf("add = %q, %v", saved, err)
	}
	if len(reviewed) != 1 || !strings.HasPrefix(reviewed[0], "Save to your preferences?\n") || !strings.HasPrefix(output, "Saved to your preferences") || strings.Contains(reviewed[0], "No newline") {
		t.Fatalf("add was reviewed as %q and answered %q", reviewed, output)
	}
	if output, _, _, _ = runMemory(t, cfg, t.TempDir(), nil, map[string]string{"action": "view", "scope": "user"}); output != saved {
		t.Fatalf("view = %q", output)
	}
	change := func() { saved = "Changed in Settings." }
	if _, _, err, _ = runMemory(t, cfg, t.TempDir(), change, map[string]string{"action": "remove", "scope": "user", "old_text": "Call me Mopi."}); err == nil || saved != "Changed in Settings." {
		t.Fatalf("a stale change was saved: %q, %v", saved, err)
	}
	big := strings.Repeat("x", preferencesLimit)
	if _, _, err, _ = runMemory(t, cfg, t.TempDir(), nil, map[string]string{"action": "add", "scope": "user", "text": big}); err == nil {
		t.Fatal("the preferences grew past their limit")
	}
}

func TestEditedMemory(t *testing.T) {
	for _, test := range []struct {
		text, action, add, old string
		trimmed                bool
		want                   string
	}{
		{"", "add", "a", "", false, "a\n"},
		{"a", "add", "b", "", false, "a\nb\n"},
		{"a\n\n", "add", "\nb\n", "", true, "a\nb"},
		{"a\nb\nc\n", "remove", "", "b", false, "a\nc\n"},
		{"a b c", "remove", "", " b", true, "a c"},
		{"a\nb\n", "replace", "B", "b", false, "a\nB\n"},
	} {
		got, err := editedMemory(test.text, test.action, test.add, test.old, test.trimmed)
		if err != nil || got != test.want {
			t.Errorf("editedMemory(%q, %s %q %q) = %q, %v, want %q", test.text, test.action, test.add, test.old, got, err, test.want)
		}
	}
	for _, test := range []struct{ text, action, add, old string }{
		{"a", "add", "  ", ""},
		{"a a", "replace", "b", "a"},
		{"a", "remove", "", ""},
		{"a", "forget", "", ""},
	} {
		if _, err := editedMemory(test.text, test.action, test.add, test.old, false); err == nil {
			t.Errorf("editedMemory(%q, %s %q %q) succeeded", test.text, test.action, test.add, test.old)
		}
	}
}

func TestDescribeMemory(t *testing.T) {
	for _, test := range []struct {
		args         string
		running      bool
		verb, target string
	}{
		{`{"action":"view","scope":"user"}`, false, "Read", "your preferences"},
		{`{"action":"add","scope":"project"}`, true, "Saving a memory to", "the project's AGENTS.md"},
		{`{"action":"replace","scope":"user"}`, false, "Saved a memory to", "your preferences"},
		{`{"action":"remove","scope":"project"}`, false, "Removed a memory from", "the project's AGENTS.md"},
	} {
		verb, target, _ := DescribeTool(ChatItem{Tool: "memory", Args: test.args}, test.running)
		if verb != test.verb || target != test.target {
			t.Errorf("DescribeTool(%s) = %q %q, want %q %q", test.args, verb, target, test.verb, test.target)
		}
	}
	// Claude Code keeps only the call's text: its diff is read back from it.
	if diff := memoryDiff("Saved to your preferences; it applies from the next turn.\n--- a/preferences\n+++ b/preferences\n"); diff != "--- a/preferences\n+++ b/preferences\n" {
		t.Errorf("memoryDiff = %q", diff)
	}
	if diff := memoryDiff("Nothing is saved in your preferences yet.\nmore"); diff != "" {
		t.Errorf("memoryDiff of a view = %q", diff)
	}
}
