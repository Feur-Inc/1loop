// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"1loop/internal/platform"
)

// A project's environment variables (#408) reach the shell tool and its
// background tasks, and are neither saved with the chat nor written in
// the model's instructions.
func TestProjectEnvReachesShellNotModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash commands")
	}
	secret := `s3cr'et "$HOME"`
	cfg := Config{Workspace: t.TempDir(), Env: map[string]string{"ONELOOP_TEST_TOKEN": secret, "bad name": "x"}}
	tasks := &BackgroundTasks{}
	done := make(chan BackgroundTaskInfo, 1)
	tasks.OnFinish = func(info BackgroundTaskInfo, _ Message) { done <- info }
	cfg.Tasks = tasks
	ctx := context.WithValue(toolContext(context.Background(), cfg), approvalContextKey{}, approvalPolicy{yolo: true})

	text, err := runShell(t, ctx, cfg.Workspace, map[string]any{"command": `printf %s "$ONELOOP_TEST_TOKEN"`})
	if got := commandJSON(t, text); err != nil || got["stdout"] != secret {
		t.Fatalf("the shell tool did not get the project's variable: %v %+v", err, got)
	}
	if _, err := runShell(t, ctx, cfg.Workspace, map[string]any{"command": `printf %s "$ONELOOP_TEST_TOKEN" > out.txt`, "run_in_background": true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the background task did not end")
	}
	text, _ = runShell(t, ctx, cfg.Workspace, map[string]any{"command": "cat out.txt"})
	if got := commandJSON(t, text); got["stdout"] != secret {
		t.Fatalf("a background task did not get the project's variable: %+v", got)
	}

	saved, _ := json.Marshal(cfg)
	if strings.Contains(string(saved), "s3cr") || strings.Contains(string(saved), "ONELOOP_TEST_TOKEN") {
		t.Fatalf("the variables are saved with the chat: %s", saved)
	}
	if prompt := systemInstructions(cfg); strings.Contains(prompt, "s3cr") || strings.Contains(prompt, "ONELOOP_TEST_TOKEN") {
		t.Fatal("the variables are in the model's instructions")
	}
}

// Antigravity CLI runs receive the project's environment variables (#408)
// through agyProcess, just like Claude Code and the built-in shell tool.
func TestProjectEnvReachesAntigravityProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub")
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "agy")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ONELOOP_FLATPAK", "")

	ctx := platform.WithProjectEnv(context.Background(), map[string]string{"TEST1LOOP": "hello-env"})
	cmd, err := agyCommand(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("agyCommand: %v", err)
	}
	if !slices.Contains(cmd.Env, "TEST1LOOP=hello-env") {
		t.Fatalf("agyCommand env missing TEST1LOOP=hello-env")
	}
}
