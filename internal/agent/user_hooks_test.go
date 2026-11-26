// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"1loop/internal/extensions"
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

// countHookReads counts the hooks files read from now on.
func countHookReads(t *testing.T) *atomic.Int64 {
	var reads atomic.Int64
	readHooksFile = func(path string) ([]byte, error) {
		reads.Add(1)
		return os.ReadFile(path)
	}
	t.Cleanup(func() { readHooksFile = os.ReadFile })
	return &reads
}

func agePast(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// The hooks file is read again only when it changed, and an edit applies
// at the next event, two quick ones of the same size too (#519).
func TestHooksFileIsKept(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("AppData", dir)         // Windows
	t.Setenv("HOME", dir)            // macOS
	reads := countHookReads(t)
	one := `{"hooks": {"PreToolUse": [{"matcher": "shell", "hooks": [{"type": "command", "command": "a"}]}]}}`
	if err := SaveHooks(one); err != nil {
		t.Fatal(err)
	}
	agePast(t, HooksPath())
	for range 3 {
		if HookCount() != 1 {
			t.Fatal("hook missing")
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("%d reads for one unchanged file", reads.Load())
	}

	// An editor's write, not SaveHooks: only the file says it changed.
	two := `{"hooks": {"PreToolUse": [{"matcher": "shell", "hooks": [{"type": "command", "command": "a"}, {"command": "b"}]}]}}`
	os.WriteFile(HooksPath(), []byte(two), 0o600)
	if HookCount() != 2 {
		t.Fatal("edit not applied")
	}
	// The same size, at once: the time may not have moved.
	same := strings.Replace(two, `"b"`, `"c"`, 1)
	os.WriteFile(HooksPath(), []byte(same), 0o600)
	if file, _ := loadHooks(); file.Hooks["PreToolUse"][0].Hooks[1].Command != "c" {
		t.Fatal("a quick edit of the same size was missed")
	}
	os.WriteFile(HooksPath(), []byte(`{"hooks": `), 0o600)
	if _, err := loadHooks(); err == nil {
		t.Fatal("broken file not reported")
	}
	os.Remove(HooksPath())
	if HookCount() != 0 {
		t.Fatal("removed file still read")
	}
}

// pluginWithHook enables a plugin whose PreToolUse hook on Bash prints
// "from the plugin".
func pluginWithHook(t *testing.T) (dir, id string) {
	dir = fakePlugins(t, 2, 2)
	root := filepath.Join(extensions.Dir(), "plugins", "owner-repo-0")
	os.MkdirAll(filepath.Join(root, "hooks"), 0o700)
	hooks := `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo from the plugin"}]}]}}`
	if err := os.WriteFile(filepath.Join(root, "hooks", "hooks.json"), []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, "owner/repo-0"
}

// Turning a plugin off or on applies to its hooks at the next tool call.
func TestPluginHooksFollowSwitches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell command")
	}
	dir, id := pluginWithHook(t)
	call := func() string {
		result := runHooks(context.Background(), "PreToolUse", "shell", map[string]any{"cwd": dir})
		if len(result.problems) > 0 {
			t.Fatal(result.problems)
		}
		return result.output
	}
	if got := call(); got != "from the plugin" {
		t.Fatalf("enabled: %q", got)
	}
	if err := extensions.SetEnabled("plugin", id, false); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != "" {
		t.Fatalf("disabled: %q", got)
	}
	if err := extensions.SetEnabled("plugin", id, true); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != "from the plugin" {
		t.Fatalf("enabled again: %q", got)
	}
}

// marshalSpy tells whether the payload holding it was encoded.
type marshalSpy struct{ encoded *atomic.Bool }

func (spy marshalSpy) MarshalJSON() ([]byte, error) {
	spy.encoded.Store(true)
	return []byte(`"output"`), nil
}

// Without a hook for the tool, the payload (its whole output after it) is
// never encoded (#519).
func TestNoMatchingHookEncodesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell command")
	}
	dir, _ := pluginWithHook(t) // a plugin's hook, on Bash only
	if err := SaveHooks(`{"hooks": {"PostToolUse": [{"matcher": "edit_file", "hooks": [{"type": "command", "command": "true"}]}]}}`); err != nil {
		t.Fatal(err)
	}
	var encoded atomic.Bool
	for _, event := range []string{"PostToolUse", "Stop"} {
		result := runHooks(context.Background(), event, "read_file", map[string]any{"cwd": dir, "tool_response": marshalSpy{&encoded}})
		if encoded.Load() || len(result.problems) > 0 {
			t.Fatalf("%s: encoded %v, problems %v", event, encoded.Load(), result.problems)
		}
	}
	// The spy works: a matching hook gets the payload.
	runHooks(context.Background(), "PostToolUse", "edit_file", map[string]any{"cwd": dir, "tool_response": marshalSpy{&encoded}})
	if !encoded.Load() {
		t.Fatal("a matching hook's payload was not encoded")
	}
}

// Tool calls run their hooks side by side while the files change (run
// with -race).
func TestHooksInParallel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell command")
	}
	dir, id := pluginWithHook(t)
	var wait sync.WaitGroup
	for i := range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := range 5 {
				runHooks(context.Background(), "PreToolUse", "shell", map[string]any{"cwd": dir})
				runHooks(context.Background(), "PostToolUse", "shell", map[string]any{"cwd": dir})
				if i == 0 {
					extensions.SetEnabled("plugin", id, j%2 == 1)
					SaveHooks(`{"hooks": {"Stop": []}}`)
				}
			}
		}()
	}
	wait.Wait()
}
