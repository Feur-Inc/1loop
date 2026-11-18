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

// A message queued during a Claude Code task becomes the next turn once
// the task is over, not a message steered into it (#433).
func TestClaudeQueuedMessageIsTheNextTurn(t *testing.T) {
	dir := keptClaude(t)
	asked := 0
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) {
		asked++
		if asked == 1 {
			return Message{Role: "user", Content: "queued message"}, true
		}
		return Message{}, false
	}}
	cfg := Config{Provider: "claudecode", ConnectionID: "claude", Model: "sonnet", Workspace: t.TempDir(), ContextKey: "chat-queue"}
	t.Cleanup(func() { DropClaudeProcess("chat-queue") })
	var injected []string
	emit := func(kind string, value any) {
		if kind == "injected" {
			injected = append(injected, value.(Message).Content)
		}
	}
	if err := (&App{}).runClaudeCode(context.Background(), cfg, []Message{{Role: "user", Content: "first"}}, inbox, emit); err != nil {
		t.Fatal(err)
	}
	input, _ := os.ReadFile(filepath.Join(dir, "input.jsonl"))
	var prompts []string
	for _, line := range strings.Split(string(input), "\n") {
		if strings.Contains(line, `"type":"user"`) {
			prompts = append(prompts, line)
		}
	}
	if len(prompts) != 2 || !strings.Contains(prompts[0], "first") || !strings.Contains(prompts[1], "queued message") {
		t.Fatalf("prompts sent to Claude Code: %q", prompts)
	}
	if strings.Contains(prompts[1], "while you were working") {
		t.Fatal("the queued message was sent as a steered one")
	}
	if len(injected) != 1 || injected[0] != "queued message" {
		t.Fatalf("injected %q", injected)
	}
}
