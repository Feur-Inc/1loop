package agent

// Claude Code connection. 1loop does not replace Claude Code's harness here:
// it starts the Claude Code installed on this computer, unmodified, in its
// documented programmatic mode
//
//	claude -p --input-format stream-json --output-format stream-json
//
// and only displays and drives it. Claude Code keeps its own login (the
// user's subscription), tools, permission rules, CLAUDE.md and MCP servers.
// 1loop sends the prompts, shows the stream (text, thinking, tool calls and
// their results) and answers the permission requests Claude Code delegates
// to its host.
//
// Billing guard: Claude Code reports how each run is paid for. If a run is
// not on the subscription login, or starts drawing on extra usage, 1loop
// stops it at once and says why.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"1loop/internal/core"
	"1loop/internal/platform"
)

// claudeEvent is one line of Claude Code's stream-json output; only the
// fields 1loop reads are declared.
type claudeEvent struct {
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	APIError     int     `json:"api_error_status"` // result: the API's HTTP status, 429 on a usage limit
	SessionID    string  `json:"session_id"`
	Parent       *string `json:"parent_tool_use_id"` // set for a subagent's messages
	APIKeySource string  `json:"apiKeySource"`
	Status       string  `json:"status"`
	Tasks        []struct {
		Description string `json:"description"`
	} `json:"tasks"` // background_tasks_changed: what still runs in the background
	Event *struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	} `json:"event"`
	Message *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"tool_use_result"`
	RequestID     string          `json:"request_id"`
	Request       *struct {
		Subtype  string          `json:"subtype"`
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	} `json:"request"`
	Response *struct {
		Response struct {
			Models []claudeModel `json:"models"`
		} `json:"response"`
	} `json:"response"`
	RateLimit *struct {
		Status         string `json:"status"`
		ResetsAt       int64  `json:"resetsAt"`
		IsUsingOverage bool   `json:"isUsingOverage"`
		Windows        map[string]struct {
			Utilization float64 `json:"utilization"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
	IsError     bool   `json:"is_error"`
	Result      string `json:"result"`
	QueuedTurns int    `json:"queued_turn_count"`
	ModelUsage  map[string]struct {
		ContextWindow int `json:"contextWindow"`
	} `json:"modelUsage"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type claudeModel struct {
	Value       string   `json:"value"`
	DisplayName string   `json:"displayName"`
	Resolved    string   `json:"resolvedModel"`
	Description string   `json:"description"`
	Efforts     []string `json:"supportedEffortLevels"`
}

// PlanUsage is how much of the subscription's windows is used (0..1).
type PlanUsage struct {
	FiveHour, Week float64
}

// claudeDisabledTools are the Claude Code tools 1loop turns off, with
// Claude Code's own --disallowedTools option.
//
// Subagents (Task, named Agent in other versions) are off FOR NOW: 1loop
// has no view of what a subagent does, so the conversation would show one
// opaque row while a whole second agent works. To allow them again once the
// interface can show their steps, empty this list.
const claudeDisabledTools = "Task,Agent"

// claudeCommand prepares a Claude Code process in its stream-json mode.
// The environment is the user's, minus what would change who pays (an API
// key) and minus the variables of a Claude Code session 1loop may have been
// started from.
func claudeCommand(ctx context.Context, dir string, extra ...string) (*exec.Cmd, error) {
	path, err := ClaudeExecutable()
	if err != nil {
		return nil, err
	}
	args := append([]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, extra...)
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		// The API client retains its private login. All model-controlled
		// project operations go through 1loop's MCP tools and launcher.
		args = append(args, "--tools", "", "--setting-sources", "", "--strict-mcp-config", "--no-chrome", "--disable-slash-commands")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	for _, pair := range os.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		if name == "ANTHROPIC_API_KEY" || name == "ANTHROPIC_AUTH_TOKEN" || name == "CLAUDECODE" ||
			name == "CLAUDE_PID" || name == "CLAUDE_EFFORT" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			continue
		}
		cmd.Env = append(cmd.Env, pair)
	}
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
	}
	// Stop ends the turn the way Claude Code documents (SIGINT), then the
	// whole process group if it lingers.
	platform.PrepareProcess(cmd)
	cmd.Cancel = func() error { return platform.InterruptProcess(cmd) }
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}

// claudeModels asks the installed Claude Code which models the account can
// use. It only exchanges the protocol's handshake: no request reaches a model.
func (backend *App) claudeModels(ctx context.Context) ([]claudeModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := claudeCommand(ctx, os.TempDir())
	if err != nil {
		return nil, err
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	fmt.Fprintln(stdin, `{"type":"control_request","request_id":"1loop-init","request":{"subtype":"initialize"}}`)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var claudeEventValue claudeEvent
		if json.Unmarshal(scanner.Bytes(), &claudeEventValue) != nil {
			continue
		}
		if claudeEventValue.Type == "control_response" && claudeEventValue.Response != nil {
			if len(claudeEventValue.Response.Response.Models) == 0 {
				return nil, fmt.Errorf("Claude Code listed no model: check that it is signed in (run claude once)")
			}
			return claudeEventValue.Response.Response.Models, nil
		}
		if claudeEventValue.Type == "result" && claudeEventValue.IsError {
			return nil, errors.New(FirstLine(claudeEventValue.Result))
		}
	}
	return nil, fmt.Errorf("Claude Code did not answer: run claude once in a terminal to check it works")
}

// claudeLimit tells a turn Claude Code ended on a usage limit without its
// rate_limit_event (it happens on a weekly limit): the result's API status
// is 429, or its text says so, older versions adding "|<reset in Unix time>".
func claudeLimit(status int, text string) (time.Time, bool) {
	lower := strings.ToLower(text)
	if status != 429 && !strings.Contains(lower, "usage limit") && !strings.Contains(lower, "rate limit") && !strings.Contains(lower, "limit reached") {
		return time.Time{}, false
	}
	if _, after, found := strings.Cut(text, "|"); found {
		if at, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64); err == nil && at > 0 {
			return unixAuto(at), true
		}
	}
	return time.Time{}, true
}

// claudeTool translates a Claude Code tool call into the row 1loop shows:
// commands, reads and edits use 1loop's own rows (and their detail panel);
// every other tool keeps its name and arguments.
func claudeTool(root, name string, input json.RawMessage) (tool, args string) {
	if strings.HasPrefix(name, "mcp__1loop_todo__") {
		return strings.TrimPrefix(name, "mcp__1loop_todo__"), string(input)
	}
	var in struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Notebook string `json:"notebook_path"`
		Old      string `json:"old_string"`
		New      string `json:"new_string"`
		Content  string `json:"content"`
	}
	_ = json.Unmarshal(input, &in)
	path := in.FilePath
	if path == "" {
		path = in.Notebook
	}
	if rel, err := filepath.Rel(root, path); err == nil && path != "" && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	switch name {
	case "Bash":
		return "shell", encode(map[string]string{"command": in.Command})
	case "Read":
		return "read_file", encode(map[string]string{"path": path})
	case "Edit", "MultiEdit", "NotebookEdit":
		return "edit_file", encode(map[string]string{"path": path, "old_text": in.Old, "new_text": in.New})
	case "Write":
		return "edit_file", encode(map[string]string{"path": path, "old_text": "", "new_text": in.Content})
	}
	return name, string(input)
}

// claudeTarget is what a call being written is about, with a path inside
// the workspace shown relative to it.
func claudeTarget(root, arguments string) string {
	target := toolTarget(arguments)
	if rel, err := filepath.Rel(root, target); err == nil && filepath.IsAbs(target) && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return target
}

// claudeDiff rebuilds a unified diff from the structured patch Claude Code
// attaches to an edit's result.
func claudeDiff(path string, result json.RawMessage) string {
	var r struct {
		Patch []struct {
			OldStart, OldLines, NewStart, NewLines int
			Lines                                  []string
		} `json:"structuredPatch"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if json.Unmarshal(result, &r) != nil {
		return ""
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n", path, path)
	if len(r.Patch) == 0 {
		if r.Type != "create" || r.Content == "" {
			return ""
		}
		lines := strings.Split(strings.TrimRight(r.Content, "\n"), "\n")
		fmt.Fprintf(&builder, "@@ -0,0 +1,%d @@\n", len(lines))
		for _, line := range lines {
			builder.WriteString("+" + line + "\n")
		}
		return builder.String()
	}
	for _, hunk := range r.Patch {
		fmt.Fprintf(&builder, "@@ -%d,%d +%d,%d @@\n", hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines)
		for _, line := range hunk.Lines {
			builder.WriteString(line + "\n")
		}
	}
	return builder.String()
}

// claudeText flattens a tool result's content: a string, or text blocks.
func claudeText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []claudeBlock
	_ = json.Unmarshal(content, &blocks)
	var parts []string
	for _, claudeBlockValue := range blocks {
		if claudeBlockValue.Type == "text" {
			parts = append(parts, claudeBlockValue.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// claudePrompt is a user message in Claude Code's input format.
func claudePrompt(messageValue Message, preface string) string {
	content := []any{}
	if text := preface + messageValue.Content; text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, imagePart := range messageValue.Images {
		content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": imagePart.Mime, "data": imagePart.Data}})
	}
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	return string(line)
}

// claudeApproval words a permission request like 1loop's own tools do.
func claudeApproval(root, name string, input json.RawMessage) (title, preview string) {
	tool, args := claudeTool(root, name, input)
	path, command, _ := ToolArgs(args)
	switch tool {
	case "shell":
		return "Run command?", root + "\n\n$ " + command
	case "edit_file":
		var in struct {
			Old string `json:"old_text"`
			New string `json:"new_text"`
		}
		_ = json.Unmarshal([]byte(args), &in)
		var builder strings.Builder
		fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n", path, path)
		for _, line := range strings.Split(in.Old, "\n") {
			if in.Old != "" {
				builder.WriteString("-" + line + "\n")
			}
		}
		for _, line := range strings.Split(in.New, "\n") {
			builder.WriteString("+" + line + "\n")
		}
		return "Apply changes to " + path + "?", builder.String()
	}
	var pretty map[string]any
	_ = json.Unmarshal(input, &pretty)
	return "Allow " + name + "?", JSONText(pretty)
}

// runClaudeCode runs one task in Claude Code: the last message of history
// is the prompt, sent in the chat's Claude Code session (cfg.Session) when
// it has one. Steered messages are written to Claude Code while it works,
// which folds them into the turn; queued ones start the next turn.
func (backend *App) runClaudeCode(ctx context.Context, cfg Config, history []Message, inboxValue Inbox, emit emitter) error {
	if len(history) == 0 {
		return fmt.Errorf("Nothing to send")
	}
	ctx = toolContext(ctx, cfg)
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		cfg.ProjectTools = func(operation context.Context, name string, raw json.RawMessage) (PanelToolResult, error) {
			// Claude's permission handler has already approved this MCP call.
			operation = context.WithValue(operation, approvalContextKey{}, approvalPolicy{yolo: true})
			call := ToolCall{ID: core.Identifier()}
			call.Function.Name, call.Function.Arguments = name, string(raw)
			outcome := backend.executeTool(operation, cfg.Workspace, call, func(event string, value any) {
				if event != "tool" && event != "tool_exec" && event != "tool_output" {
					emit(event, value)
				}
			})
			return PanelToolResult{Text: outcome.Output, Images: outcome.Images}, outcome.Err
		}
	}
	args := []string{"--include-partial-messages", "--permission-prompt-tool", "stdio"}
	if cfg.ApprovalMode == "auto" {
		// Claude Code's own judge decides; what it still asks reaches the
		// user like any other permission request.
		args = append(args, "--permission-mode", "auto")
	}
	if claudeDisabledTools != "" {
		args = append(args, "--disallowedTools", claudeDisabledTools)
	}
	if cfg.Model != "" && cfg.Model != "default" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Effort != "" {
		args = append(args, "--effort", cfg.Effort)
	}
	if cfg.Session != "" {
		args = append(args, "--resume", cfg.Session)
	}
	if cfg.Todo != nil {
		config, close, err := backend.startTodoMCP(ctx, cfg, emit)
		if err != nil {
			return err
		}
		defer close()
		args = append(args, "--mcp-config", config)
	}
	// The user's MCP servers, plugins and skills, for this session only.
	extensionArgs, cleanExtensions := claudeExtensionArgs()
	defer cleanExtensions()
	args = append(args, extensionArgs...)
	appendPrompt := mobileInstructions(cfg) + projectInstructions(cfg.Workspace) + citationInstructions + todoInstructions(cfg) + todoSnapshot(cfg) + memoryInstructions(cfg)
	if cfg.ProjectTools != nil {
		appendPrompt += "\n\nUse 1loop's MCP shell, read_file, list_directory, edit_file and apply_patch tools for project work. Follow the phone's selected execution policy and stay strictly inside the requested project. Use recall to retrieve older conversation details. Your own context compaction remains managed by Claude Code."
	}
	for _, group := range builtinToolGroups(cfg) {
		appendPrompt += group.instructions(cfg)
	}
	if strings.TrimSpace(cfg.UserInstructions) != "" {
		appendPrompt = userPreferenceInstructions(cfg) + appendPrompt
	}
	if appendPrompt != "" {
		args = append(args, "--append-system-prompt", appendPrompt)
	}
	cmd, err := claudeCommand(ctx, cfg.Workspace, args...)
	if err != nil {
		return err
	}
	if cfg.CompactAt > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("CLAUDE_CODE_AUTO_COMPACT_WINDOW=%d", cfg.CompactAt))
	}
	logged := append([]string(nil), cmd.Args...)
	for i := 1; i < len(logged); i++ {
		if logged[i-1] == "--mcp-config" {
			logged[i] = "[1loop MCP config]"
		}
	}
	platform.Devf("claude", "start in %s: %s", cfg.Workspace, strings.Join(logged, " "))
	defer func() { platform.Devf("claude", "process ended: %v", cmd.ProcessState) }()
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr outputBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	var writing sync.Mutex
	open := true
	send := func(line string) {
		writing.Lock()
		defer writing.Unlock()
		if open {
			io.WriteString(stdin, line+"\n")
		}
	}
	finish := func() { // no more input: Claude Code exits after its last turn
		writing.Lock()
		defer writing.Unlock()
		if open {
			open = false
			stdin.Close()
		}
	}
	var failure error
	defer func() {
		finish()
		if ctx.Err() != nil || failure != nil {
			// Stopped: nothing Claude Code started may keep running (its
			// commands and background tasks share its process group).
			platform.KillProcessTree(cmd)
		}
		cmd.Wait()
	}()

	messages := append([]Message(nil), history...)
	prompt := messages[len(messages)-1]
	preface := ""
	// The recap takes at most ~40% of Claude's window (about 4 characters a
	// token): a 1M window receives nearly the whole conversation.
	budget := ModelContextWindow(cfg) * 4 * 40 / 100
	if cfg.Session == "" && len(messages) > 1 {
		// A chat begun with another model: Claude Code starts a session
		// of its own, so it is told what was said before.
		preface = "[Earlier conversation in this chat, for context]\n" + core.TruncateText(levelText(messages[:len(messages)-1], LevelEverything, ModelContextWindow(cfg)), budget) + "\n[End of the earlier conversation]\n\n"
	} else if cfg.Session != "" && cfg.HandoffSummary != "" {
		// Back after another model, the gap summarized as the user chose.
		preface = "[Since your last turn in this chat, the user continued with another model. Summary of what happened meanwhile]\n" + cfg.HandoffSummary + "\n[End of the summary]\n\n"
	} else if cfg.Session != "" && cfg.SessionMark > 0 && cfg.Recall != nil {
		// Back to Claude Code after another model: its session misses
		// what happened meanwhile, which it now reads before the prompt.
		if items, err := cfg.Recall(ctx); err == nil {
			end := len(items)
			if end > 0 && items[end-1].Kind == "user" {
				end-- // the prompt itself
			}
			if cfg.SessionMark < end {
				preface = "[Since your last turn in this chat, the user continued with another model. What happened meanwhile]\n" + itemsText(items[cfg.SessionMark:end]) + "\n[End of what happened meanwhile]\n\n"
			}
		}
	}
	// The host then records how many of the chat's items the session has seen.
	defer func() {
		if failure == nil && ctx.Err() == nil {
			emit("session_synced", nil)
		}
	}()
	emit("activity", Activity{State: "wait"})
	send(claudePrompt(prompt, preface))

	type pending struct {
		outcome ToolOutcome
		started time.Time
	}
	calls := map[string]*pending{}   // tool calls without a result yet
	streaming := map[int]*ToolCall{} // tool calls being written, by block index
	sawText := false                 // the current text arrived as deltas
	status := &announcer{emit: emit}
	usage := 0
	// Between two turns Claude Code is idle: either the task is over, or it
	// waits for what it left running in the background (a command started
	// with run_in_background, a Monitor) and will start a turn of its own
	// when that reports. Its input stays open meanwhile, so that turn can
	// still ask for a permission and the user can still send a message.
	idle := false
	var idleSince time.Time
	var background []string // what Claude Code runs in the background
	waitBackground := func() {
		switch len(background) {
		case 0:
		case 1:
			status.set(Activity{State: "background", Detail: background[0]})
		default:
			status.set(Activity{State: "background", Detail: fmt.Sprintf("%d background tasks", len(background))})
		}
	}
	inject := func(messageValue Message) {
		idle = false
		messages = append(messages, messageValue)
		emit("injected", messageValue)
		status.set(Activity{State: "wait"})
		send(claudePrompt(messageValue, ""))
	}

	// The output is read apart, so the loop below can also watch the clock
	// (an idle Claude Code says nothing) and the Stop button.
	lines := make(chan []byte)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 64<<20)
		for scanner.Scan() {
			select {
			case lines <- append([]byte(nil), scanner.Bytes()...):
			case <-done:
				return
			}
		}
	}()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for failure == nil {
		var line []byte
		select {
		case <-cfg.CapabilitiesChanged:
			request, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": core.Identifier(), "request": map[string]string{"subtype": "mcp_reconnect", "serverName": "1loop_todo"}})
			send(string(request))
			continue
		case <-ctx.Done():
			platform.Devf("claude", "stop asked: interrupt sent, waiting for it to close its session")
			// Stop: Claude Code got the interrupt it documents (SIGINT);
			// it has a moment to close its session before it is killed.
			grace := time.After(1500 * time.Millisecond)
			for {
				select {
				case _, more := <-lines:
					if more {
						continue
					}
				case <-grace:
				}
				return ctx.Err()
			}
		case <-tick.C:
			if extras := inboxValue.Steer(); len(extras) > 0 {
				for _, extra := range extras {
					inject(extra)
				}
			} else if !idle {
				continue
			} else if next, ok := inboxValue.Next(); ok {
				inject(next)
			} else if len(background) == 0 && time.Since(idleSince) > 600*time.Millisecond {
				idle = false
				platform.Devf("claude", "nothing runs, nothing waits: closing its input")
				finish() // nothing runs, nothing waits: the task is over
			}
			continue
		case next, more := <-lines:
			if !more {
				lines = nil
			}
			line = next
		}
		if lines == nil {
			break
		}
		var claudeEventValue claudeEvent
		if json.Unmarshal(line, &claudeEventValue) != nil {
			platform.Devf("claude", "unreadable line: %s", platform.DevText(string(line), 160))
			continue
		}
		if platform.Dev != nil && claudeEventValue.Type != "stream_event" {
			platform.Devf("claude", "%s %s, %d bytes, idle %v, background tasks %d", claudeEventValue.Type, claudeEventValue.Subtype, len(line), idle, len(background))
		}
		if claudeEventValue.Parent != nil && claudeEventValue.Type != "result" {
			continue // a subagent's own stream: its outcome comes as the Agent tool's result
		}
		switch claudeEventValue.Type {
		case "stream_event", "assistant", "user", "control_request":
			idle = false // a turn is running
		}
		switch claudeEventValue.Type {
		case "system":
			switch claudeEventValue.Subtype {
			case "compact_boundary":
				// Claude owns compaction; send the live task position through its
				// supported input stream without replacing its session context.
				if reminder := todoResumeReminder(cfg); reminder != "" {
					message := Message{Role: "user", Internal: true, Content: reminder}
					messages = append(messages, message)
					send(claudePrompt(message, ""))
					emit("context_note", "Context compacted · "+todoCurrentStep(cfg))
				}
			case "background_tasks_changed":
				background = background[:0]
				for _, task := range claudeEventValue.Tasks {
					background = append(background, task.Description)
				}
				if idle {
					idleSince = time.Now() // a finished task reports: its turn comes next
					waitBackground()
				}
			case "init":
				if claudeEventValue.APIKeySource != "" && claudeEventValue.APIKeySource != "none" {
					failure = fmt.Errorf("Claude Code is not using your subscription here (credentials: %s). 1loop stopped before anything was billed to an API key.", claudeEventValue.APIKeySource)
				}
				if claudeEventValue.SessionID != "" {
					emit("session", claudeEventValue.SessionID)
				}
			case "status":
				if claudeEventValue.Status == "requesting" {
					idle = false
					status.set(Activity{State: "wait"})
				}
			case "api_retry":
				emit("notice", "Claude Code is retrying the request…")
			}
		case "rate_limit_event":
			if claudeEventValue.RateLimit != nil {
				if claudeEventValue.RateLimit.IsUsingOverage {
					failure = fmt.Errorf("Claude Code started drawing on extra usage, beyond your plan's limits. 1loop stopped the task.")
				} else if claudeEventValue.RateLimit.Status == "rejected" {
					var reset time.Time
					if at := claudeEventValue.RateLimit.ResetsAt; at > 0 {
						if at > 100000000000 {
							reset = time.UnixMilli(at)
						} else {
							reset = time.Unix(at, 0)
						}
					}
					failure = &RateLimitError{Cause: fmt.Errorf("Claude Code reached a usage limit."), Reset: reset}
				}
				emit("plan", PlanUsage{FiveHour: claudeEventValue.RateLimit.Windows["five_hour"].Utilization, Week: claudeEventValue.RateLimit.Windows["seven_day"].Utilization})
			}
		case "stream_event":
			if claudeEventValue.Event == nil {
				break
			}
			switch claudeEventValue.Event.Type {
			case "content_block_start":
				switch claudeEventValue.Event.ContentBlock.Type {
				case "thinking":
					status.set(Activity{State: "think"})
				case "tool_use":
					call := &ToolCall{ID: claudeEventValue.Event.ContentBlock.ID}
					call.Function.Name = claudeEventValue.Event.ContentBlock.Name
					streaming[claudeEventValue.Event.Index] = call
					tool, _ := claudeTool(cfg.Workspace, call.Function.Name, nil)
					status.set(Activity{State: "call", Tool: tool})
				}
			case "content_block_delta":
				switch claudeEventValue.Event.Delta.Type {
				case "thinking_delta":
					status.set(Activity{State: "think"})
				case "text_delta":
					if claudeEventValue.Event.Delta.Text != "" {
						sawText = true
						status.last = Activity{State: "write"} // the delta itself tells the interface
						emit("delta", claudeEventValue.Event.Delta.Text)
					}
				case "input_json_delta":
					if call := streaming[claudeEventValue.Event.Index]; call != nil && status.last.Detail == "" {
						call.Function.Arguments += claudeEventValue.Event.Delta.PartialJSON
						tool, _ := claudeTool(cfg.Workspace, call.Function.Name, nil)
						status.set(Activity{State: "call", Tool: tool, Detail: claudeTarget(cfg.Workspace, call.Function.Arguments)})
					}
				}
			case "message_start":
				streaming = map[int]*ToolCall{}
			}
		case "assistant":
			if claudeEventValue.Message == nil {
				break
			}
			if n := claudeEventValue.Message.Usage.Input + claudeEventValue.Message.Usage.CacheCreation + claudeEventValue.Message.Usage.CacheRead; n > 0 {
				usage = n
				emit("usage", n)
			}
			var blocks []claudeBlock
			_ = json.Unmarshal(claudeEventValue.Message.Content, &blocks)
			for _, claudeBlockValue := range blocks {
				switch claudeBlockValue.Type {
				case "text":
					if !sawText && claudeBlockValue.Text != "" { // no partial messages: show it whole
						emit("delta", claudeBlockValue.Text)
					}
					sawText = false
					messages = append(messages, Message{Role: "assistant", Content: claudeBlockValue.Text, Usage: usage})
					emit("message_end", nil)
				case "tool_use":
					tool, arguments := claudeTool(cfg.Workspace, claudeBlockValue.Name, claudeBlockValue.Input)
					pendingValue := &pending{outcome: ToolOutcome{ID: claudeBlockValue.ID, Name: tool, Args: arguments}, started: time.Now()}
					calls[claudeBlockValue.ID] = pendingValue
					emit("tool", pendingValue.outcome)
					emit("tool_exec", pendingValue.outcome)
				}
			}
		case "control_request":
			if claudeEventValue.Request == nil || claudeEventValue.Request.Subtype != "can_use_tool" {
				break
			}
			title, preview := claudeApproval(cfg.Workspace, claudeEventValue.Request.ToolName, claudeEventValue.Request.Input)
			response := map[string]any{"behavior": "deny", "message": "The user denied this action."}
			name := strings.TrimPrefix(claudeEventValue.Request.ToolName, "mcp__1loop_todo__")
			projectRead := cfg.ProjectTools != nil && (name == "read_file" || name == "list_directory" || name == "recall" || name == "request_user_input")
			if projectRead || claudeEventValue.Request.ToolName == "mcp__1loop_todo__update_todo" || claudeEventValue.Request.ToolName == "mcp__1loop_todo__memory" || (claudeEventValue.Request.ToolName == "mcp__1loop_todo__computer_use" && !computerNeedsApproval(claudeEventValue.Request.Input)) || strings.HasPrefix(claudeEventValue.Request.ToolName, "mcp__1loop_todo__browser_") || (strings.HasPrefix(claudeEventValue.Request.ToolName, "mcp__1loop_todo__terminal_") && !terminalNeedsApproval(strings.TrimPrefix(claudeEventValue.Request.ToolName, "mcp__1loop_todo__"), claudeEventValue.Request.Input)) || backend.confirm(ctx, emit, title, preview) {
				response = map[string]any{"behavior": "allow", "updatedInput": claudeEventValue.Request.Input}
				for _, p := range calls { // the approved call starts now
					p.started = time.Now()
					emit("tool_exec", p.outcome)
				}
			}
			line, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
				"subtype": "success", "request_id": claudeEventValue.RequestID, "response": response}})
			send(string(line))
		case "user":
			if claudeEventValue.Message == nil {
				break
			}
			var blocks []claudeBlock
			_ = json.Unmarshal(claudeEventValue.Message.Content, &blocks)
			for _, b := range blocks {
				p := calls[b.ToolUseID]
				if b.Type != "tool_result" || p == nil {
					continue
				}
				delete(calls, b.ToolUseID)
				p.outcome.Output = claudeText(b.Content)
				if strings.HasPrefix(p.outcome.Name, "browser_") || strings.HasPrefix(p.outcome.Name, "terminal_") || p.outcome.Name == "computer_use" {
					p.outcome.Images = claudeBrowserImages(b.Content)
				}
				p.outcome.Elapsed = time.Since(p.started)
				if b.IsError {
					p.outcome.Err = errors.New(FirstLine(p.outcome.Output))
				} else if p.outcome.Name == "edit_file" {
					path, _, _ := ToolArgs(p.outcome.Args)
					p.outcome.Diff = claudeDiff(path, claudeEventValue.ToolUseResult)
				} else if p.outcome.Name == "memory" {
					p.outcome.Diff = memoryDiff(p.outcome.Output)
				}
				// Kept in the chat's history as a call and its result, so a
				// model that takes over knows what Claude Code did.
				call := ToolCall{ID: b.ToolUseID, Type: "function"}
				call.Function.Name, call.Function.Arguments = p.outcome.Name, p.outcome.Args
				messages = append(messages, Message{Role: "assistant", ToolCalls: []ToolCall{call}},
					Message{Role: "tool", ToolCallID: b.ToolUseID, Content: p.outcome.modelText(), Images: p.outcome.Images})
				emit("tool_result", p.outcome)
			}
			// The gap between two tool calls: steered messages go in now.
			for _, extra := range inboxValue.Steer() {
				inject(extra)
			}
		case "result":
			// Claude Code says how large the model's window really is.
			for model, u := range claudeEventValue.ModelUsage {
				if u.ContextWindow > 0 && (len(claudeEventValue.ModelUsage) == 1 || strings.Contains(model, cfg.Model)) {
					learnWindow(cfg.ConnectionID, cfg.Model, u.ContextWindow)
				}
			}
			if claudeEventValue.IsError && failure == nil {
				text := claudeEventValue.Result
				if text == "" {
					text = "Claude Code ended the turn with an error (" + claudeEventValue.Subtype + ")"
				}
				failure = errors.New(text)
				if limited, ok := claudeLimit(claudeEventValue.APIError, text); ok {
					failure = &RateLimitError{Cause: failure, Reset: limited}
				}
			}
			emit("history", append([]Message(nil), messages...))
			if failure != nil || claudeEventValue.QueuedTurns > 0 {
				break // more turns are already queued in Claude Code
			}
			if extras := inboxValue.Steer(); len(extras) > 0 {
				for _, extra := range extras {
					inject(extra)
				}
			} else if next, ok := inboxValue.Next(); ok {
				inject(next)
			} else {
				idle, idleSince = true, time.Now()
				waitBackground()
			}
		}
	}
	if failure != nil {
		emit("history", append([]Message(nil), messages...))
		var limited *RateLimitError
		if len(calls) > 0 && errors.As(failure, &limited) {
			return fmt.Errorf("Claude Code reached a limit while a tool result was pending; automatic resumption is unsafe. Review the conversation before continuing.")
		}
		return failure // the deferred kill stops it now: nothing more may be spent
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(messages) == len(history) {
		// Claude Code left without answering: its own words explain why.
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return errors.New(FirstLine(text))
		}
		return fmt.Errorf("Claude Code stopped without answering")
	}
	return nil
}

func claudeBrowserImages(raw json.RawMessage) []core.ImagePart {
	var blocks []struct {
		Type, Data string
		Mime       string `json:"mimeType"`
		Source     struct {
			MediaType string `json:"media_type"`
			Data      string
		}
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var images []core.ImagePart
	for _, b := range blocks {
		if b.Type != "image" {
			continue
		}
		if b.Source.Data != "" {
			images = append(images, core.ImagePart{Mime: b.Source.MediaType, Data: b.Source.Data})
		} else if b.Data != "" {
			images = append(images, core.ImagePart{Mime: b.Mime, Data: b.Data})
		}
	}
	return images
}

// itemsText is what a chat shows, as text for a model: what Claude Code
// reads of the turns other models took while it was away.
func itemsText(items []ChatItem) string {
	var builder strings.Builder
	for _, item := range items {
		switch item.Kind {
		case "user":
			text := item.Text
			if len(item.Images) > 0 {
				text += fmt.Sprintf(" [+%d image(s) attached]", len(item.Images))
			}
			builder.WriteString("USER: " + text + "\n\n")
		case "assistant":
			if item.Text != "" {
				builder.WriteString("ASSISTANT: " + item.Text + "\n\n")
			}
		case "tool":
			builder.WriteString(fmt.Sprintf("TOOL %s(%s)\n", item.Tool, item.Args))
			if item.Failed != "" {
				builder.WriteString("FAILED: " + item.Failed + "\n\n")
			} else {
				builder.WriteString("RESULT: " + item.Text + "\n\n")
			}
		case "note":
			builder.WriteString("NOTE: " + item.Text + "\n\n")
		}
	}
	return builder.String()
}
