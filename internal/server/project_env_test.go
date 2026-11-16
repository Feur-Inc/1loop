// SPDX-License-Identifier: MPL-2.0

package server

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A server project's variables (#408) are read and replaced by paired
// clients only, kept in config.json, given to its chats' runs and its
// worktrees', never listed with the projects, and follow a rename.
func TestServerProjectEnv(t *testing.T) {
	server, call := pairedServer(t)
	code, body := call("POST", "/api/projects", map[string]any{"path": "demo", "create": true}, true)
	if code != 200 {
		t.Fatalf("add a project: %d %s", code, body)
	}
	var added struct{ Workspace string }
	json.Unmarshal([]byte(body), &added)
	project := added.Workspace
	env := map[string]string{"DB_URL": "postgres://u:pw-secret@db/app"}

	if code, _ = call("POST", "/api/projects/env", map[string]any{"path": project, "env": env}, false); code != 401 && code != 403 {
		t.Fatalf("an unpaired client set variables: %d", code)
	}
	if code, body = call("POST", "/api/projects/env", map[string]any{"path": project, "env": map[string]string{"1BAD": "x"}}, true); code != 400 {
		t.Fatalf("an invalid name: %d %s", code, body)
	}
	if code, body = call("POST", "/api/projects/env", map[string]any{"path": "/etc", "env": env}, true); code != 400 {
		t.Fatalf("an unregistered folder: %d %s", code, body)
	}
	if code, body = call("POST", "/api/projects/env", map[string]any{"path": project, "env": env}, true); code != 200 || strings.Contains(body, "pw-secret") {
		t.Fatalf("set: %d %s", code, body)
	}
	code, body = call("GET", "/api/projects/env?path="+project, nil, true)
	var read struct{ Env map[string]string }
	json.Unmarshal([]byte(body), &read)
	if code != 200 || !maps.Equal(read.Env, env) {
		t.Fatalf("read: %d %s", code, body)
	}
	for _, path := range []string{"/api/projects", "/api/state"} {
		if _, body = call("GET", path, nil, true); strings.Contains(body, "pw-secret") {
			t.Fatalf("%s shows the variables: %s", path, body)
		}
	}
	saved, _ := os.ReadFile(filepath.Join(server.dir, "config.json"))
	if !strings.Contains(string(saved), "pw-secret") {
		t.Fatal("the variables are not kept in config.json")
	}

	server.mu.Lock()
	chat := &serverChat{}
	chat.Config.Workspace = project
	cfg := server.runConfigLocked(chat, nil)
	server.worktrees = map[string]string{filepath.Join(project, ".worktrees", "x"): project}
	chat.Config.Workspace = filepath.Join(project, ".worktrees", "x")
	worktree := server.runConfigLocked(chat, nil)
	server.mu.Unlock()
	if !maps.Equal(cfg.Env, env) || !maps.Equal(worktree.Env, env) {
		t.Fatalf("a chat's run gets %v, a worktree's %v", cfg.Env, worktree.Env)
	}

	if code, body = call("POST", "/api/projects/rename", map[string]any{"path": project, "name": "renamed"}, true); code != 200 {
		t.Fatalf("rename: %d %s", code, body)
	}
	renamed := filepath.Join(filepath.Dir(project), "renamed")
	server.mu.Lock()
	moved, left := server.projectEnv[renamed], server.projectEnv[project]
	server.mu.Unlock()
	if !maps.Equal(moved, env) || left != nil {
		t.Fatalf("after the rename: %v, %v", moved, left)
	}
	if code, body = call("POST", "/api/projects/env", map[string]any{"path": renamed, "env": map[string]string{}}, true); code != 200 {
		t.Fatalf("clear: %d %s", code, body)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.projectEnv) != 0 {
		t.Fatalf("cleared variables kept: %v", server.projectEnv)
	}
}
