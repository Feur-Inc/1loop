// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestSaveHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("AppData", dir)         // Windows
	t.Setenv("HOME", dir)            // macOS
	if content, err := ReadHooks(); err != nil || content != hooksTemplate {
		t.Fatalf("no file yet: %q %v", content, err)
	}
	for _, bad := range []struct{ content, problem string }{
		{`{"hooks": `, "Not valid hooks JSON"},
		{`{"hooks": {"BeforeTool": []}}`, "Unknown event"},
		{`{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": " "}]}]}}`, "needs"},
		{`{"hooks": {"PreToolUse": [{"matcher": "(", "hooks": [{"type": "command", "command": "x"}]}]}}`, "not a valid pattern"},
		{`{"hookz": {}}`, "unknown field"},
	} {
		if err := SaveHooks(bad.content); err == nil || !strings.Contains(err.Error(), bad.problem) {
			t.Fatalf("%s: %v", bad.content, err)
		}
	}
	if _, err := os.Stat(HooksPath()); !os.IsNotExist(err) {
		t.Fatal("an invalid file was written")
	}
	good := `{"hooks": {"PostToolUse": [{"matcher": "edit_file|apply_patch", "hooks": [{"type": "command", "command": "gofmt -w ."}]}]}}`
	if err := SaveHooks(good); err != nil {
		t.Fatal(err)
	}
	if content, _ := ReadHooks(); content != good || HookCount() != 1 {
		t.Fatalf("saved: %q, %d hooks", content, HookCount())
	}
}

// A chatty hook's output is cut before it joins the model's context.
func TestHookOutputIsCut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell command")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	hooks := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "head -c 3000000 /dev/zero | tr '\\0' a"}]}]}}`
	if err := SaveHooks(hooks); err != nil {
		t.Fatal(err)
	}
	result := runHooks(context.Background(), "UserPromptSubmit", "", map[string]any{"cwd": dir, "prompt": "hi"})
	if len(result.problems) > 0 || len(result.output) > hookContextLimit+100 || !strings.Contains(result.output, "skipped") {
		t.Fatalf("problems %v, %d characters kept", result.problems, len(result.output))
	}
}
