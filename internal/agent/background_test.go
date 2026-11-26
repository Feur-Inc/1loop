// SPDX-License-Identifier: MPL-2.0

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
	output, _, err := (&App{Approvals: map[string]chan ApprovalAnswer{}}).runTool(ctx, workspace, call, func() {}, func(string, any) {})
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

// wait blocks until the task ends and answers with its end, instead of a
// wake message; output says when nothing new came since the last read.
func TestBackgroundWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash commands")
	}
	tasks := &BackgroundTasks{}
	woken := make(chan Message, 64)
	tasks.OnFinish = func(_ BackgroundTaskInfo, wake Message) { woken <- wake }
	yolo := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: true})
	ctx := context.WithValue(yolo, tasksContextKey{}, tasks)
	if _, err := runShell(t, ctx, t.TempDir(), map[string]any{"command": "echo first; sleep 0.6; echo done", "run_in_background": true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if out := backgroundCall(t, ctx, `{"action":"output","id":"1","max_output_tokens":null,"wait_seconds":null}`); !strings.Contains(out, "first") {
		t.Fatalf("output: %q", out)
	}
	if out := backgroundCall(t, ctx, `{"action":"output","id":"1","max_output_tokens":null,"wait_seconds":null}`); !strings.Contains(out, "No new output") {
		t.Fatalf("second output: %q", out)
	}
	start := time.Now()
	out := backgroundCall(t, ctx, `{"action":"wait","id":"1","max_output_tokens":null,"wait_seconds":10}`)
	if !strings.Contains(out, "ended with code 0") || !strings.Contains(out, "done") || time.Since(start) > 5*time.Second {
		t.Fatalf("wait: %q after %v", out, time.Since(start))
	}
	select {
	case wake := <-woken:
		t.Fatalf("a waited task also woke the model: %q", wake.Content)
	case <-time.After(300 * time.Millisecond):
	}
	// A wait that gives up says so; the task's end then wakes the model.
	if _, err := runShell(t, ctx, t.TempDir(), map[string]any{"command": "sleep 1.5", "run_in_background": true}); err != nil {
		t.Fatal(err)
	}
	if out := backgroundCall(t, ctx, `{"action":"wait","id":"2","max_output_tokens":null,"wait_seconds":1}`); !strings.Contains(out, "Still running after 1s") {
		t.Fatalf("timed out wait: %q", out)
	}
	select {
	case <-woken:
	case <-time.After(5 * time.Second):
		t.Fatal("no wake after a wait that gave up")
	}
}

func TestBackgroundTaskOf(t *testing.T) {
	cases := []struct{ tool, args, result, want string }{
		{"shell", `{"command":"npm run dev","run_in_background":true}`, "Started background task 3: $ npm run dev\nIt has no time limit.", "3"},
		{"shell", `{"command":"ls"}`, "exit 0\nREADME.md", ""},
		{"background_task", `{"action":"output","id":"7"}`, "7 · running", "7"},
		{"background_task", `{"action":"list","id":null}`, "", ""},
	}
	for _, c := range cases {
		if got := BackgroundTaskOf(c.tool, c.args, c.result); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}

// The Background tab reads a task's state and the end of its output, cut at
// a line start (#597).
func TestBackgroundOutput(t *testing.T) {
	tasks := &BackgroundTasks{}
	dir := t.TempDir()
	command := "for i in $(seq 1 300); do echo line-$i; done"
	if runtime.GOOS == "windows" {
		command = `1..300 | ForEach-Object { "line-$_" }`
	}
	result, err := startBackground(context.Background(), tasks, dir, dir, command, false)
	if err != nil {
		t.Fatal(err)
	}
	id := BackgroundTaskOf("shell", "{}", result)
	if id == "" {
		t.Fatalf("no task in %q", result)
	}
	tasks.mu.Lock()
	task := tasks.find(id)
	tasks.mu.Unlock()
	<-task.done
	info, out, ok := tasks.Output(id, 200)
	if !ok || info.Running || info.ExitCode != 0 {
		t.Fatalf("task %+v ok %v", info, ok)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "line-300") || strings.HasPrefix(out, "ine") || len(out) > 200 {
		t.Fatalf("the end of the output, cut at a line: %q", out)
	}
	if _, _, ok := tasks.Output("nope", 200); ok {
		t.Fatal("an unknown task has an output")
	}
}
