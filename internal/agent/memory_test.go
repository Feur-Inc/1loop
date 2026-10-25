package agent

import (
	"context"
	"encoding/json"
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

// Project facts go in AGENTS.md with the file tools; the settings edit it
// without overwriting a change made since they read it.
func TestProjectMemory(t *testing.T) {
	workspace := t.TempDir()
	if text, err := ProjectMemory(workspace); text != "" || err != nil {
		t.Fatalf("ProjectMemory of a missing AGENTS.md = %q, %v", text, err)
	}
	if err := SaveProjectMemory(workspace, "", "- Run ./ci.sh\n"); err != nil {
		t.Fatal(err)
	}
	if err := SaveProjectMemory(workspace, "", "late\n"); err == nil {
		t.Fatal("a stale AGENTS.md was overwritten")
	}
	if text, _ := ProjectMemory(workspace); text != "- Run ./ci.sh\n" {
		t.Fatalf("AGENTS.md = %q", text)
	}
	// In Auto mode, writing AGENTS.md goes to the reviewer like a command.
	for title, safe := range map[string]bool{"Apply changes to main.go?": true, "Apply changes to AGENTS.md?": false, "Apply changes to main.go, docs/AGENTS.md?": false} {
		if obviouslySafe(workspace, title, "") != safe {
			t.Errorf("obviouslySafe(%q) = %v", title, !safe)
		}
	}
}

func TestMemoryPreferences(t *testing.T) {
	if _, _, err, _ := runMemory(t, Config{}, t.TempDir(), nil, map[string]string{"action": "view"}); err == nil {
		t.Fatal("the preferences were read without a store")
	}
	if (Config{}).toolAvailable("memory") {
		t.Fatal("memory is offered while it is turned off")
	}
	saved := "Answer in French."
	cfg := Config{Preferences: func(_ context.Context, update func(string) (string, error)) error {
		next, err := update(saved)
		if err == nil {
			saved = next
		}
		return err
	}}
	output, _, err, reviewed := runMemory(t, cfg, t.TempDir(), nil, map[string]string{"action": "add", "text": "Call me Mopi.\n\n"})
	if err != nil || saved != "Answer in French.\nCall me Mopi." {
		t.Fatalf("add = %q, %v", saved, err)
	}
	if len(reviewed) != 1 || !strings.HasPrefix(reviewed[0], "Save to your preferences?\n") || !strings.HasPrefix(output, "Saved to your preferences") || strings.Contains(reviewed[0], "No newline") {
		t.Fatalf("add was reviewed as %q and answered %q", reviewed, output)
	}
	if output, _, _, _ = runMemory(t, cfg, t.TempDir(), nil, map[string]string{"action": "view"}); output != saved {
		t.Fatalf("view = %q", output)
	}
	change := func() { saved = "Changed in Settings." }
	if _, _, err, _ = runMemory(t, cfg, t.TempDir(), change, map[string]string{"action": "remove", "old_text": "Call me Mopi."}); err == nil || saved != "Changed in Settings." {
		t.Fatalf("a stale change was saved: %q, %v", saved, err)
	}
	big := strings.Repeat("x", preferencesLimit)
	if _, _, err, _ = runMemory(t, cfg, t.TempDir(), nil, map[string]string{"action": "add", "text": big}); err == nil {
		t.Fatal("the preferences grew past their limit")
	}
}

func TestEditedMemory(t *testing.T) {
	for _, test := range []struct{ text, action, add, old, want string }{
		{"", "add", "a", "", "a"},
		{"a", "add", "b", "", "a\nb"},
		{"a\n\n", "add", "\nb\n", "", "a\nb"},
		{"a\nb\nc", "remove", "", "b", "a\nc"},
		{"a b c", "remove", "", " b", "a c"},
		{"a\nb", "replace", "B", "b", "a\nB"},
	} {
		got, err := editedMemory(test.text, test.action, test.add, test.old)
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
		if _, err := editedMemory(test.text, test.action, test.add, test.old); err == nil {
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
		{`{"action":"view"}`, false, "Read", "your preferences"},
		{`{"action":"add"}`, true, "Saving a memory to", "your preferences"},
		{`{"action":"replace"}`, false, "Saved a memory to", "your preferences"},
		{`{"action":"remove"}`, false, "Removed a memory from", "your preferences"},
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
