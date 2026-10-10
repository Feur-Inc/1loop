package main

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
	"strings"
	"sync"
	"syscall"
	"time"
)

// claudeEvent is one line of Claude Code's stream-json output; only the
// fields 1loop reads are declared.
type claudeEvent struct {
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	SessionID    string  `json:"session_id"`
	Parent       *string `json:"parent_tool_use_id"` // set for a subagent's messages
	APIKeySource string  `json:"apiKeySource"`
	Status       string  `json:"status"`
	Event        *struct {
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
	Description string   `json:"description"`
	Efforts     []string `json:"supportedEffortLevels"`
}

// planUsage is how much of the subscription's windows is used (0..1).
type planUsage struct {
	FiveHour, Week float64
}

// claudeCommand prepares a Claude Code process in its stream-json mode.
// The environment is the user's, minus what would change who pays (an API
// key) and minus the variables of a Claude Code session 1loop may have been
// started from.
func claudeCommand(ctx context.Context, dir string, extra ...string) (*exec.Cmd, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("Claude Code is not installed: no claude command was found")
	}
	args := append([]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, extra...)
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
	// Stop ends the turn the way Claude Code documents (SIGINT), then the
	// whole process group if it lingers.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}

// claudeModels asks the installed Claude Code which models the account can
// use. It only exchanges the protocol's handshake: no request reaches a model.
func (a *app) claudeModels(ctx context.Context) ([]claudeModel, error) {
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
		var e claudeEvent
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Type == "control_response" && e.Response != nil {
			if len(e.Response.Response.Models) == 0 {
				return nil, fmt.Errorf("Claude Code listed no model: check that it is signed in (run claude once)")
			}
			return e.Response.Response.Models, nil
		}
		if e.Type == "result" && e.IsError {
			return nil, errors.New(firstLine(e.Result))
		}
	}
	return nil, fmt.Errorf("Claude Code did not answer: run claude once in a terminal to check it works")
}

// claudeTool translates a Claude Code tool call into the row 1loop shows:
// commands, reads and edits use 1loop's own rows (and their detail panel);
// every other tool keeps its name and arguments.
func claudeTool(root, name string, input json.RawMessage) (tool, args string) {
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
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	if len(r.Patch) == 0 {
		if r.Type != "create" || r.Content == "" {
			return ""
		}
		lines := strings.Split(strings.TrimRight(r.Content, "\n"), "\n")
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
		for _, line := range lines {
			b.WriteString("+" + line + "\n")
		}
		return b.String()
	}
	for _, hunk := range r.Patch {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines)
		for _, line := range hunk.Lines {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
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
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// claudePrompt is a user message in Claude Code's input format.
func claudePrompt(m message, preface string) string {
	content := []any{}
	if text := preface + m.Content; text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, im := range m.Images {
		content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": im.Mime, "data": im.Data}})
	}
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
	return string(line)
}

// claudeApproval words a permission request like 1loop's own tools do.
func claudeApproval(root, name string, input json.RawMessage) (title, preview string) {
	tool, args := claudeTool(root, name, input)
	path, command, _ := toolArgs(args)
	switch tool {
	case "shell":
		return "Run command?", root + "\n\n$ " + command
	case "edit_file":
		var in struct {
			Old string `json:"old_text"`
			New string `json:"new_text"`
		}
		_ = json.Unmarshal([]byte(args), &in)
		var b strings.Builder
		fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
		for _, line := range strings.Split(in.Old, "\n") {
			if in.Old != "" {
				b.WriteString("-" + line + "\n")
			}
		}
		for _, line := range strings.Split(in.New, "\n") {
			b.WriteString("+" + line + "\n")
		}
		return "Apply changes to " + path + "?", b.String()
	}
	var pretty map[string]any
	_ = json.Unmarshal(input, &pretty)
	return "Allow " + name + "?", jsonText(pretty)
}

// runClaudeCode runs one task in Claude Code: the last message of history
// is the prompt, sent in the chat's Claude Code session (cfg.Session) when
// it has one. Steered messages are written to Claude Code while it works,
// which folds them into the turn; queued ones start the next turn.
func (a *app) runClaudeCode(ctx context.Context, cfg config, history []message, in inbox, emit emitter) error {
	if len(history) == 0 {
		return fmt.Errorf("Nothing to send")
	}
	ctx = toolContext(ctx, cfg)
	args := []string{"--include-partial-messages", "--permission-prompt-tool", "stdio"}
	if cfg.Model != "" && cfg.Model != "default" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Effort != "" {
		args = append(args, "--effort", cfg.Effort)
	}
	if cfg.Session != "" {
		args = append(args, "--resume", cfg.Session)
	}
	if strings.TrimSpace(cfg.UserInstructions) != "" {
		args = append(args, "--append-system-prompt", "Additional instructions from the user:\n"+cfg.UserInstructions)
	}
	cmd, err := claudeCommand(ctx, cfg.Workspace, args...)
	if err != nil {
		return err
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr limitedBuffer
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
	defer func() { finish(); cmd.Wait() }() // Wait also ends a process that outlives a stop

	messages := append([]message(nil), history...)
	prompt := messages[len(messages)-1]
	preface := ""
	if cfg.Session == "" && len(messages) > 1 {
		// A chat begun with another model: Claude Code starts a session
		// of its own, so it is told what was said before.
		preface = "[Earlier conversation in this chat, for context]\n" + truncateText(conversationText(messages[:len(messages)-1]), 24000) + "\n[End of the earlier conversation]\n\n"
	}
	emit("activity", activity{State: "wait"})
	send(claudePrompt(prompt, preface))

	type pending struct {
		outcome toolOutcome
		started time.Time
	}
	calls := map[string]*pending{}   // tool calls without a result yet
	streaming := map[int]*toolCall{} // tool calls being written, by block index
	sawText := false                 // the current text arrived as deltas
	status := &announcer{emit: emit}
	usage := 0
	var failure error
	inject := func(m message) {
		messages = append(messages, m)
		emit("injected", m)
		send(claudePrompt(m, ""))
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	for scanner.Scan() {
		var e claudeEvent
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Parent != nil && e.Type != "result" {
			continue // a subagent's own stream: its outcome comes as the Agent tool's result
		}
		switch e.Type {
		case "system":
			switch e.Subtype {
			case "init":
				if e.APIKeySource != "" && e.APIKeySource != "none" {
					failure = fmt.Errorf("Claude Code is not using your subscription here (credentials: %s). 1loop stopped before anything was billed to an API key.", e.APIKeySource)
				}
				if e.SessionID != "" {
					emit("session", e.SessionID)
				}
			case "status":
				if e.Status == "requesting" {
					status.set(activity{State: "wait"})
				}
			case "api_retry":
				emit("notice", "Claude Code is retrying the request…")
			}
		case "rate_limit_event":
			if e.RateLimit != nil {
				if e.RateLimit.IsUsingOverage {
					failure = fmt.Errorf("Claude Code started drawing on extra usage, beyond your plan's limits. 1loop stopped the task.")
				}
				emit("plan", planUsage{FiveHour: e.RateLimit.Windows["five_hour"].Utilization, Week: e.RateLimit.Windows["seven_day"].Utilization})
			}
		case "stream_event":
			if e.Event == nil {
				break
			}
			switch e.Event.Type {
			case "content_block_start":
				switch e.Event.ContentBlock.Type {
				case "thinking":
					status.set(activity{State: "think"})
				case "tool_use":
					call := &toolCall{ID: e.Event.ContentBlock.ID}
					call.Function.Name = e.Event.ContentBlock.Name
					streaming[e.Event.Index] = call
					tool, _ := claudeTool(cfg.Workspace, call.Function.Name, nil)
					status.set(activity{State: "call", Tool: tool})
				}
			case "content_block_delta":
				switch e.Event.Delta.Type {
				case "thinking_delta":
					status.set(activity{State: "think"})
				case "text_delta":
					if e.Event.Delta.Text != "" {
						sawText = true
						status.last = activity{State: "write"} // the delta itself tells the interface
						emit("delta", e.Event.Delta.Text)
					}
				case "input_json_delta":
					if call := streaming[e.Event.Index]; call != nil && status.last.Detail == "" {
						call.Function.Arguments += e.Event.Delta.PartialJSON
						tool, _ := claudeTool(cfg.Workspace, call.Function.Name, nil)
						status.set(activity{State: "call", Tool: tool, Detail: claudeTarget(cfg.Workspace, call.Function.Arguments)})
					}
				}
			case "message_start":
				streaming = map[int]*toolCall{}
			}
		case "assistant":
			if e.Message == nil {
				break
			}
			if n := e.Message.Usage.Input + e.Message.Usage.CacheCreation + e.Message.Usage.CacheRead; n > 0 {
				usage = n
				emit("usage", n)
			}
			var blocks []claudeBlock
			_ = json.Unmarshal(e.Message.Content, &blocks)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if !sawText && b.Text != "" { // no partial messages: show it whole
						emit("delta", b.Text)
					}
					sawText = false
					messages = append(messages, message{Role: "assistant", Content: b.Text, Usage: usage})
					emit("message_end", nil)
				case "tool_use":
					tool, arguments := claudeTool(cfg.Workspace, b.Name, b.Input)
					p := &pending{outcome: toolOutcome{ID: b.ID, Name: tool, Args: arguments}, started: time.Now()}
					calls[b.ID] = p
					emit("tool", p.outcome)
					emit("tool_exec", p.outcome)
				}
			}
		case "control_request":
			if e.Request == nil || e.Request.Subtype != "can_use_tool" {
				break
			}
			title, preview := claudeApproval(cfg.Workspace, e.Request.ToolName, e.Request.Input)
			response := map[string]any{"behavior": "deny", "message": "The user denied this action."}
			if a.confirm(ctx, emit, title, preview) {
				response = map[string]any{"behavior": "allow", "updatedInput": e.Request.Input}
				for _, p := range calls { // the approved call starts now
					p.started = time.Now()
					emit("tool_exec", p.outcome)
				}
			}
			line, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
				"subtype": "success", "request_id": e.RequestID, "response": response}})
			send(string(line))
		case "user":
			if e.Message == nil {
				break
			}
			var blocks []claudeBlock
			_ = json.Unmarshal(e.Message.Content, &blocks)
			for _, b := range blocks {
				p := calls[b.ToolUseID]
				if b.Type != "tool_result" || p == nil {
					continue
				}
				delete(calls, b.ToolUseID)
				p.outcome.Output = claudeText(b.Content)
				p.outcome.Elapsed = time.Since(p.started)
				if b.IsError {
					p.outcome.Err = errors.New(firstLine(p.outcome.Output))
				} else if p.outcome.Name == "edit_file" {
					path, _, _ := toolArgs(p.outcome.Args)
					p.outcome.Diff = claudeDiff(path, e.ToolUseResult)
				}
				emit("tool_result", p.outcome)
			}
			// The gap between two tool calls: steered messages go in now.
			for _, extra := range in.steer() {
				inject(extra)
			}
		case "result":
			// Claude Code says how large the model's window really is.
			for model, u := range e.ModelUsage {
				if u.ContextWindow > 0 && (len(e.ModelUsage) == 1 || strings.Contains(model, cfg.Model)) {
					learnedMu.Lock()
					learnedWindows[cfg.ConnectionID+"|"+cfg.Model] = u.ContextWindow
					learnedMu.Unlock()
				}
			}
			if e.IsError && failure == nil {
				text := e.Result
				if text == "" {
					text = "Claude Code ended the turn with an error (" + e.Subtype + ")"
				}
				failure = errors.New(text)
			}
			emit("history", append([]message(nil), messages...))
			if failure != nil || e.QueuedTurns > 0 {
				break // more turns are already queued in Claude Code
			}
			if extras := in.steer(); len(extras) > 0 {
				for _, extra := range extras {
					inject(extra)
				}
			} else if next, ok := in.next(); ok {
				inject(next)
			} else {
				finish()
			}
		}
		if failure != nil {
			break
		}
	}
	if failure != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // stop it now: nothing more may be spent
		return failure
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(messages) == len(history) {
		// Claude Code left without answering: its own words explain why.
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return errors.New(firstLine(text))
		}
		return fmt.Errorf("Claude Code stopped without answering")
	}
	return nil
}
