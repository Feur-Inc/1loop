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
	"time"
	"unicode/utf8"

	"1loop/internal/core"
	"1loop/internal/platform"
)

// coreToolDefinitions are the tools every request carries; the panel and
// desktop tools load on demand (see tool_groups.go).
func coreToolDefinitions() []map[string]any {
	definitions := []struct {
		name, description string
		fields            []string
	}{
		{"shell", "Run a " + platform.ShellDescription() + " command in the workspace after user approval. A command stops after timeout_ms (null: 60000, up to 600000). For longer work, a server or a watcher, set run_in_background to true: the command starts and this call returns at once, with no time limit; you are told when it ends, with the end of its output, and can follow it with background_task. Output longer than max_output_tokens (null: 10000, up to 50000) keeps its start and its end; filter or page long output rather than raising the limit. In Auto mode commands may run in a workspace sandbox: set escalate to true, with a justification, only to rerun a command that must write outside it (null otherwise).", []string{"command", "max_output_tokens:int", "timeout_ms:int", "run_in_background:bool", "escalate:bool", "justification:string?"}},
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
	return append(result, patchToolDefinition(), backgroundToolDefinition(), todoToolDefinition(), recallToolDefinition(), checkpointToolDefinition(), questionToolDefinition(), permissionsToolDefinition())
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

func (backend *App) runTool(ctx context.Context, root string, call ToolCall, begin func(), emit emitter) (output, diff string, err error) {
	if call.Function.Name == "recall" {
		begin()
		output, err = recallConversation(ctx, json.RawMessage(call.Function.Arguments))
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
	var args struct {
		Path      string `json:"path"`
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
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "", "", err
	}
	name := call.Function.Name
	if name == "shell" {
		if strings.TrimSpace(args.Command) == "" {
			return "", "", fmt.Errorf("Empty command")
		}
		review, sandboxed, title, preview, problem := shellApproval(ctx, root, args.Command, args.Escalate != nil && *args.Escalate, args.Reason)
		if problem != nil {
			return "", "", problem
		}
		if review && !backend.confirm(ctx, emit, title, preview) {
			return "", "", fmt.Errorf("Command denied")
		}
		begin()
		if args.Detach != nil && *args.Detach {
			tasks, _ := ctx.Value(tasksContextKey{}).(*BackgroundTasks)
			output, err := startBackground(ctx, tasks, root, args.Command, sandboxed)
			return output, "", err
		}
		timeout, cancel := context.WithTimeout(ctx, shellTimeout(args.Timeout))
		defer cancel()
		cmd := platform.ShellCommand(timeout, args.Command)
		platform.WorkspaceCommand(cmd, root, platform.WorkspaceYOLO(ctx))
		if sandboxed {
			if err := platform.SandboxCommand(cmd, root); err != nil {
				return "", "", err
			}
		}
		platform.PrepareProcess(cmd)
		cmd.Cancel = func() error { return platform.KillProcessTree(cmd) }
		cmd.WaitDelay = time.Second
		var output outputBuffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		tokens := 10000
		if args.MaxTokens != nil {
			tokens = min(50000, max(1000, *args.MaxTokens))
		}
		text := output.String()
		if sandboxed && err != nil && sandboxRefusal(text) {
			text += "\n[1loop: this command ran in the workspace sandbox, which writes only in the workspace, the temporary folders and the build caches. If it must write elsewhere, run it again with escalate: true and a justification.]"
		}
		if timeout.Err() != nil {
			return headAndTail(text, tokens), "", timeout.Err()
		}
		return headAndTail(text, tokens), "", err
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
