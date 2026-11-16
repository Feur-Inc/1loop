// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/agent"
	"1loop/internal/browser"
)

// The slim image has no Chromium (ONELOOP_BROWSER=none): the server says
// so, and Browser Use fails at once with why, instead of opening a tab that
// downloads or starts a browser it cannot run.
func TestServerWithoutBrowser(t *testing.T) {
	t.Setenv("ONELOOP_BROWSER", "none")
	server, _, call := testServer(t)
	chat := &serverChat{ID: "c1", Title: "t", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	server.mu.Lock()
	server.chats = []*serverChat{chat}
	server.mu.Unlock()

	t.Setenv("PATH", t.TempDir()) // no claude command either
	t.Setenv("HOME", t.TempDir())
	for _, path := range []string{"/api/state"} {
		code, data := call("GET", path, nil)
		var reply struct {
			Browser    *bool
			ClaudeCode *bool
		}
		if err := json.Unmarshal(data, &reply); code != 200 || err != nil || reply.Browser == nil || *reply.Browser {
			t.Fatalf("%s reports no browser: %d %s", path, code, data)
		}
		if reply.ClaudeCode == nil || *reply.ClaudeCode {
			t.Fatalf("%s reports no Claude Code: %d %s", path, code, data)
		}
	}

	tools := server.browserTools(chat)
	for _, call := range []struct{ name, args string }{
		{"browser_tabs", `{"action":"open","url":"https://example.com"}`},
		{"browser_navigate", `{"action":"navigate","url":"https://example.com"}`},
		{"browser_read", `{"mode":"snapshot"}`},
	} {
		_, err := tools(context.Background(), call.name, json.RawMessage(call.args))
		if err == nil || err.Error() != browser.NoBrowserProblem {
			t.Fatalf("%s: %v, want %q", call.name, err, browser.NoBrowserProblem)
		}
	}
	code, data := call("POST", "/api/chats/c1/panel", strings.NewReader(`{"action":"open","kind":"browser","url":"https://example.com"}`))
	if code != 400 || !strings.Contains(string(data), "no browser") {
		t.Fatalf("panel open: %d %s", code, data)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(chat.tabs) != 0 || chat.session != nil {
		t.Fatalf("no tab and no browser session: %d tabs", len(chat.tabs))
	}
}

func TestServerReportsBrowser(t *testing.T) {
	t.Setenv("ONELOOP_BROWSER", "")
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0755)
	t.Setenv("PATH", bin)
	_, _, call := testServer(t)
	code, data := call("GET", "/api/state", nil)
	if code != 200 || !strings.Contains(string(data), `"browser":true`) || !strings.Contains(string(data), `"claudeCode":true`) {
		t.Fatalf("state: %d %s", code, data)
	}
}
