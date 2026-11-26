// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"1loop/internal/terminal"
)

// noMachine is an SSH machine whose every operation fails: enough for what
// a run reads there before its first tool call (AGENTS.md).
type noMachine struct{}

var errNoMachine = errors.New("no machine in this test")

func (noMachine) Label() string                                         { return "gpu-box" }
func (noMachine) ReadFile(context.Context, string) ([]byte, error)      { return nil, errNoMachine }
func (noMachine) WriteFile(context.Context, string, []byte, bool) error { return errNoMachine }
func (noMachine) Remove(context.Context, string) error                  { return errNoMachine }
func (noMachine) MkdirAll(context.Context, string) error                { return errNoMachine }
func (noMachine) ReadDir(context.Context, string) ([]fs.FileInfo, error) {
	return nil, errNoMachine
}
func (noMachine) Lstat(context.Context, string) (fs.FileInfo, error) { return nil, errNoMachine }
func (noMachine) RealPath(context.Context, string) (string, error)   { return "", errNoMachine }
func (noMachine) Start(context.Context, string, string, io.Writer) (func() (int, error), error) {
	return nil, errNoMachine
}
func (noMachine) Terminal(context.Context, string) (terminal.Process, error) {
	return nil, errNoMachine
}

// Claude Code works in a chat on an SSH machine (#585): its own tools are
// off, so nothing it runs lands on this computer, and it starts in a local
// folder that stands for the machine's workspace, not in that remote path.
func TestClaudeCodeOnMachine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	body := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args.log"
pwd > "` + dir + `/pwd.log"
while IFS= read -r line; do
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
	cache := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("ONELOOP_PROJECT_LAUNCHER", "")

	cfg := Config{Provider: "claudecode", ConnectionID: "claude", Model: "sonnet", Workspace: "/srv/remote-project", MachineID: "m1", Machine: noMachine{}, ContextKey: "chat-machine"}
	t.Cleanup(func() { DropClaudeProcess("chat-machine") })
	if err := machineErr(cfg); err != nil {
		t.Fatalf("Claude Code refused on a machine: %v", err)
	}
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	if err := (&App{}).runClaudeCode(context.Background(), cfg, []Message{{Role: "user", Content: "which GPU?"}}, inbox, func(string, any) {}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "args.log"))
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	tools := ""
	for i, arg := range args {
		if arg == "--tools" && i+1 < len(args) {
			tools = args[i+1]
		}
	}
	if tools != "WebSearch" {
		t.Errorf("Claude Code's own tools: --tools %q, want only WebSearch", tools)
	}
	if prompt := string(data); !strings.Contains(prompt, "gpu-box") || !strings.Contains(prompt, "Use 1loop's MCP tools for all project work") {
		t.Error("the system prompt does not send project work to 1loop's tools on the machine")
	}
	pwd, _ := os.ReadFile(filepath.Join(dir, "pwd.log"))
	started := strings.TrimSpace(string(pwd))
	if real, err := filepath.EvalSymlinks(cache); err == nil && !strings.HasPrefix(started, real+string(filepath.Separator)) && !strings.HasPrefix(started, cache+string(filepath.Separator)) {
		t.Errorf("started in %q, not in a folder of 1loop's cache", started)
	}
	again, err := claudeMachineDir(cfg)
	if err != nil || filepath.Base(again) != filepath.Base(started) {
		t.Errorf("the next turn would start in %q, not %q: the session would not resume", again, started)
	}
}

// Antigravity's own tools cannot be turned off: it stays refused on a
// machine, and ChatGPT (Web), which only calls 1loop's tools, is not.
func TestMachineErrByProvider(t *testing.T) {
	for provider, refused := range map[string]bool{"claudecode": false, "chatgptweb": false, "openai": false, "antigravity": true} {
		err := machineErr(Config{Provider: provider, MachineID: "m1", Machine: noMachine{}})
		if (err != nil) != refused {
			t.Errorf("%s on a machine: %v", provider, err)
		}
	}
}

// Computer Use drives this computer's screen: off in a chat on a machine,
// while Browser Use stays.
func TestComputerUseOffOnMachine(t *testing.T) {
	handler := PanelToolHandler(func(context.Context, string, json.RawMessage) (PanelToolResult, error) { return PanelToolResult{}, nil })
	cfg := Config{Provider: "claudecode", Computer: handler}
	if !cfg.toolAvailable("computer_use") {
		t.Fatal("Computer Use is off on this computer")
	}
	cfg.MachineID, cfg.Machine = "m1", noMachine{}
	if cfg.toolAvailable("computer_use") {
		t.Error("Computer Use is offered in a chat on an SSH machine")
	}
	if handler, _ := toolContext(context.Background(), cfg).Value(computerContextKey{}).(PanelToolHandler); handler != nil {
		t.Error("Computer Use is attached to a run on an SSH machine")
	}
}

// Claude Code gives its own working directory as the shell's folder: on a
// machine that local folder stands for the workspace there.
func TestMachineArgs(t *testing.T) {
	dir := "/home/me/.cache/1loop/claude-machines/ab"
	for raw, want := range map[string]string{
		`{"command":"hostname","workdir":"/home/me/.cache/1loop/claude-machines/ab"}`: `{"command":"hostname","workdir":"/srv/p"}`,
		`{"path":"/home/me/.cache/1loop/claude-machines/ab/src/main.go"}`:             `{"path":"/srv/p/src/main.go"}`,
		`{"path":"src/main.go"}`:                                  `{"path":"src/main.go"}`,
		`{"workdir":"/home/me/.cache/1loop/claude-machines/abc"}`: `{"workdir":"/home/me/.cache/1loop/claude-machines/abc"}`,
	} {
		if got := string(machineArgs(json.RawMessage(raw), dir, "/srv/p")); got != want {
			t.Errorf("%s: %s, want %s", raw, got, want)
		}
	}
}
