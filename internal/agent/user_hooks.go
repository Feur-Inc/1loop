package agent

// Hooks: the user's own commands around the agent's work, in Claude Code's
// format, from ~/.config/1loop/hooks.json (the user's file only: a project
// cannot bring commands of its own).
//
//	{"hooks": {
//	  "PreToolUse":  [{"matcher": "shell|apply_patch", "hooks": [{"type": "command", "command": "~/bin/guard.sh"}]}],
//	  "PostToolUse": [{"matcher": "edit_file|apply_patch", "hooks": [{"type": "command", "command": "gofmt -w ."}]}],
//	  "UserPromptSubmit": [...], "Stop": [...]}}
//
// A hook receives the event as JSON on stdin (hook_event_name, session_id,
// cwd, tool_name, tool_input, tool_response, prompt). Exit code 0 lets the
// work go on; for UserPromptSubmit its output joins the context. Exit code 2
// blocks a PreToolUse (its stderr is the tool's error for the model) and
// hands a PostToolUse's stderr to the model. Any other failure is reported
// in the chat and the work goes on. Each command has 60 seconds by default.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"1loop/internal/platform"
)

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"` // seconds
}

type hookMatcher struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
}

type hooksFile struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// HooksPath is the user's hooks file.
func HooksPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "hooks.json")
}

// loadHooks reads the hooks file at each event, so an edit applies at once.
func loadHooks() (hooksFile, error) {
	var file hooksFile
	data, err := os.ReadFile(HooksPath())
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("%s: %w", HooksPath(), err)
	}
	return file, nil
}

// hookResult is what an event's hooks said: blocked (exit 2) with feedback,
// output to add (exit 0), and problems to show.
type hookResult struct {
	blocked  bool
	feedback string
	output   string
	problems []string
}

func runHooks(ctx context.Context, event, target string, payload map[string]any) hookResult {
	var result hookResult
	file, err := loadHooks()
	if err != nil {
		result.problems = append(result.problems, err.Error())
		return result
	}
	payload["hook_event_name"] = event
	input, _ := json.Marshal(payload)
	cwd, _ := payload["cwd"].(string)
	for _, matcher := range file.Hooks[event] {
		if !hookMatches(matcher.Matcher, target) {
			continue
		}
		for _, hook := range matcher.Hooks {
			if hook.Type != "" && hook.Type != "command" || strings.TrimSpace(hook.Command) == "" {
				continue
			}
			timeout := time.Duration(max(1, hook.Timeout)) * time.Second
			if hook.Timeout == 0 {
				timeout = 60 * time.Second
			}
			run, cancel := context.WithTimeout(ctx, timeout)
			cmd := platform.ShellCommand(run, hook.Command)
			platform.WorkspaceCommand(cmd, cwd, platform.WorkspaceYOLO(ctx))
			cmd.Env = append(os.Environ(), "ONELOOP_WORKSPACE="+cwd, "ONELOOP_HOOK_EVENT="+event)
			cmd.Stdin = bytes.NewReader(input)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			platform.PrepareProcess(cmd)
			cmd.Cancel = func() error { return platform.KillProcessTree(cmd) }
			err := cmd.Run()
			cancel()
			code := 0
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else if err != nil {
				code = -1
			}
			platform.Devf("hooks", "%s %q on %s: exit %d", event, hook.Command, target, code)
			switch code {
			case 0:
				if text := strings.TrimSpace(stdout.String()); text != "" {
					result.output += text + "\n"
				}
			case 2:
				result.blocked = true
				result.feedback += strings.TrimSpace(stderr.String()) + "\n"
			default:
				detail := strings.TrimSpace(stderr.String())
				if detail == "" && err != nil {
					detail = err.Error()
				}
				result.problems = append(result.problems, fmt.Sprintf("Hook %q failed (%s): %s", hook.Command, event, platform.DevText(detail, 300)))
			}
		}
	}
	result.feedback = strings.TrimSpace(result.feedback)
	result.output = strings.TrimSpace(result.output)
	return result
}

// hookMatches: an empty matcher or "*" matches every tool; otherwise a
// regular expression on the whole tool name, as in Claude Code.
func hookMatches(matcher, target string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && re.MatchString(target)
}

// HookCount is how many hook commands the user's file holds.
func HookCount() int {
	file, _ := loadHooks()
	count := 0
	for _, matchers := range file.Hooks {
		for _, matcher := range matchers {
			count += len(matcher.Hooks)
		}
	}
	return count
}

func reportHookProblems(emit emitter, result hookResult) {
	for _, problem := range result.problems {
		emit("note", problem)
	}
}
