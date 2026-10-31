package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func commandJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err, text)
	}
	return value
}

func TestShellDirectoryAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "child")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: true})
	ctx = context.WithValue(ctx, tasksContextKey{}, &BackgroundTasks{})
	command := "printf out; printf err >&2; exit 7"
	if runtime.GOOS == "windows" {
		command = "[Console]::Out.Write('out'); [Console]::Error.Write('err'); exit 7"
	}
	text, err := runShell(t, ctx, root, map[string]any{"command": command, "workdir": "child"})
	got := commandJSON(t, text)
	if err == nil || got["cwd"] != sub || got["stdout"] != "out" || got["stderr"] != "err" || got["exit_code"] != float64(7) || got["status"] != "failed" {
		t.Fatalf("%v %+v", err, got)
	}
	for _, dir := range []string{"file", "missing", ".."} {
		if _, err := shellDirectory(ctx, nil, root, dir); err == nil {
			t.Fatalf("accepted invalid directory %q", dir)
		}
	}
	outside := t.TempDir()
	if _, err := shellDirectory(ctx, nil, root, outside); err == nil {
		t.Fatal("ungranted absolute path accepted")
	}
	granted := context.WithValue(ctx, grantsContextKey{}, &PermissionGrants{folders: []string{outside}})
	if dir, err := shellDirectory(granted, nil, root, outside); err != nil || dir != outside {
		t.Fatal(dir, err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := shellDirectory(ctx, nil, root, "escape"); err == nil {
			t.Fatal("symlink escape accepted")
		}
	}

	text, err = runShell(t, ctx, root, map[string]any{"command": "pwd"})
	if err != nil || commandJSON(t, text)["cwd"] != root {
		t.Fatalf("cwd persisted: %v %s", err, text)
	}
	if runtime.GOOS != "windows" {
		text, err = runShell(t, ctx, root, map[string]any{"command": "sleep 5", "timeout_ms": 1000})
		if err == nil || commandJSON(t, text)["status"] != "timed_out" {
			t.Fatalf("timeout: %v %s", err, text)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)
	slow := "sleep 5"
	if runtime.GOOS == "windows" {
		slow = "Start-Sleep -Seconds 5"
	}
	text, err = runShell(t, cancelled, root, map[string]any{"command": slow})
	if err == nil || commandJSON(t, text)["status"] != "cancelled" {
		t.Fatalf("cancellation: %v %s", err, text)
	}
}

func TestCommandOutputRetentionAndIsolation(t *testing.T) {
	ctx := context.WithValue(context.Background(), tasksContextKey{}, &BackgroundTasks{})
	capture := &commandCapture{}
	capture.stdout.Write([]byte(strings.Repeat("x", 5000) + "middle🟠" + strings.Repeat("z", 5000)))
	tokens := 1000
	got := commandJSON(t, commandResult(ctx, "/fixture", capture, 0, nil, &tokens))
	id := got["output_id"].(string)
	read := func(ctx context.Context, offset, limit int) (string, error) {
		raw, _ := json.Marshal(map[string]any{"id": id, "stream": "stdout", "offset": offset, "limit": limit})
		return readCommandOutput(ctx, string(raw))
	}
	text, err := read(ctx, 5000, 100)
	if err != nil || !strings.Contains(commandJSON(t, text)["text"].(string), "middle🟠") {
		t.Fatal(text, err)
	}
	text, err = read(ctx, 5006, 1)
	if err != nil || commandJSON(t, text)["next_offset"] != float64(5010) {
		t.Fatal(text, err)
	}
	other := context.WithValue(context.Background(), tasksContextKey{}, &BackgroundTasks{})
	if _, err := read(other, 0, 100); err == nil {
		t.Fatal("output leaked to another chat")
	}
	for i := 0; i < 8; i++ {
		commandResult(ctx, "/fixture", &commandCapture{}, 0, nil, nil)
	}
	if _, err := read(ctx, 0, 100); err == nil {
		t.Fatal("old output was not evicted")
	}
	capture = &commandCapture{}
	capture.stdout.Write([]byte(strings.Repeat("x", commandStreamLimit+100)))
	got = commandJSON(t, commandResult(ctx, "/fixture", capture, 0, nil, nil))
	if got["retention_truncated"] != true || len(capture.stdout.data) != commandStreamLimit {
		t.Fatal("unbounded or unreported output")
	}
}

func TestSearchEmptyAndError(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		pattern, status string
		failed          bool
	}{{"needle", "matches", false}, {"absent", "no_matches", false}, {"[", "failed", true}} {
		raw, _ := json.Marshal(map[string]any{"path": ".", "pattern": tc.pattern})
		text, err := searchFiles(ctx, root, string(raw))
		if (err != nil) != tc.failed || commandJSON(t, text)["status"] != tc.status {
			t.Fatalf("%q %v %s", tc.pattern, err, text)
		}
	}
	t.Setenv("PATH", t.TempDir())
	raw, _ := json.Marshal(map[string]any{"path": ".", "pattern": "needle"})
	text, err := searchFiles(ctx, root, string(raw))
	if err == nil || commandJSON(t, text)["status"] != "failed" {
		t.Fatal("missing rg was hidden", text, err)
	}

}
