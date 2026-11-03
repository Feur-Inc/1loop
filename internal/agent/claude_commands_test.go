package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSlashCommand(t *testing.T) {
	for _, test := range []struct {
		text, name, args string
		ok               bool
	}{
		{"/status", "status", "", true},
		{"  /compact keep the API design ", "compact", "keep the API design", true},
		{"/compact\nkeep the tests", "compact", "keep the tests", true},
		{"/plugin:skill-name do it", "plugin:skill-name", "do it", true},
		{"/etc/hosts is wrong", "", "", false},
		{"/ hello", "", "", false},
		{"/", "", "", false},
		{"/-x", "", "", false},
		{"look at /status", "", "", false},
	} {
		name, args, ok := SlashCommand(test.text)
		if name != test.name || args != test.args || ok != test.ok {
			t.Errorf("SlashCommand(%q) = %q, %q, %v; want %q, %q, %v", test.text, name, args, ok, test.name, test.args, test.ok)
		}
	}
}

func TestClaudeInfoCommandAliases(t *testing.T) {
	info := ClaudeInfo{Commands: []ClaudeCommand{{Name: "usage", Aliases: []string{"cost", "stats"}}, {Name: "review"}}}
	for _, name := range []string{"usage", "cost", "review"} {
		if _, ok := info.Command(name); !ok {
			t.Errorf("%s not found", name)
		}
	}
	if _, ok := info.Command("status"); ok {
		t.Error("status found")
	}
}

// fakeClaude puts on PATH a claude that records its input in input.jsonl
// and answers the initialize handshake, then each prompt with script.
func fakeClaude(t *testing.T, script string) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir = t.TempDir()
	body := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "` + dir + `/input.jsonl"
  case "$line" in
  *'"initialize"'*)
    echo '{"type":"control_response","response":{"subtype":"success","request_id":"1loop-init","response":{"models":[{"value":"default"}],"commands":[{"name":"usage","description":"Show plan usage\nmore","argumentHint":"","aliases":["cost"]},{"name":"review","argumentHint":"[pr]"}],"claude_code_version":"9.9.9","account":{"email":"someone@example.test","organization":"Org","subscriptionType":"Claude Max"}}}}' ;;
  *'"type":"user"'*)
` + script + `
    ;;
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

func TestClaudeInfoFromHandshake(t *testing.T) {
	fakeClaude(t, "")
	backend := &App{}
	conn := Connection{ID: "claude", Provider: "claudecode"}
	work := t.TempDir()
	if _, ok := backend.CachedClaudeInfo(conn, work); ok {
		t.Fatal("cached before any handshake")
	}
	info, err := backend.ClaudeInfo(context.Background(), conn, work)
	if err != nil {
		t.Fatal(err)
	}
	if info.Email != "someone@example.test" || info.Subscription != "Claude Max" || info.Version != "9.9.9" || len(info.Commands) != 2 {
		t.Fatalf("info = %+v", info)
	}
	if cached, ok := backend.CachedClaudeInfo(conn, work); !ok || len(cached.Commands) != 2 {
		t.Fatal("handshake not cached")
	}
	if _, ok := backend.CachedClaudeInfo(Connection{ID: "other"}, work); ok {
		t.Fatal("another connection shares the listing")
	}
}

// A command goes to Claude Code as typed, even in a chat begun with another
// model, and the throwaway session it opens is not kept.
func TestClaudeCommandSentAsTyped(t *testing.T) {
	dir := fakeClaude(t, `    echo '{"type":"system","subtype":"init","session_id":"s-command","apiKeySource":"none"}'
    echo '{"type":"assistant","message":{"id":"m","model":"<synthetic>","content":[{"type":"text","text":"Current week: 8% used"}]}}'
    echo '{"type":"result","subtype":"success","is_error":false,"result":"Current week: 8% used","session_id":"s-command"}'`)
	history := []Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}, {Role: "user", Content: "/usage"}}
	var events []string
	var text strings.Builder
	emit := func(event string, value any) {
		events = append(events, event)
		if event == "delta" {
			text.WriteString(value.(string))
		}
	}
	cfg := Config{Provider: "claudecode", ConnectionID: "claude", Model: "default", Workspace: t.TempDir()}
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	if err := (&App{}).runClaudeCode(context.Background(), cfg, history, inbox, emit); err != nil {
		t.Fatal(err)
	}
	if text.String() != "Current week: 8% used" {
		t.Errorf("shown %q", text.String())
	}
	for _, event := range events {
		if event == "session" || event == "session_synced" {
			t.Errorf("command run emitted %s", event)
		}
	}
	input, _ := os.ReadFile(filepath.Join(dir, "input.jsonl"))
	if !strings.Contains(string(input), `"text":"/usage"`) || strings.Contains(string(input), "Earlier conversation") {
		t.Errorf("sent %s", input)
	}
}

// A command that answers nothing (/rename) is not reported as a failure.
func TestClaudeCommandWithoutOutput(t *testing.T) {
	fakeClaude(t, `    echo '{"type":"system","subtype":"init","session_id":"s1","apiKeySource":"none"}'
    echo '{"type":"result","subtype":"success","is_error":false,"result":"","session_id":"s1"}'`)
	cfg := Config{Provider: "claudecode", ConnectionID: "claude", Model: "default", Workspace: t.TempDir(), Session: "s1"}
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	err := (&App{}).runClaudeCode(context.Background(), cfg, []Message{{Role: "user", Content: "/rename tests"}}, inbox, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
}
