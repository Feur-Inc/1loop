// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Claude Code pauses once its call has its result, with both in the history
// (#18): the fake one would go on for ever.
func TestClaudeCodePauses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	body := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
  *'"type":"user"'*)
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"s1","apiKeySource":"none"}'
    printf '%s\n' '{"type":"assistant","message":{"id":"m","model":"x","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make"}}]}}'
    printf '%s\n' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"made"}]}}'
    sleep 30 ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Cleanup(func() { DropClaudeProcess("chat-pause") })
	pause := NewPauseSignal()
	pause.Ask()
	pause.Ask() // twice is fine
	cfg := Config{Provider: "claudecode", ConnectionID: "claude", Model: "sonnet", Workspace: t.TempDir(), ContextKey: "chat-pause", Pause: pause}
	var history []Message
	emit := func(kind string, value any) {
		if kind == "history" {
			history = value.([]Message)
		}
	}
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	began := time.Now()
	err := (&App{}).runClaudeCode(context.Background(), cfg, []Message{{Role: "user", Content: "build"}}, inbox, emit)
	if !errors.Is(err, ErrPaused) || time.Since(began) > 15*time.Second {
		t.Fatalf("%v after %s", err, time.Since(began))
	}
	if len(history) != 3 || len(history[1].ToolCalls) != 1 || history[2].Content != "made" {
		t.Errorf("history %+v", history)
	}
}
