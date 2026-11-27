// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"1loop/internal/agent"
)

// While Claude Code works on a server chat, a message sent and Stop are
// taken at once (#545).
func TestBusyClaudeTakesMessagesAndStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	bin := t.TempDir()
	body := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "` + bin + `/input.jsonl"
  case "$line" in
  *'"get_context_usage"'*)
    printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"1loop-context","response":{}}}' ;;
  *'"type":"user"'*)
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"s1","apiKeySource":"none"}'
    sleep 4
    printf '%s\n' '{"type":"assistant","message":{"id":"m","model":"x","content":[{"type":"text","text":"ok"}]}}'
    printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}' ;;
  esac
done
`
	os.WriteFile(filepath.Join(bin, "claude"), []byte(body), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	server, root, call := testServer(t)
	connection := agent.Connection{ID: "claude", Name: "Claude", Provider: "claudecode", Enabled: true, AllModels: true}
	server.mu.Lock()
	server.cfg = agent.Config{Provider: "claudecode", ConnectionID: "claude", Model: "sonnet", Workspace: root}
	server.choices = map[string]agent.ModelChoice{"sonnet · Claude": {Connection: connection, ID: "sonnet"}}
	server.mu.Unlock()
	post := func(path string, body map[string]any) (int, string, time.Duration) {
		raw, _ := json.Marshal(body)
		started := time.Now()
		code, data := call("POST", path, bytes.NewReader(raw))
		return code, string(data), time.Since(started)
	}
	code, data, _ := post("/api/send", map[string]any{"text": "first", "workspace": root})
	var reply struct{ Chat string }
	json.Unmarshal([]byte(data), &reply)
	if code != 200 {
		t.Fatalf("first: %d %s", code, data)
	}
	waitFor(t, server, "the run", func() bool { return server.chatLocked(reply.Chat).run != nil })
	time.Sleep(500 * time.Millisecond)
	code, data, took := post("/api/send", map[string]any{"chat": reply.Chat, "text": "second, while it works"})
	if code != 200 || took > 2*time.Second {
		t.Fatalf("follow-up while running: %d %s in %s", code, data, took)
	}
	waitFor(t, server, "the follow-up's turn", func() bool {
		input, _ := os.ReadFile(filepath.Join(bin, "input.jsonl"))
		return bytes.Contains(input, []byte("second, while it works"))
	})
	waitIdle(t, server, server.chatLocked(reply.Chat))
	server.mu.Lock()
	var users []string
	for _, message := range server.chatLocked(reply.Chat).History {
		if message.Role == "user" {
			users = append(users, message.Content)
		}
	}
	server.mu.Unlock()
	if len(users) != 2 {
		t.Fatalf("user messages: %q", users)
	}

	// Stop while it works ends the run.
	post("/api/send", map[string]any{"chat": reply.Chat, "text": "third"})
	waitFor(t, server, "the run", func() bool { return server.chatLocked(reply.Chat).run != nil })
	code, data, took = post("/api/stop", map[string]any{"chat": reply.Chat})
	if code != 200 || took > 2*time.Second {
		t.Fatalf("stop: %d %s in %s", code, data, took)
	}
	waitFor(t, server, "the stop", func() bool { return server.chatLocked(reply.Chat).run == nil })
	// It reads as a stop, as on the desktop, not as "context canceled" (#687).
	server.mu.Lock()
	items := server.chatLocked(reply.Chat).Items
	last := items[len(items)-1]
	server.mu.Unlock()
	if last.Kind != "error" || last.Text != agent.StoppedText {
		t.Fatalf("after Stop, the last item is %s %q, want error %q", last.Kind, last.Text, agent.StoppedText)
	}
}
