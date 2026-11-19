// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// keptClaude puts on PATH a claude that counts its starts in starts.log and
// answers each prompt, and 1loop's context question, at once.
func keptClaude(t *testing.T) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir = t.TempDir()
	body := `#!/bin/sh
echo start >> "` + dir + `/starts.log"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "` + dir + `/input.jsonl"
  case "$line" in
  *'"get_context_usage"'*)
    printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"1loop-context","response":{}}}' ;;
  *'"type":"user"'*)
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"s1","apiKeySource":"none"}'
    printf '%s\n' '{"type":"assistant","message":{"id":"m","model":"x","content":[{"type":"text","text":"ok"}]}}'
    printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}' ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ONELOOP_PROJECT_LAUNCHER", "")
	return dir
}

func TestClaudeProcessKeptBetweenTurns(t *testing.T) {
	dir := keptClaude(t)
	work := t.TempDir()
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	turn := func(cfg Config, text string) {
		t.Helper()
		if err := (&App{}).runClaudeCode(context.Background(), cfg, []Message{{Role: "user", Content: text}}, inbox, func(string, any) {}); err != nil {
			t.Fatal(err)
		}
	}
	starts := func() int {
		data, _ := os.ReadFile(filepath.Join(dir, "starts.log"))
		return strings.Count(string(data), "start")
	}
	cfg := Config{Provider: "claudecode", ConnectionID: "claude", Model: "sonnet", Workspace: work, ContextKey: "chat-kept"}
	t.Cleanup(func() { DropClaudeProcess("chat-kept") })
	turn(cfg, "one")
	cfg.Session = "s1" // the session the first turn reported
	turn(cfg, "two")
	turn(cfg, "three")
	if n := starts(); n != 1 {
		t.Fatalf("%d processes for three turns of one chat", n)
	}
	input, _ := os.ReadFile(filepath.Join(dir, "input.jsonl"))
	if !strings.Contains(string(input), `"mcp_reconnect"`) && cfg.Todo != nil {
		t.Error("the kept process was not asked for 1loop's tools again")
	}
	// Another model: another process.
	cfg.Model = "opus"
	turn(cfg, "four")
	if n := starts(); n != 2 {
		t.Fatalf("a changed model reused the process: %d starts", n)
	}
	// A dropped chat's process ends.
	DropClaudeProcess("chat-kept")
	claudeParked.Lock()
	_, kept := claudeParked.byChat["chat-kept"]
	claudeParked.Unlock()
	if kept {
		t.Fatal("still kept after DropClaudeProcess")
	}
}

// A Read sent with a command waits for it in Claude Code: its row must not
// count the command's time as its own.
func TestClaudeRunningQueue(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  int
	}{
		{nil, 0},
		{[]string{"Bash", "Read"}, 1},
		{[]string{"Read", "Grep", "Bash", "Read"}, 2},
		{[]string{"Read"}, 1},
		{[]string{"Edit", "Edit"}, 1},
		{[]string{"mcp__1loop_todo__update_todo", "Bash"}, 1},
	} {
		if got := claudeRunning(c.names); got != c.want {
			t.Errorf("claudeRunning(%v) = %d, want %d", c.names, got, c.want)
		}
	}
}
