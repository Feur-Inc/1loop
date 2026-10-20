package agent

// Background tasks: shell commands started with run_in_background. They
// have no time limit and outlive the turn that started them; their output
// is kept (start and end, as for any command). When one ends, the host
// wakes the model with WakeMessage: a new turn, or a message joining the
// running one. Each has its own Stop, apart from the chat's.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"1loop/internal/platform"
)

// BackgroundTaskInfo is a task as the panels and the phone show it.
type BackgroundTaskInfo struct {
	ID       string    `json:"id"`
	Command  string    `json:"command"`
	Running  bool      `json:"running"`
	Stopped  bool      `json:"stopped,omitempty"` // ended by Stop
	ExitCode int       `json:"exitCode"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`
}

type backgroundTask struct {
	info    BackgroundTaskInfo
	output  lockedOutput
	cancel  context.CancelFunc
	command *exec.Cmd
}

// lockedOutput is an outputBuffer the command writes while the model reads.
type lockedOutput struct {
	mu     sync.Mutex
	buffer outputBuffer
}

func (out *lockedOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.buffer.Write(p)
}

func (out *lockedOutput) String() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.buffer.String()
}

// BackgroundTasks are one chat's. OnFinish (wake the model) and OnChange
// (draw the panel again) are the host's; they run on the task's goroutine.
type BackgroundTasks struct {
	mu       sync.Mutex
	tasks    []*backgroundTask
	next     int
	OnFinish func(info BackgroundTaskInfo, wake Message)
	OnChange func()
}

// every registry with a task, for StopEveryTask.
var everyTasks struct {
	mu   sync.Mutex
	list []*BackgroundTasks
}

// StopEveryTask ends every background task of the process: 1loop is told
// to stop by a signal, with no time to close its chats one by one.
func StopEveryTask() {
	everyTasks.mu.Lock()
	list := append([]*BackgroundTasks{}, everyTasks.list...)
	everyTasks.mu.Unlock()
	for _, tasks := range list {
		tasks.StopAll()
	}
}

func (tasks *BackgroundTasks) changed() {
	if tasks.OnChange != nil {
		tasks.OnChange()
	}
}

// maxRunningTasks bounds a chat's running tasks: a model starting one
// after another in a loop must not fill the machine with processes.
const maxRunningTasks = 16

// start runs command (already approved and prepared: workspace, sandbox)
// without waiting for it.
func (tasks *BackgroundTasks) start(command *exec.Cmd, text string, cancel context.CancelFunc) (string, error) {
	tasks.mu.Lock()
	running := 0
	for _, task := range tasks.tasks {
		if task.info.Running {
			running++
		}
	}
	if running >= maxRunningTasks {
		tasks.mu.Unlock()
		cancel()
		return "", fmt.Errorf("%d background tasks are already running; stop one (background_task) or wait for one to end", running)
	}
	tasks.next++
	task := &backgroundTask{info: BackgroundTaskInfo{ID: fmt.Sprint(tasks.next), Command: text, Running: true, Started: time.Now()}, cancel: cancel, command: command}
	tasks.mu.Unlock()
	command.Stdout, command.Stderr = &task.output, &task.output
	if err := command.Start(); err != nil {
		cancel()
		return "", err
	}
	tasks.mu.Lock()
	first := len(tasks.tasks) == 0
	tasks.tasks = append(tasks.tasks, task)
	tasks.mu.Unlock()
	if first {
		everyTasks.mu.Lock()
		everyTasks.list = append(everyTasks.list, tasks)
		everyTasks.mu.Unlock()
	}
	tasks.changed()
	go func() {
		err := command.Wait()
		cancel()
		tasks.mu.Lock()
		task.info.Running, task.info.Ended = false, time.Now()
		task.info.ExitCode = command.ProcessState.ExitCode()
		if err != nil && task.info.ExitCode == 0 {
			task.info.ExitCode = -1
		}
		info := task.info
		tasks.mu.Unlock()
		tasks.changed()
		if tasks.OnFinish != nil {
			tasks.OnFinish(info, WakeMessage(info, headAndTail(task.output.String(), 2000)))
		}
	}()
	return task.info.ID, nil
}

// Snapshot is every task of the chat, oldest first.
func (tasks *BackgroundTasks) Snapshot() []BackgroundTaskInfo {
	if tasks == nil {
		return nil
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	out := make([]BackgroundTaskInfo, 0, len(tasks.tasks))
	for _, task := range tasks.tasks {
		out = append(out, task.info)
	}
	return out
}

func (tasks *BackgroundTasks) find(id string) *backgroundTask {
	for _, task := range tasks.tasks {
		if task.info.ID == id {
			return task
		}
	}
	return nil
}

// Stop ends a running task and everything it started.
func (tasks *BackgroundTasks) Stop(id string) bool {
	if tasks == nil {
		return false
	}
	tasks.mu.Lock()
	task := tasks.find(id)
	running := task != nil && task.info.Running
	if running {
		task.info.Stopped = true
	}
	tasks.mu.Unlock()
	if running {
		// Killed now, not when the context's goroutine gets to it: a signal
		// handler exits right after StopEveryTask.
		platform.KillProcessTree(task.command)
		task.cancel()
	}
	return running
}

// StopAll ends every running task: the chat is deleted, or 1loop quits.
func (tasks *BackgroundTasks) StopAll() {
	if tasks == nil {
		return
	}
	for _, info := range tasks.Snapshot() {
		if info.Running {
			tasks.Stop(info.ID)
		}
	}
}

// WakeMessage tells the model a task ended, with the end of its output.
func WakeMessage(info BackgroundTaskInfo, output string) Message {
	ended := fmt.Sprintf("exited with code %d", info.ExitCode)
	if info.Stopped {
		ended = "was stopped by the user"
	}
	return Message{Role: "user", Internal: true, Content: fmt.Sprintf("[Background task %s %s after %s]\n$ %s\n%s",
		info.ID, ended, info.Ended.Sub(info.Started).Round(time.Second), info.Command, strings.TrimSpace(output))}
}

// WakeSummary is the line a chat shows for a wake message.
func WakeSummary(m Message) string {
	first, _, _ := strings.Cut(m.Content, "\n")
	return strings.Trim(first, "[]")
}

func backgroundToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "background_task",
		"description": "Look after commands started with shell's run_in_background: list them, read a task's output (its start and end, up to max_output_tokens; null: 10000), or stop one. You are told when a task ends, so do not poll in a loop.",
		"parameters": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "id", "max_output_tokens"}, "properties": map[string]any{
			"action":            map[string]any{"type": "string", "enum": []string{"list", "output", "stop"}},
			"id":                map[string]any{"type": []string{"string", "null"}},
			"max_output_tokens": map[string]any{"type": []string{"integer", "null"}},
		}},
	}}
}

// backgroundTool answers background_task.
func backgroundTool(tasks *BackgroundTasks, arguments string) (string, error) {
	if tasks == nil {
		return "", fmt.Errorf("Background tasks are not attached to this conversation")
	}
	var args struct {
		Action    string
		ID        *string
		MaxTokens *int `json:"max_output_tokens"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	describe := func(info BackgroundTaskInfo) string {
		state := "running for " + time.Since(info.Started).Round(time.Second).String()
		if !info.Running {
			state = fmt.Sprintf("ended with code %d after %s", info.ExitCode, info.Ended.Sub(info.Started).Round(time.Second))
			if info.Stopped {
				state = "stopped"
			}
		}
		return fmt.Sprintf("%s · %s · $ %s", info.ID, state, info.Command)
	}
	if args.Action == "list" {
		var lines []string
		for _, info := range tasks.Snapshot() {
			lines = append(lines, describe(info))
		}
		if len(lines) == 0 {
			return "No background task.", nil
		}
		return strings.Join(lines, "\n"), nil
	}
	if args.ID == nil {
		return "", fmt.Errorf("Give the task's id")
	}
	tasks.mu.Lock()
	task := tasks.find(*args.ID)
	tasks.mu.Unlock()
	if task == nil {
		return "", fmt.Errorf("No background task %s", *args.ID)
	}
	switch args.Action {
	case "output":
		tokens := 10000
		if args.MaxTokens != nil {
			tokens = min(50000, max(1000, *args.MaxTokens))
		}
		tasks.mu.Lock()
		info := task.info
		tasks.mu.Unlock()
		return describe(info) + "\n" + headAndTail(task.output.String(), tokens), nil
	case "stop":
		if !tasks.Stop(*args.ID) {
			return "Task " + *args.ID + " was not running.", nil
		}
		return "Stopping task " + *args.ID + ".", nil
	}
	return "", fmt.Errorf("Unknown action %q", args.Action)
}

// shellTimeout is the shell tool's limit: 60 seconds unless the model sets
// timeout_ms, up to 10 minutes; longer work runs in the background.
func shellTimeout(milliseconds *int) time.Duration {
	if milliseconds == nil {
		return 60 * time.Second
	}
	return time.Duration(min(600000, max(1000, *milliseconds))) * time.Millisecond
}

// startBackground prepares and starts an approved command as a task.
func startBackground(ctx context.Context, tasks *BackgroundTasks, root, command string, sandboxed bool) (string, error) {
	if tasks == nil {
		return "", fmt.Errorf("Background tasks are not attached to this conversation")
	}
	// The task outlives the turn: its context is not the turn's.
	taskCtx, cancel := context.WithCancel(context.Background())
	cmd := platform.ShellCommand(taskCtx, command)
	platform.WorkspaceCommand(cmd, root, platform.WorkspaceYOLO(ctx))
	if sandboxed {
		if err := platform.SandboxCommand(cmd, root); err != nil {
			cancel()
			return "", err
		}
	}
	platform.PrepareProcess(cmd)
	cmd.Cancel = func() error { return platform.KillProcessTree(cmd) }
	cmd.WaitDelay = 2 * time.Second
	id, err := tasks.start(cmd, command, cancel)
	if err != nil {
		return "", err
	}
	return "Started background task " + id + ": $ " + command + "\nIt has no time limit. You will be told when it ends, with the end of its output; read it meanwhile with background_task (action output) and stop it with action stop.", nil
}
