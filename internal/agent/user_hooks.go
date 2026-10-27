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
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`    // set: the command runs with them, without a shell
	Timeout int      `json:"timeout"` // seconds
	Async   bool     `json:"async"`   // runs in the background; it cannot block or answer
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
	// The user's file, then the enabled plugins' hooks: written for Claude
	// Code, they also match its names of 1loop's tools, and find their
	// plugin's folder in CLAUDE_PLUGIN_ROOT.
	type source struct {
		matchers []hookMatcher
		root     string
	}
	sources := []source{{file.Hooks[event], ""}}
	for _, plugin := range pluginHookFiles() {
		var hooks hooksFile
		if data, err := os.ReadFile(plugin.File); err == nil && json.Unmarshal(data, &hooks) == nil {
			sources = append(sources, source{hooks.Hooks[event], plugin.Root})
		}
	}
	for _, from := range sources {
		for _, matcher := range from.matchers {
			if !hookMatches(matcher.Matcher, target) && (from.root == "" || !claudeHookMatches(matcher.Matcher, target)) {
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
				parent := ctx
				if hook.Async {
					parent = context.Background() // outlives the tool call it follows
				}
				run, cancel := context.WithTimeout(parent, timeout)
				expand := func(text string) string {
					if from.root == "" {
						return text
					}
					return strings.ReplaceAll(text, "${CLAUDE_PLUGIN_ROOT}", from.root)
				}
				var cmd *exec.Cmd
				if len(hook.Args) > 0 {
					args := make([]string, len(hook.Args))
					for i, arg := range hook.Args {
						args[i] = expand(arg)
					}
					cmd = exec.CommandContext(run, expand(hook.Command), args...)
				} else {
					cmd = platform.ShellCommand(run, expand(hook.Command))
				}
				platform.WorkspaceCommand(cmd, cwd, platform.WorkspaceYOLO(ctx))
				cmd.Env = append(os.Environ(), "ONELOOP_WORKSPACE="+cwd, "ONELOOP_HOOK_EVENT="+event)
				if from.root != "" {
					cmd.Env = append(cmd.Env, "CLAUDE_PLUGIN_ROOT="+from.root, "CLAUDE_PROJECT_DIR="+cwd)
				}
				cmd.Stdin = bytes.NewReader(input)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				platform.PrepareProcess(cmd)
				cmd.Cancel = func() error { return platform.KillProcessTree(cmd) }
				if hook.Async {
					if err := cmd.Start(); err != nil {
						cancel()
						result.problems = append(result.problems, fmt.Sprintf("Hook %q failed (%s): %s", hook.Command, event, err))
						continue
					}
					go func() {
						err := cmd.Wait()
						cancel()
						platform.Devf("hooks", "%s %q in the background on %s: %v %s", event, hook.Command, target, err, platform.DevText(strings.TrimSpace(stderr.String()), 300))
					}()
					continue
				}
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

// claudeHookMatches tries Claude Code's names of a 1loop tool.
func claudeHookMatches(matcher, target string) bool {
	for _, name := range claudeToolNames[target] {
		if hookMatches(matcher, name) {
			return true
		}
	}
	return false
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

// hookEvents are the events a hook can follow.
var hookEvents = []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop"}

// hooksTemplate is what an editor starts from when there is no file yet.
const hooksTemplate = "{\n  \"hooks\": {\n    \"PreToolUse\": [],\n    \"PostToolUse\": [],\n    \"UserPromptSubmit\": [],\n    \"Stop\": []\n  }\n}\n"

// ReadHooks is the hooks file as text, for an editor (the template when it
// does not exist yet).
func ReadHooks() (string, error) {
	data, err := os.ReadFile(HooksPath())
	if errors.Is(err, os.ErrNotExist) {
		return hooksTemplate, nil
	}
	return string(data), err
}

// SaveHooks checks an edited hooks file and writes it: valid JSON in
// Claude Code's shape, known events, each hook a command. An edit applies
// at the next event.
func SaveHooks(content string) error {
	var file hooksFile
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return fmt.Errorf("Not valid hooks JSON: %w", err)
	}
	for event, matchers := range file.Hooks {
		if !Contains(hookEvents, event) {
			return fmt.Errorf("Unknown event %q: use %s", event, strings.Join(hookEvents, ", "))
		}
		for _, matcher := range matchers {
			if matcher.Matcher != "" {
				if _, err := regexp.Compile(matcher.Matcher); err != nil {
					return fmt.Errorf("%s: matcher %q is not a valid pattern", event, matcher.Matcher)
				}
			}
			for _, hook := range matcher.Hooks {
				if hook.Type != "" && hook.Type != "command" || strings.TrimSpace(hook.Command) == "" {
					return fmt.Errorf("%s: each hook needs \"type\": \"command\" and a command", event)
				}
			}
		}
	}
	path := HooksPath()
	if path == "" {
		return fmt.Errorf("No configuration folder")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".hooks-*")
	if err != nil {
		return err
	}
	_, err = temp.WriteString(content)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temp.Name(), 0600)
	}
	if err != nil {
		os.Remove(temp.Name())
		return err
	}
	return os.Rename(temp.Name(), path)
}
