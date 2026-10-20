package agent

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runShell(t *testing.T, ctx context.Context, workspace string, arguments map[string]any) (string, error) {
	t.Helper()
	full := map[string]any{"max_output_tokens": nil, "timeout_ms": nil, "run_in_background": nil, "escalate": nil, "justification": nil}
	for key, value := range arguments {
		full[key] = value
	}
	raw, _ := json.Marshal(full)
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = "shell", string(raw)
	output, _, err := (&App{Approvals: map[string]chan bool{}}).runTool(ctx, workspace, call, func() {}, func(string, any) {})
	return output, err
}

func backgroundCall(t *testing.T, ctx context.Context, arguments string) string {
	t.Helper()
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = "background_task", arguments
	output, _, err := (&App{}).runTool(ctx, t.TempDir(), call, func() {}, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func TestBackgroundTasks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash commands")
	}
	tasks := &BackgroundTasks{}
	woken := make(chan Message, 64)
	tasks.OnFinish = func(_ BackgroundTaskInfo, wake Message) { woken <- wake }
	yolo := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: true})
	ctx := context.WithValue(yolo, tasksContextKey{}, tasks)
	workspace := t.TempDir()

	// A task outlives the call that started it, and wakes the model at its end.
	output, err := runShell(t, ctx, workspace, map[string]any{"command": "sleep 0.3; echo built", "run_in_background": true})
	if err != nil || !strings.Contains(output, "Started background task 1") {
		t.Fatalf("start: %v %q", err, output)
	}
	if list := backgroundCall(t, ctx, `{"action":"list","id":null,"max_output_tokens":null}`); !strings.Contains(list, "1 · running") {
		t.Fatalf("list while running: %q", list)
	}
	select {
	case wake := <-woken:
		if !wake.Internal || wake.Role != "user" || !strings.Contains(wake.Content, "Background task 1 exited with code 0") || !strings.Contains(wake.Content, "built") {
			t.Fatalf("wake message: %+v", wake)
		}
		if WakeSummary(wake) != "Background task 1 exited with code 0 after 0s" {
			t.Fatalf("summary: %q", WakeSummary(wake))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no wake after the task ended")
	}
	if out := backgroundCall(t, ctx, `{"action":"output","id":"1","max_output_tokens":null}`); !strings.Contains(out, "ended with code 0") || !strings.Contains(out, "built") {
		t.Fatalf("output: %q", out)
	}

	// Its own Stop ends it and what it started.
	if _, err := runShell(t, ctx, workspace, map[string]any{"command": "sleep 60", "run_in_background": true}); err != nil {
		t.Fatal(err)
	}
	if out := backgroundCall(t, ctx, `{"action":"stop","id":"2","max_output_tokens":null}`); !strings.Contains(out, "Stopping task 2") {
		t.Fatalf("stop: %q", out)
	}
	select {
	case wake := <-woken:
		if !strings.Contains(wake.Content, "Background task 2 was stopped by the user") {
			t.Fatalf("stopped wake: %q", wake.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stopped task did not end")
	}
	if snapshot := tasks.Snapshot(); len(snapshot) != 2 || snapshot[1].Running || !snapshot[1].Stopped {
		t.Fatalf("snapshot: %+v", snapshot)
	}

	// A model starting tasks in a loop is stopped at maxRunningTasks.
	for i := 0; i < maxRunningTasks; i++ {
		if _, err := runShell(t, ctx, workspace, map[string]any{"command": "sleep 60", "run_in_background": true}); err != nil {
			t.Fatalf("task %d: %v", i, err)
		}
	}
	if _, err := runShell(t, ctx, workspace, map[string]any{"command": "sleep 60", "run_in_background": true}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("task over the limit: %v", err)
	}
	tasks.StopAll()
}

func TestShellTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash commands")
	}
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: true})
	started := time.Now()
	if _, err := runShell(t, ctx, t.TempDir(), map[string]any{"command": "sleep 5", "timeout_ms": 1000}); err == nil || time.Since(started) > 4*time.Second {
		t.Fatalf("timeout_ms 1000: %v after %s", err, time.Since(started))
	}
	if shellTimeout(nil) != time.Minute {
		t.Fatal("default timeout is not 60 s")
	}
	big, small := 3_600_000, 1
	if shellTimeout(&big) != 10*time.Minute || shellTimeout(&small) != time.Second {
		t.Fatal("timeout_ms is not bounded to 1 s – 10 min")
	}
}
