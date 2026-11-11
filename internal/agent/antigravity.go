package agent

// Antigravity connection. As with Claude Code (claudecode.go), 1loop does not
// replace the harness: it starts the user's own Antigravity CLI, installed on
// this computer and unmodified, in its documented headless mode
//
//	agy --input-format stream-json --output-format stream-json -p=
//
// and only displays and drives it. agy keeps its own Google sign-in, tools,
// settings and MCP servers. 1loop never reads or reuses its credentials,
// never calls Antigravity's backend itself, and runs it only for the person
// at this computer: Google describes local, single-user headless use of the
// official binary as supported, while wrapping agy as a service for others
// is not (consumer plans).
//
// Headless agy cannot ask for a permission: an action that needs one (a
// shell command, by default) is refused and listed in the turn's result.
// 1loop then asks the user, and for what they approve starts agy again on
// the same conversation with an allow rule for that exact action, passed in
// --settings for that run only (the user's settings.json is not changed).

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"1loop/internal/core"
	"1loop/internal/platform"
)

// agySessionPrefix marks an Antigravity conversation in Config.Session, the
// field a Claude Code chat keeps its session in: a chat that goes from one
// to the other never resumes the wrong one.
const agySessionPrefix = "agy:"

// HarnessSession is the chat's session in the connection's own harness:
// its Claude Code session or its Antigravity conversation, "" when it has
// none there.
func HarnessSession(cfg Config) string {
	switch cfg.Provider {
	case "claudecode":
		if strings.HasPrefix(cfg.Session, agySessionPrefix) {
			return ""
		}
		return cfg.ClaudeSession()
	case "antigravity":
		if id, found := strings.CutPrefix(cfg.Session, agySessionPrefix); found {
			return id
		}
	}
	return ""
}

// HarnessMark is how many of the chat's items that session has seen.
func HarnessMark(cfg Config) int {
	if cfg.Provider == "claudecode" {
		return cfg.ClaudeMark()
	}
	return cfg.SessionMark
}

// CLIHarness says whether a provider runs in a coding agent's own harness
// (Claude Code, Antigravity) rather than in 1loop's agent loop: the CLI keeps
// the session, its tools and its context compaction.
func CLIHarness(provider string) bool {
	return provider == "claudecode" || provider == "antigravity"
}

// agyHost is the Flatpak's launcher for the host's agy (host-run linked as agy).
const agyHost = "/app/lib/1loop/host/agy"

// AgyExecutable is the agy command: on PATH, else in ~/.local/bin. In the
// Flatpak it is the host's, started through host-run.
func AgyExecutable() (string, error) {
	if os.Getenv("ONELOOP_FLATPAK") != "" {
		if exec.Command("/app/lib/1loop/host/host-run", "sh", "-c", "command -v agy >/dev/null").Run() == nil {
			return agyHost, nil
		}
		return "", fmt.Errorf("Antigravity CLI is not installed on the system: no agy command was found on the host")
	}
	if path, err := exec.LookPath("agy"); err == nil {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".local", "bin", "agy")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("Antigravity CLI is not installed: no agy command was found")
}

// AgyTerms is what the connection's settings page and the docs say about
// how 1loop uses agy.
const AgyTerms = "1loop starts the Antigravity CLI installed on this computer, unmodified, in its documented headless mode, for you only. It never reads or reuses agy's sign-in, never contacts Antigravity's servers itself and never shares agy with other people. Google's forum answers describe this local, single-user use as supported; its FAQ says wrapping agy is not supported on consumer plans, so check that your plan allows it."

// agyCommand prepares an agy process in its stream-json mode.
func agyCommand(ctx context.Context, dir string, extra ...string) (*exec.Cmd, error) {
	// -p= : a bare -p would take the next flag as the prompt.
	return agyProcess(ctx, dir, append([]string{"--input-format", "stream-json", "--output-format", "stream-json", "-p="}, extra...)...)
}

// agyProcess prepares an agy process. The environment is the user's minus
// API keys, so the run uses the account agy is signed in with.
func agyProcess(ctx context.Context, dir string, args ...string) (*exec.Cmd, error) {
	path, err := AgyExecutable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	for _, pair := range os.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		if name == "GEMINI_API_KEY" || name == "GOOGLE_API_KEY" {
			continue
		}
		cmd.Env = append(cmd.Env, pair)
	}
	platform.PrepareProcess(cmd)
	cmd.Cancel = func() error { return platform.InterruptProcess(cmd) }
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}

// agyLimits asks agy itself for its usage limits: /usage in print mode is
// answered by agy without a model turn (no tokens). 1loop still never reads
// agy's sign-in.
func agyLimits(ctx context.Context) (AccountLimits, error) {
	limits := AccountLimits{Note: "Limits not provided"}
	home, err := os.UserHomeDir()
	if err != nil {
		return limits, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := agyProcess(ctx, home, "--output-format", "json", "-p=/usage")
	if err != nil {
		return limits, err
	}
	out, err := cmd.Output()
	if err != nil {
		return limits, err
	}
	return parseAgyUsage(out)
}

// agyGroupName is a model group's name short enough for a limit gauge's
// caption: "Gemini Models" is "Gemini", "Claude and GPT models" is
// "Claude/GPT", "Gemini 4 Argon" is "Argon".
func agyGroupName(name string) string {
	name = strings.TrimSuffix(strings.TrimSuffix(name, " Models"), " models")
	name = strings.ReplaceAll(name, " and ", "/")
	if fields := strings.Fields(name); len(fields) > 1 && strings.HasPrefix(name, "Gemini ") {
		name = fields[len(fields)-1] // a named Gemini model (Gemini 4 Argon): its name
	}
	return name
}

// parseAgyUsage reads agy's /usage result: model groups, each with its
// usage windows (5 hours, a week), shortest window first in each group.
func parseAgyUsage(out []byte) (AccountLimits, error) {
	limits := AccountLimits{Note: "Limits not provided"}
	var result struct {
		Command struct {
			Name string `json:"name"`
			Data struct {
				Groups []struct {
					Name    string `json:"name"`
					Buckets []struct {
						Window    string    `json:"window"`
						Remaining *float64  `json:"remaining_fraction"`
						ResetAt   time.Time `json:"reset_time"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return limits, err
	}
	if result.Command.Name != "usage" {
		return limits, fmt.Errorf("agy did not answer /usage")
	}
	for _, group := range result.Command.Data.Groups {
		var windows []AccountLimitWindow
		for _, bucket := range group.Buckets {
			if bucket.Remaining == nil {
				continue
			}
			period := map[string]string{"weekly": "7d", "daily": "1d"}[bucket.Window]
			if period == "" {
				period = bucket.Window
			}
			name := agyGroupName(group.Name)
			windows = append(windows, AccountLimitWindow{Label: name + " · " + period, used: 100 * (1 - *bucket.Remaining), ResetAt: bucket.ResetAt, Group: name})
		}
		// agy lists the weekly limit first; the 5-hour one comes first here,
		// as for Claude Code.
		sort.SliceStable(windows, func(i, j int) bool {
			return strings.HasSuffix(windows[i].Label, " · 5h") && !strings.HasSuffix(windows[j].Label, " · 5h")
		})
		limits.Windows = append(limits.Windows, windows...)
	}
	return limits, nil
}

// agyModel is a model of `agy models`, its effort variants grouped: agy
// lists gemini-3.8-flash-low, -medium and -high, and takes
// --model gemini-3.8-flash --effort low.
type agyModel struct {
	ID, Name string
	Efforts  []string
}

var agyEffortSuffix = regexp.MustCompile(`^(.*)-(minimal|low|medium|high|xhigh|max)$`)
var agyNameEffort = regexp.MustCompile(`\s*\((Minimal|Low|Medium|High|Extra high|Xhigh|Max)\)\s*$`)

// parseAgyModels reads `agy models`: a line per model, its ID and its name
// separated by a tab.
func parseAgyModels(output string) []agyModel {
	var models []agyModel
	index := map[string]int{}
	for _, line := range strings.Split(output, "\n") {
		id, name, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found || id == "" || strings.ContainsAny(id, " :") {
			continue
		}
		base, effort := id, ""
		if m := agyEffortSuffix.FindStringSubmatch(id); m != nil {
			base, effort = m[1], m[2]
		}
		name = strings.TrimSpace(agyNameEffort.ReplaceAllString(name, ""))
		if name == "" {
			name = base
		}
		i, seen := index[base]
		if !seen {
			i = len(models)
			index[base] = i
			models = append(models, agyModel{ID: base, Name: name})
		}
		if effort != "" && !Contains(models[i].Efforts, effort) {
			models[i].Efforts = append(models[i].Efforts, effort)
		}
	}
	for i := range models {
		models[i].Efforts = sortedEfforts(models[i].Efforts)
	}
	return models
}

// agyModels asks the installed agy which models the account can use.
func agyModels(ctx context.Context) ([]agyModel, error) {
	path, err := AgyExecutable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "models")
	cmd.Dir = os.TempDir()
	platform.PrepareProcess(cmd)
	var stderr outputBuffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	models := parseAgyModels(string(out))
	if len(models) > 0 {
		return models, nil
	}
	if text := strings.TrimSpace(stderr.String() + "\n" + string(out)); text != "" && err != nil {
		return nil, fmt.Errorf("Antigravity CLI: %s", FirstLine(text))
	}
	return nil, fmt.Errorf("Antigravity CLI listed no model: run agy once in a terminal to sign in")
}

// agyEvent is one line of agy's stream-json output; only the fields 1loop
// reads are declared.
type agyEvent struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	Step           *struct {
		State     string    `json:"state"` // ACTIVE, DONE
		Type      string    `json:"step_type"`
		Index     int       `json:"step_index"`
		TextDelta string    `json:"text_delta"`
		Usage     *agyUsage `json:"usage"`
		Tool      *struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
			Output     *string         `json:"output"`
		} `json:"tool_info"`
	} `json:"step_update"`
	Result *struct {
		ConversationID string   `json:"conversation_id"`
		Status         string   `json:"status"` // SUCCESS, ERROR, CANCELED…
		Response       string   `json:"response"`
		Error          string   `json:"error"`
		FinishedReason string   `json:"finished_reason"`
		Usage          agyUsage `json:"usage"`
		Denied         []struct {
			Action      string `json:"action"`
			DisplayName string `json:"display_name"`
		} `json:"denied_actions"`
	} `json:"result"`
}

type agyUsage struct {
	Input     int `json:"input_tokens"`
	Output    int `json:"output_tokens"`
	CacheRead int `json:"cache_read_tokens"`
}

// agyParameters are the arguments of agy's tools 1loop shows.
type agyParameters struct {
	CommandLine   string `json:"CommandLine"`
	Path          string `json:"Path"`
	AbsolutePath  string `json:"AbsolutePath"`
	TargetFile    string `json:"TargetFile"`
	DirectoryPath string `json:"DirectoryPath"`
	SearchPath    string `json:"SearchPath"`
}

func (p agyParameters) path() string {
	for _, path := range []string{p.Path, p.AbsolutePath, p.TargetFile, p.DirectoryPath, p.SearchPath} {
		if path != "" {
			return path
		}
	}
	return ""
}

// agyTool translates an agy tool call into the row 1loop shows: commands,
// reads, listings and edits use 1loop's own rows; every other tool keeps its
// name and arguments.
func agyTool(root, name string, input json.RawMessage) (tool, args string) {
	var in agyParameters
	_ = json.Unmarshal(input, &in)
	path := in.path()
	if rel, err := filepath.Rel(root, path); err == nil && path != "" && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	switch name {
	case "run_command":
		return "shell", encode(map[string]string{"command": in.CommandLine})
	case "read_file", "view_file":
		return "read_file", encode(map[string]string{"path": path})
	case "list_dir", "list_directory":
		return "list_directory", encode(map[string]string{"path": path})
	case "write_file", "write_to_file", "replace_file_content", "multi_replace_file_content", "sed_file":
		return "edit_file", encode(map[string]string{"path": path, "old_text": "", "new_text": ""})
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	return name, string(input)
}

// agyRule is the allow rule that lets agy run a refused call once the user
// approved it: command(<the exact command line>), as agy's own message
// suggests, or <action>(<path>) for another action.
func agyRule(action string, input json.RawMessage) string {
	var in agyParameters
	_ = json.Unmarshal(input, &in)
	target := in.CommandLine
	if action != "command" || target == "" {
		target = in.path()
	}
	if target == "" {
		return ""
	}
	return action + "(" + target + ")"
}

// agyAction is the permission an agy tool needs, as denied_actions names it.
func agyAction(tool string) string {
	switch tool {
	case "run_command", "send_command_input":
		return "command"
	}
	return ""
}

// agyAttachmentsBase is where a run's attached images are saved: the
// temporary folder, except in the Flatpak, whose /tmp is private while agy
// runs on the host (packaging/flatpak/host-run): there, the app's cache
// folder, which the host sees at the same path.
func agyAttachmentsBase() string {
	if os.Getenv("FLATPAK_ID") == "" {
		return ""
	}
	cache, err := os.UserCacheDir()
	if err != nil || os.MkdirAll(cache, 0o700) != nil {
		return ""
	}
	return cache
}

// agyPrompt is a user message in agy's input format. agy reads text only:
// attached images are saved to files in dir and named in the text, for its
// file viewer. (--add-dir is not used for them: agy would then run commands
// in that folder instead of the project.)
func agyPrompt(messageValue Message, preface, dir string) string {
	text := preface + messageValue.Content
	for i, image := range messageValue.Images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil || dir == "" {
			continue
		}
		ext := ".png"
		switch image.Mime {
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		case "image/gif":
			ext = ".gif"
		}
		path := filepath.Join(dir, fmt.Sprintf("image-%s-%d%s", core.Identifier()[:8], i+1, ext))
		if os.WriteFile(path, data, 0o600) == nil {
			text += "\n\n[Attached image: " + path + " (open it with your file viewing tool)]"
		}
	}
	line, _ := json.Marshal(map[string]any{"event": "user", "message": map[string]any{"content": text}})
	return string(line)
}

// agyArgs are the options of an agy run for this chat.
func agyArgs(cfg Config, session string, allow []string) []string {
	var args []string
	if cfg.Model != "" && cfg.Model != "default" {
		args = append(args, "--model", cfg.Model)
		if cfg.Effort != "" {
			args = append(args, "--effort", cfg.Effort)
		}
	}
	if session != "" {
		args = append(args, "--conversation", session)
	}
	if cfg.ApprovalMode == "yolo" {
		args = append(args, "--dangerously-skip-permissions")
	} else if len(allow) > 0 {
		settings, _ := json.Marshal(map[string]any{"permissions": map[string]any{"allow": allow}})
		args = append(args, "--settings", string(settings))
	}
	return args
}

// runAntigravity runs one task in agy: the last message of history is the
// prompt, sent in the chat's agy conversation when it has one. Each message
// is a turn of agy's; messages sent while it works start the next turn.
func (backend *App) runAntigravity(ctx context.Context, cfg Config, history []Message, inboxValue Inbox, emit emitter) error {
	if len(history) == 0 {
		return fmt.Errorf("Nothing to send")
	}
	if cfg.Machine != nil || cfg.MachineID != "" {
		return fmt.Errorf("Antigravity works on this computer's projects only")
	}
	// The phone's local runtime keeps each project's tools inside the project
	// through its launcher (ONELOOP_PROJECT_LAUNCHER); agy's own tools and
	// commands would run outside it, in the whole Debian environment.
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		return fmt.Errorf("Antigravity is not available in the phone's local runtime yet: its tools would not stay inside the project")
	}
	ctx = toolContext(ctx, cfg)
	attachments, err := os.MkdirTemp(agyAttachmentsBase(), "1loop-agy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(attachments)

	messages := append([]Message(nil), history...)
	prompt := messages[len(messages)-1]
	session := HarnessSession(cfg)
	preface := ""
	budget := ModelContextWindow(cfg) * 4 * 40 / 100
	if session == "" && len(messages) > 1 {
		preface = "[Earlier conversation in this chat, for context]\n" + core.TruncateText(levelText(messages[:len(messages)-1], LevelEverything, ModelContextWindow(cfg)), budget) + "\n[End of the earlier conversation]\n\n"
	} else if session != "" && cfg.HandoffSummary != "" {
		preface = "[Since your last turn in this chat, the user continued with another model. Summary of what happened meanwhile]\n" + cfg.HandoffSummary + "\n[End of the summary]\n\n"
	} else if session != "" && cfg.SessionMark > 0 && cfg.Recall != nil {
		if items, err := cfg.Recall(ctx); err == nil {
			end := len(items)
			if end > 0 && items[end-1].Kind == "user" {
				end--
			}
			if cfg.SessionMark < end {
				preface = "[Since your last turn in this chat, the user continued with another model. What happened meanwhile]\n" + itemsText(items[cfg.SessionMark:end]) + "\n[End of what happened meanwhile]\n\n"
			}
		}
	}
	// agy takes no system prompt from 1loop: the user's saved preferences go
	// first in each run's first message, so changes reach resumed chats too.
	if strings.TrimSpace(cfg.UserInstructions) != "" {
		preface = "[The user's preferences: they supplement your instructions]\n" + cfg.UserInstructions + "\n[End of the user's preferences]\n\n" + preface
	}
	answered := false
	defer func() {
		if answered && ctx.Err() == nil {
			emit("session_synced", nil)
		}
	}()

	var allow []string // allow rules the user approved during this task
	line := agyPrompt(prompt, preface, attachments)
	for {
		outcome, err := backend.agyProcess(ctx, cfg, &session, allow, attachments, line, &messages, inboxValue, emit)
		if outcome.answered {
			answered = true
		}
		emit("history", append([]Message(nil), messages...))
		if err != nil {
			return err
		}
		if len(outcome.approved) == 0 {
			break
		}
		// agy refused what the user then approved: the conversation goes on
		// in a new run that allows exactly that.
		allow = append(allow, outcome.approved...)
		notice := Message{Role: "user", Internal: true, Content: "[1loop] The user approved the action(s) Antigravity's headless mode refused: " + strings.Join(outcome.approved, ", ") + ". Run them now and continue the task."}
		messages = append(messages, notice)
		line = agyPrompt(notice, "", "")
	}
	if !answered && ctx.Err() == nil {
		return fmt.Errorf("Antigravity stopped without answering")
	}
	return ctx.Err()
}

type agyOutcome struct {
	approved []string // allow rules for refused calls the user approved
	answered bool     // agy wrote an answer or ran a tool
}

// agyProcess runs agy until it has no turn left: the first line, then each
// message the user sends meanwhile.
func (backend *App) agyProcess(ctx context.Context, cfg Config, session *string, allow []string, attachments, first string, messages *[]Message, inboxValue Inbox, emit emitter) (agyOutcome, error) {
	var outcome agyOutcome
	processCtx, stopProcess := context.WithCancel(ctx)
	defer stopProcess()
	cmd, err := agyCommand(processCtx, cfg.Workspace, agyArgs(cfg, *session, allow)...)
	if err != nil {
		return outcome, err
	}
	logged := append([]string(nil), cmd.Args...)
	for i := 1; i < len(logged); i++ {
		if logged[i-1] == "--settings" {
			logged[i] = "[allow rules]"
		}
	}
	platform.Devf("agy", "start in %s: %s", cfg.Workspace, strings.Join(logged, " "))
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr outputBuffer
	cmd.Stderr = &stderr
	var writing sync.Mutex
	open := true
	send := func(line string) {
		writing.Lock()
		defer writing.Unlock()
		if open {
			io.WriteString(stdin, line+"\n")
		}
	}
	inputOpen := func() bool {
		writing.Lock()
		defer writing.Unlock()
		return open
	}
	finish := func() { // no more input: agy exits after its last turn
		writing.Lock()
		defer writing.Unlock()
		if open {
			open = false
			stdin.Close()
		}
	}
	processDone := make(chan struct{})
	cmd.Cancel = func() error {
		go finish()
		platform.InterruptProcess(cmd)
		go func() {
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-processDone:
			case <-timer.C:
				platform.KillProcessTree(cmd)
			}
		}()
		return nil
	}
	cmd.WaitDelay = 4 * time.Second
	if err := cmd.Start(); err != nil {
		return outcome, err
	}
	defer func() {
		finish()
		cmd.Wait()
		close(processDone)
		if ctx.Err() != nil {
			platform.KillProcessTree(cmd)
		}
	}()
	emit("activity", Activity{State: "wait"})
	send(first)

	type pending struct {
		outcome ToolOutcome
		input   json.RawMessage
		tool    string
		started time.Time
	}
	calls := map[int]*pending{} // tool steps that have not ended yet, by step index
	var unanswered []*pending   // tool steps that ended without output: refused, or silent
	status := &announcer{emit: emit}
	text := ""
	usage := 0
	turns := 1
	inject := func(messageValue Message) {
		*messages = append(*messages, messageValue)
		emit("injected", messageValue)
		turns++
		send(agyPrompt(messageValue, "", attachments))
	}
	record := func(p *pending) {
		call := ToolCall{ID: p.outcome.ID, Type: "function"}
		call.Function.Name, call.Function.Arguments = p.outcome.Name, p.outcome.Args
		*messages = append(*messages, Message{Role: "assistant", ToolCalls: []ToolCall{call}},
			Message{Role: "tool", ToolCallID: p.outcome.ID, Content: p.outcome.modelText()})
		emit("tool_result", p.outcome)
	}

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
				io.Copy(io.Discard, stdout)
				return
			}
		}
	}()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var failure error
	for failure == nil {
		var line []byte
		select {
		case <-ctx.Done():
			return outcome, ctx.Err()
		case <-tick.C:
			// Messages sent while agy works become its next turns. Once its
			// input is closed they stay queued, for the next run.
			if !inputOpen() {
				continue
			}
			for _, extra := range inboxValue.Steer() {
				inject(extra)
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
		var event agyEvent
		if json.Unmarshal(line, &event) != nil {
			platform.Devf("agy", "unreadable line: %s", platform.DevText(string(line), 160))
			continue
		}
		switch event.Event {
		case "init":
			if event.ConversationID != "" && event.ConversationID != *session {
				*session = event.ConversationID
				emit("session", agySessionPrefix+event.ConversationID)
			}
		case "step_update":
			step := event.Step
			if step == nil {
				break
			}
			if step.Usage != nil && step.Usage.Input > 0 {
				usage = step.Usage.Input + step.Usage.CacheRead
				emit("usage", usage)
			}
			switch step.Type {
			case "agent_response":
				if step.TextDelta != "" {
					text += step.TextDelta
					status.last = Activity{State: "write"}
					emit("delta", step.TextDelta)
				} else if step.State == "ACTIVE" && text == "" {
					status.set(Activity{State: "think"})
				}
				if step.State == "DONE" && strings.TrimSpace(text) != "" {
					outcome.answered = true
					*messages = append(*messages, Message{Role: "assistant", Content: text, Usage: usage})
					emit("message_end", nil)
					text = ""
				}
			case "tool":
				if step.Tool == nil {
					break
				}
				p := calls[step.Index]
				if p == nil {
					tool, args := agyTool(cfg.Workspace, step.Tool.Name, step.Tool.Parameters)
					p = &pending{outcome: ToolOutcome{ID: core.Identifier(), Name: tool, Args: args}, input: step.Tool.Parameters, tool: step.Tool.Name, started: time.Now()}
					calls[step.Index] = p
					status.set(Activity{State: "call", Tool: tool, Detail: claudeTarget(cfg.Workspace, args)})
					emit("tool", p.outcome)
					emit("tool_exec", p.outcome)
				}
				if step.State != "DONE" {
					break
				}
				delete(calls, step.Index)
				outcome.answered = true
				p.outcome.Elapsed = time.Since(p.started)
				if step.Tool.Output == nil {
					unanswered = append(unanswered, p) // the result says whether it was refused
					break
				}
				p.outcome.Output = *step.Tool.Output
				record(p)
			default:
				if step.State == "ACTIVE" {
					status.set(Activity{State: "wait"})
				}
			}
		case "result":
			result := event.Result
			if result == nil {
				break
			}
			turns--
			recordUsage(cfg, spentTokens{tokens: usageTokens(result.Usage.Input, result.Usage.Output, result.Usage.CacheRead)})
			if text != "" { // an answer cut short: keep what was written
				*messages = append(*messages, Message{Role: "assistant", Content: text, Usage: usage})
				emit("message_end", nil)
				outcome.answered, text = true, ""
			}
			// What headless agy refused: the user decides now.
			refused := map[string]bool{}
			for _, denied := range result.Denied {
				refused[denied.Action] = true
			}
			for _, p := range unanswered {
				action := agyAction(p.tool)
				if !refused[action] {
					record(p) // ended with nothing to show
					continue
				}
				rule := agyRule(action, p.input)
				var title, preview string
				if p.outcome.Name == "shell" {
					_, command, _ := ToolArgs(p.outcome.Args)
					title, preview = "Run command?", cfg.Workspace+"\n\n$ "+command
				} else {
					title, preview = "Allow "+p.tool+"?", p.outcome.Args
				}
				approved := rule != "" && cfg.ApprovalMode != "yolo" && backend.confirm(ctx, emit, title, preview)
				if approved {
					outcome.approved = append(outcome.approved, rule)
					p.outcome.Err = errors.New("Refused by Antigravity's headless mode; approved, running it again")
				} else {
					p.outcome.Err = errors.New("Not allowed: the user denied this action")
				}
				record(p)
			}
			unanswered = nil
			switch {
			case result.Status == "SUCCESS":
				if strings.TrimSpace(result.Response) == "" && len(result.Denied) == 0 {
					// agy issues #902 and #1065: long tool-using headless
					// turns can end with no answer.
					emit("note", "Antigravity ended the turn without an answer (a known Antigravity CLI issue). Send a message to have it continue.")
				}
			case result.Status == "CANCELED" || result.Status == "CANCELLED":
				failure = fmt.Errorf("Antigravity canceled the turn without finishing it (a known Antigravity CLI issue in long headless turns). Send a message to have it continue.")
			default:
				message := strings.TrimSpace(result.Error)
				if message == "" {
					message = "Antigravity ended the turn with an error (" + strings.ToLower(result.Status) + ")"
				}
				failure = errors.New(message)
				if limited, ok := claudeLimit(0, message); ok {
					failure = &RateLimitError{Cause: failure, Reset: limited}
				}
			}
			if failure != nil || len(outcome.approved) > 0 || turns > 0 {
				if len(outcome.approved) > 0 {
					finish() // the approved calls need a new run
				}
				break
			}
			if next, ok := inboxValue.Next(); ok {
				inject(next)
			} else {
				finish()
			}
		}
	}
	if failure != nil {
		return outcome, failure
	}
	if ctx.Err() != nil {
		return outcome, ctx.Err()
	}
	if !outcome.answered && len(outcome.approved) == 0 {
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return outcome, errors.New(FirstLine(text))
		}
	}
	return outcome, nil
}

// agyHandoffNote asks agy for a handoff summary, in its conversation when
// the chat has one, in plan mode so it changes nothing.
func agyHandoffNote(ctx context.Context, cfg Config, prompt string) (string, error) {
	args := []string{"--mode", "plan"}
	if cfg.Model != "" && cfg.Model != "default" {
		args = append(args, "--model", cfg.Model)
	}
	if session := HarnessSession(cfg); session != "" {
		args = append(args, "--conversation", session)
	}
	dir := cfg.Workspace
	if cfg.MachineID != "" || dir == "" {
		dir = os.TempDir()
	}
	cmd, err := agyCommand(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	cmd.Stdin = strings.NewReader(agyPrompt(Message{Role: "user", Content: prompt}, "", "") + "\n")
	stdout, _ := cmd.StdoutPipe()
	var stderr outputBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer cmd.Wait()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	for scanner.Scan() {
		var event agyEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Result == nil {
			continue
		}
		if event.Result.Status != "SUCCESS" || strings.TrimSpace(event.Result.Response) == "" {
			return "", fmt.Errorf("Antigravity did not write the summary: %s", core.TruncateText(event.Result.Error+" "+strings.ToLower(event.Result.Status), 300))
		}
		return event.Result.Response, nil
	}
	if text := strings.TrimSpace(stderr.String()); text != "" {
		return "", fmt.Errorf("%s", core.TruncateText(text, 300))
	}
	return "", fmt.Errorf("Antigravity ended without an answer")
}
