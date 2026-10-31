package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"1loop/internal/core"
	"1loop/internal/extensions"
	"1loop/internal/platform"
)

// coreToolDefinitions are the tools every request carries; the panel and
// desktop tools load on demand (see tool_groups.go).
func coreToolDefinitions() []map[string]any {
	definitions := []struct {
		name, description string
		fields            []string
	}{
		{"shell", "Run a " + platform.ShellDescription() + " command after user approval. Each call starts a new shell: cd does not persist. workdir is an existing directory relative to the project, or an absolute granted directory; null uses the project. The result reports cwd, stdout, stderr, exit_code and status. Exit 1 is not automatically success. A command stops after timeout_ms (null: 60000, up to 600000). For longer work, a server or a watcher, set run_in_background to true: the command starts and this call returns at once, with no time limit; you are told when it ends, with the end of its output, and can follow it with background_task. Output longer than max_output_tokens (null: 10000, up to 50000) keeps its start and its end; use command_output with output_id to read retained output pages. Logs are temporary and bounded. In Auto mode commands may run in a workspace sandbox: set escalate to true, with a justification, only to rerun a command that must write outside it (null otherwise).", []string{"command", "workdir:string?", "max_output_tokens:int", "timeout_ms:int", "run_in_background:bool", "escalate:bool", "justification:string?"}},
		{"search_files", "Search workspace file contents with ripgrep (rg must be installed). pattern is a regular expression; path is a file or directory, null uses the project. Honors ignore files, skips binary files and files over 1 MiB; returns at most 100 matching lines per file. no_matches is a successful empty search; failed means a real error. Narrow path or pattern when truncated.", []string{"path:string?", "pattern", "max_output_tokens:int"}},
		{"command_output", "Read a retained shell output page in this conversation. Choose stdout or stderr. offset is a zero-based byte offset, limit defaults to 8192 bytes (maximum 32768). Retention is eight commands, up to 2 MiB per stream; totals and truncated report any discarded bytes. Logs disappear when the conversation runtime closes.", []string{"id", "stream", "offset:int", "limit:int"}},
		{"read_file", "Read a UTF-8 text file in the workspace (maximum 1 MiB): limit lines from line offset (1-based; null: from line 1, 2000 lines). The reply says which lines it holds and how many the file has; read further only when you need them.", []string{"path", "offset:int", "limit:int"}},
		{"list_directory", "List entries of a directory in the workspace. Use . for the root.", []string{"path"}},
		{
			"edit_file",
			"Replace exactly one occurrence of old_text with new_text in an existing file. To create a new file, use old_text empty. A unified diff is shown for approval before writing. Parent directories must exist.",
			[]string{"path", "old_text", "new_text"},
		},
	}
	result := make([]map[string]any, 0, len(definitions))
	for _, d := range definitions {
		properties := map[string]any{}
		var required []string
		for _, field := range d.fields {
			// "name:int" is an optional number: null takes the default (strict
			// schemas require every field).
			if name, ok := strings.CutSuffix(field, ":int"); ok {
				properties[name] = map[string]any{"type": []string{"integer", "null"}}
				required = append(required, name)
				continue
			}
			if name, ok := strings.CutSuffix(field, ":bool"); ok {
				properties[name] = map[string]any{"type": []string{"boolean", "null"}}
				required = append(required, name)
				continue
			}
			if name, ok := strings.CutSuffix(field, ":string?"); ok {
				properties[name] = map[string]any{"type": []string{"string", "null"}}
				required = append(required, name)
				continue
			}
			properties[field] = map[string]any{"type": "string"}
			required = append(required, field)
		}
		result = append(result, map[string]any{"type": "function", "function": map[string]any{
			"name":        d.name,
			"description": d.description,
			"parameters":  map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
		}})
	}
	return append(result, patchToolDefinition(), backgroundToolDefinition(), todoToolDefinition(), recallToolDefinition(), memoryToolDefinition(), checkpointToolDefinition(), questionToolDefinition(), permissionsToolDefinition())
}

// allToolDefinitions are every tool, loaded or not.
func availableCoreTools(cfg Config) []map[string]any {
	var definitions []map[string]any
	for _, definition := range coreToolDefinitions() {
		name := definition["function"].(map[string]any)["name"].(string)
		if cfg.toolAvailable(name) {
			definitions = append(definitions, definition)
		}
	}
	if cfg.toolAvailable("imagegen") {
		definitions = append(definitions, imageGenerationToolDefinition())
	}
	if skill := skillToolDefinition(); skill != nil && cfg.toolAvailable("skill") {
		definitions = append(definitions, skill)
	}
	return definitions
}

func allToolDefinitions(cfg Config) []map[string]any {
	result := availableCoreTools(cfg)
	for _, group := range availableToolGroups(cfg) {
		result = append(result, group.tools()...)
	}
	return result
}

func WorkspacePath(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("Missing path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		var parent string
		parent, err = filepath.EvalSymlinks(filepath.Dir(path))
		resolved = filepath.Join(parent, filepath.Base(path))
	}
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("The path is outside the workspace")
	}
	return resolved, nil
}

// outputBuffer keeps the start and the end of a command's output: the start
// says what ran, the end holds the result or the error.
type outputBuffer struct {
	head, tail []byte
	total      int
}

const outputKeep = 512 << 10

func (buffer *outputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	buffer.total += n
	if room := outputKeep - len(buffer.head); room > 0 {
		k := min(room, len(p))
		buffer.head = append(buffer.head, p[:k]...)
		p = p[k:]
	}
	if len(p) > 0 {
		buffer.tail = append(buffer.tail, p...)
		if len(buffer.tail) > 2*outputKeep {
			buffer.tail = append([]byte(nil), buffer.tail[len(buffer.tail)-outputKeep:]...)
		}
	}
	return n, nil
}

func (buffer *outputBuffer) String() string {
	tail := buffer.tail[max(0, len(buffer.tail)-outputKeep):]
	if skipped := buffer.total - len(buffer.head) - len(tail); skipped > 0 {
		return strings.ToValidUTF8(string(buffer.head)+fmt.Sprintf("\n…[%d bytes not kept]…\n", skipped)+string(tail), "")
	}
	return string(buffer.head) + string(tail)
}

// liveOutput is a shell command's output, also shown while it runs: the
// model only gets it at the end, but the user can follow it in the tool
// call's details ("tool_output", the end of the output so far).
type liveOutput struct {
	mu     sync.Mutex
	buffer outputBuffer
}

const (
	liveOutputEvery = 250 * time.Millisecond
	liveOutputKeep  = 16 << 10 // enough for a screen; light to send to the phone
)

func (live *liveOutput) Write(p []byte) (int, error) {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.buffer.Write(p)
}

// stream sends the output when it grew, until stop returns: no
// tool_output comes after it, so the final result is never overwritten.
func (live *liveOutput) stream(id string, emit emitter) (stop func()) {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(liveOutputEvery)
		defer ticker.Stop()
		shown := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			live.mu.Lock()
			total, text := live.buffer.total, ""
			if total != shown {
				text = live.buffer.String()
			}
			live.mu.Unlock()
			if total == shown {
				continue
			}
			shown = total
			if len(text) > liveOutputKeep {
				text = "…\n" + strings.ToValidUTF8(text[len(text)-liveOutputKeep:], "")
			}
			emit("tool_output", ToolOutcome{ID: id, Name: "shell", Output: text})
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// headAndTail fits text in about tokens tokens (4 bytes each), keeping its
// start and its end.
func headAndTail(text string, tokens int) string {
	budget := tokens * 4
	if len(text) <= budget {
		return text
	}
	half := budget / 2
	cut := len(text) - 2*half
	return strings.ToValidUTF8(text[:half], "") + fmt.Sprintf("\n…[%d bytes omitted; filter the output or raise max_output_tokens]…\n", cut) + strings.ToValidUTF8(text[len(text)-half:], "")
}

// readLines is the part of a file read_file returns: lines from offset
// (1-based), at most limit, with where it stands in the file.
func readLines(text string, offset, limit int) string {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = 2000
	}
	if offset == 1 && len(lines) <= limit {
		return text
	}
	if offset > len(lines) {
		return fmt.Sprintf("[The file has %d lines; offset %d is past its end.]", len(lines), offset)
	}
	end := min(len(lines), offset-1+limit)
	return fmt.Sprintf("[Lines %d-%d of %d.]\n", offset, end, len(lines)) + strings.Join(lines[offset-1:end], "")
}

func fileText(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Expected a regular text file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", fmt.Errorf("File is binary or larger than 1 MiB")
	}
	return string(data), nil
}

// ToolOutcome is what one tool call did, for the model and for the
// transcript: the side panel shows the call, its output and its timing.
type ToolOutcome struct {
	Images         []core.ImagePart
	RecallID       string
	ID, Name, Args string
	Output         string
	Diff           string        // edit_file: the unified diff that was applied
	Elapsed        time.Duration // execution only, approval time excluded
	Err            error
}

// modelText is the tool result the model receives.
func (outcome ToolOutcome) modelText() string {
	if outcome.Err != nil {
		if (outcome.Name == "shell" || outcome.Name == "search_files") && json.Valid([]byte(outcome.Output)) {
			return outcome.Output
		}
		return outcome.Output + "\nError: " + outcome.Err.Error()
	}
	return outcome.Output
}

// executeTool runs one call. It emits "tool" when the call starts (before
// any approval) and "tool_exec" when it really begins to run.
func (backend *App) executeTool(ctx context.Context, root string, call ToolCall, emit emitter) (outcome ToolOutcome) {
	outcome = ToolOutcome{ID: call.ID, Name: call.Function.Name, Args: call.Function.Arguments, RecallID: core.Identifier()[:16]}
	emit("tool", outcome)
	var started time.Time
	begin := func() {
		started = time.Now()
		emit("tool_exec", outcome)
	}
	defer func() {
		if !started.IsZero() {
			outcome.Elapsed = time.Since(started)
		}
	}()
	var input any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &input)
	cfg, _ := ctx.Value(configContextKey{}).(Config)
	if !cfg.toolAvailable(call.Function.Name) {
		outcome.Err = fmt.Errorf("This tool is unavailable or its permission was revoked")
		return outcome
	}
	payload := func() map[string]any {
		return map[string]any{"session_id": cfg.ContextKey, "cwd": root, "tool_name": call.Function.Name, "tool_input": input}
	}
	if hooks := runHooks(ctx, "PreToolUse", call.Function.Name, payload()); hooks.blocked || len(hooks.problems) > 0 {
		reportHookProblems(emit, hooks)
		if hooks.blocked {
			outcome.Err = fmt.Errorf("Blocked by the user's hook: %s", hooks.feedback)
			return outcome
		}
	}
	defer func() {
		after := payload()
		response := map[string]any{"output": outcome.Output, "error": nil}
		if outcome.Err != nil {
			response["error"] = outcome.Err.Error()
		}
		after["tool_response"] = response
		hooks := runHooks(ctx, "PostToolUse", call.Function.Name, after)
		reportHookProblems(emit, hooks)
		if hooks.blocked {
			outcome.Output += "\n[The user's hook says] " + hooks.feedback
		}
	}()
	if call.Function.Name == "install_packages" {
		if !backend.confirm(ctx, emit, "Install Debian packages?", call.Function.Arguments) {
			outcome.Err = fmt.Errorf("The user denied package installation")
		} else {
			begin()
			result, err := cfg.Packages(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			outcome.Output, outcome.Err = result.Text, err
		}
	} else if isPhoneTool(call.Function.Name) {
		// Every phone action is approved, by the reviewer in Auto mode, except
		// in YOLO; in Approval mode the tools are not offered at all.
		title := "Use the phone?"
		if call.Function.Name == "adb_shell" {
			title = "Use local Android shell?"
		}
		if !cfg.toolAvailable(call.Function.Name) {
			outcome.Err = fmt.Errorf("Local phone permission was revoked")
		} else if !backend.confirm(ctx, emit, title, call.Function.Arguments) {
			outcome.Err = fmt.Errorf("The user denied this phone action")
		} else {
			begin()
			result, err := cfg.Phone(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			outcome.Output, outcome.Images, outcome.Err = result.Text, result.Images, err
		}
	} else if call.Function.Name == "computer_use" {
		handler, _ := ctx.Value(computerContextKey{}).(PanelToolHandler)
		switch {
		case handler == nil:
			outcome.Err = fmt.Errorf("Computer Use is not attached to this conversation")
		case computerNeedsApproval(json.RawMessage(call.Function.Arguments)) && !backend.confirm(ctx, emit, "Configure desktop accessibility", call.Function.Arguments):
			outcome.Err = fmt.Errorf("The user denied this desktop configuration change")
		default:
			begin()
			result, err := handler(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			outcome.Output, outcome.Images, outcome.Err = result.Text, result.Images, err
		}
	} else if strings.HasPrefix(call.Function.Name, "terminal_") {
		handler, _ := ctx.Value(terminalContextKey{}).(PanelToolHandler)
		switch {
		case handler == nil:
			outcome.Err = fmt.Errorf("Terminal tools are not attached to this conversation")
		case terminalNeedsApproval(call.Function.Name, json.RawMessage(call.Function.Arguments)) && !terminalRead(ctx, call) && !backend.confirm(ctx, emit, "Use terminal", call.Function.Arguments):
			outcome.Err = fmt.Errorf("The user denied this terminal action")
		default:
			begin()
			result, err := handler(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			outcome.Output, outcome.Images, outcome.Err = result.Text, result.Images, err
		}
	} else if call.Function.Name == "imagegen" {
		outcome.Output, outcome.Images, outcome.Err = backend.generateImage(ctx, cfg, call.Function.Arguments, begin)
	} else if isGitHubTool(call.Function.Name) {
		outcome.Output, outcome.Images, outcome.Err = backend.callGitHub(ctx, cfg, call, begin, emit)
	} else if extensions.IsToolName(call.Function.Name) {
		outcome.Output, outcome.Images, outcome.Err = backend.callMCP(ctx, cfg, call, begin, emit)
	} else if strings.HasPrefix(call.Function.Name, "browser_") {
		begin()
		handler, _ := ctx.Value(browserContextKey{}).(PanelToolHandler)
		if handler == nil {
			outcome.Err = fmt.Errorf("Browser tools are not attached to this conversation")
		} else {
			result, err := handler(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			outcome.Output, outcome.Images, outcome.Err = result.Text, result.Images, err
		}
	} else {
		outcome.Output, outcome.Diff, outcome.Err = backend.runTool(ctx, root, call, begin, emit)
	}
	platform.Devf("tool", "%s %s: %d bytes of output, error %v", outcome.Name, platform.DevText(outcome.Args, 120), len(outcome.Output), outcome.Err)
	return outcome
}

// toolArgs are the arguments of the shell and file tools.
type toolArgs struct {
	Path      string `json:"path"`
	Workdir   string `json:"workdir"`
	Command   string `json:"command"`
	Old       string `json:"old_text"`
	New       string `json:"new_text"`
	MaxTokens *int   `json:"max_output_tokens"`
	Offset    *int   `json:"offset"`
	Limit     *int   `json:"limit"`
	Escalate  *bool  `json:"escalate"`
	Reason    string `json:"justification"`
	Timeout   *int   `json:"timeout_ms"`
	Detach    *bool  `json:"run_in_background"`
}

func (backend *App) runTool(ctx context.Context, root string, call ToolCall, begin func(), emit emitter) (output, diff string, err error) {
	if call.Function.Name == "search_files" {
		begin()
		output, err = searchFiles(ctx, root, call.Function.Arguments)
		return
	}
	if call.Function.Name == "command_output" {
		begin()
		output, err = readCommandOutput(ctx, call.Function.Arguments)
		return
	}
	if call.Function.Name == "recall" {
		begin()
		output, err = recallConversation(ctx, json.RawMessage(call.Function.Arguments))
		return
	}
	if call.Function.Name == "memory" {
		return backend.editMemory(ctx, call.Function.Arguments, begin, emit)
	}
	if call.Function.Name == "skill" {
		begin()
		output, err = loadSkill(call.Function.Arguments)
		return
	}
	if call.Function.Name == "apply_patch" {
		var args struct{ Patch string }
		if err = json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return
		}
		return backend.applyPatch(ctx, root, args.Patch, begin, emit)
	}
	if call.Function.Name == "request_user_input" {
		output, err = backend.askUser(ctx, call.Function.Arguments, begin, emit)
		return
	}
	if call.Function.Name == "request_permissions" {
		output, err = backend.requestPermissions(ctx, call.Function.Arguments, begin, emit)
		return
	}
	if call.Function.Name == "tool_search" {
		begin()
		output, err = searchTools(ctx.Value(configContextKey{}).(Config), call.Function.Arguments)
		return
	}
	if call.Function.Name == "checkpoint" {
		begin()
		var args struct {
			Note string `json:"note"`
		}
		if err = json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return
		}
		if strings.TrimSpace(args.Note) == "" || utf8.RuneCountInString(args.Note) > 2000 {
			return "", "", fmt.Errorf("Provide a concise checkpoint note, between 1 and 2000 characters")
		}
		return "Checkpoint recorded. 1loop will compact after this tool batch if context pressure warrants it.", "", nil
	}
	if call.Function.Name == "background_task" {
		begin()
		tasks, _ := ctx.Value(tasksContextKey{}).(*BackgroundTasks)
		output, err = backgroundTool(tasks, call.Function.Arguments)
		return
	}
	if call.Function.Name == "update_todo" {
		state, _ := ctx.Value(todoContextKey{}).(*TodoState)
		if state == nil {
			return "", "", fmt.Errorf("TODO is not attached to a conversation")
		}
		begin()
		items, err := state.update(json.RawMessage(call.Function.Arguments))
		if err != nil {
			return "", "", err
		}
		emit("todo", nil)
		data, _ := json.Marshal(items)
		return string(data), "", nil
	}
	var args toolArgs
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "", "", err
	}
	name := call.Function.Name
	if machine := machineOf(ctx); machine != nil {
		return backend.machineTool(ctx, machine, root, call, args, begin, emit)
	}
	if name == "shell" {
		if strings.TrimSpace(args.Command) == "" {
			return "", "", fmt.Errorf("Empty command")
		}
		dir, problem := shellDirectory(ctx, nil, root, args.Workdir)
		if problem != nil {
			return "", "", problem
		}
		review, sandboxed, title, preview, problem := shellApproval(ctx, dir, args.Command, args.Escalate != nil && *args.Escalate, args.Reason)
		if problem != nil {
			return "", "", problem
		}
		if review && !backend.confirm(ctx, emit, title, preview) {
			return "", "", fmt.Errorf("Command denied")
		}
		begin()
		if args.Detach != nil && *args.Detach {
			tasks, _ := ctx.Value(tasksContextKey{}).(*BackgroundTasks)
			output, err := startBackground(ctx, tasks, root, dir, args.Command, sandboxed)
			return output, "", err
		}
		timeout, cancel := context.WithTimeout(ctx, shellTimeout(args.Timeout))
		defer cancel()
		cmd := platform.ShellCommand(timeout, args.Command)
		platform.WorkspaceCommandIn(cmd, root, dir, platform.WorkspaceYOLO(ctx))
		if sandboxed {
			if err := platform.SandboxCommand(cmd, root); err != nil {
				return "", "", err
			}
		}
		platform.PrepareProcess(cmd)
		cmd.Cancel = func() error { return platform.KillProcessTree(cmd) }
		cmd.WaitDelay = time.Second
		output := &liveOutput{}
		capture := &commandCapture{}
		cmd.Stdout = io.MultiWriter(&capture.stdout, output)
		cmd.Stderr = io.MultiWriter(&capture.stderr, output)
		stop := output.stream(call.ID, emit)
		err := cmd.Run()
		stop()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		if sandboxed && err != nil && sandboxRefusal(output.buffer.String()) {
			capture.stderr.Write([]byte("\n[1loop: this command ran in the workspace sandbox. If it must write elsewhere, rerun with escalate: true and a justification.]"))
		}
		if timeout.Err() != nil {
			err = timeout.Err()
		}
		return commandResult(ctx, dir, capture, code, err, args.MaxTokens), "", err
	}
	if name != "read_file" && name != "list_directory" && name != "edit_file" {
		return "", "", fmt.Errorf("Unknown tool: %s", name)
	}
	path, err := toolPath(ctx, root, args.Path)
	if err != nil {
		return "", "", err
	}
	switch name {
	case "read_file":
		begin()
		text, err := fileText(path)
		if err != nil {
			return "", "", err
		}
		offset, limit := 1, 2000
		if args.Offset != nil {
			offset = *args.Offset
		}
		if args.Limit != nil {
			limit = *args.Limit
		}
		return readLines(text, offset, limit), "", nil
	case "list_directory":
		begin()
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", "", err
		}
		var out strings.Builder
		for i, entry := range entries {
			if i >= 2000 {
				out.WriteString("… listing limited to 2000 entries\n")
				break
			}
			out.WriteString(entry.Name())
			if entry.IsDir() {
				out.WriteString("/")
			}
			out.WriteString("\n")
		}
		return out.String(), "", nil
	}
	// edit_file
	before, err := fileText(path)
	create := os.IsNotExist(err)
	if err != nil && !create {
		return "", "", err
	}
	if create && args.Old != "" {
		return "", "", fmt.Errorf("The file does not exist")
	}
	if !create && (args.Old == "" || strings.Count(before, args.Old) != 1) {
		return "", "", fmt.Errorf("old_text must match exactly one occurrence")
	}
	after := args.New
	if !create {
		after = strings.Replace(before, args.Old, args.New, 1)
	}
	if len(after) > 1<<20 {
		return "", "", fmt.Errorf("Content exceeds 1 MiB")
	}
	if before == after && !create {
		return "No changes", "", nil
	}
	rel := relPath(root, path)
	diff, err = platform.UnifiedDiff(ctx, rel, before, after)
	if err != nil {
		return "", "", err
	}
	if !backend.confirm(ctx, emit, "Apply changes to "+rel+"?", diff) {
		return "", diff, fmt.Errorf("Edit denied")
	}
	begin()
	// Recheck the target after approval: it may have changed while the user reviewed the diff.
	currentPath, err := toolPath(ctx, root, args.Path)
	if err != nil || currentPath != path {
		return "", diff, fmt.Errorf("The path has changed since the preview")
	}
	current, err := fileText(path)
	if (create && !os.IsNotExist(err)) || (!create && (err != nil || current != before)) {
		return "", diff, fmt.Errorf("The file has changed since the preview; read it again")
	}
	flags := os.O_WRONLY | os.O_TRUNC
	if create {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return "", diff, err
	}
	_, writeErr := io.WriteString(file, after)
	closeErr := file.Close()
	if writeErr != nil {
		return "", diff, writeErr
	}
	if closeErr != nil {
		return "", diff, closeErr
	}
	return "Changes applied: " + rel + "\n" + diff, diff, nil
}
