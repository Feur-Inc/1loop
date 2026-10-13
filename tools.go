package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

func toolDefinitions() []map[string]any {
	definitions := []struct {
		name, description string
		fields            []string
	}{
		{"shell", "Run a Linux bash command in the workspace after user approval. Commands have a 60 second timeout.", []string{"command"}},
		{"read_file", "Read a UTF-8 text file in the workspace (maximum 1 MiB).", []string{"path"}},
		{"list_directory", "List entries of a directory in the workspace. Use . for the root.", []string{"path"}},
		{"edit_file", "Replace exactly one occurrence of old_text with new_text in an existing file. To create a new file, use old_text empty. A unified diff is shown for approval before writing. Parent directories must exist.", []string{"path", "old_text", "new_text"}},
	}
	result := make([]map[string]any, 0, len(definitions))
	for _, d := range definitions {
		properties := map[string]any{}
		for _, field := range d.fields {
			properties[field] = map[string]any{"type": "string"}
		}
		result = append(result, map[string]any{"type": "function", "function": map[string]any{"name": d.name, "description": d.description, "parameters": map[string]any{"type": "object", "properties": properties, "required": d.fields, "additionalProperties": false}}})
	}
	result = append(result, todoToolDefinition(), recallToolDefinition(), checkpointToolDefinition())
	return append(result, browserToolDefinitions()...)
}

func workspacePath(root, path string) (string, error) {
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

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := (128 << 10) - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.Buffer.Write(p)
	}
	return n, nil
}

func fileText(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Expected a regular text file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", fmt.Errorf("File is binary or larger than 1 MiB")
	}
	return string(data), nil
}

func unifiedDiff(ctx context.Context, path, before, after string) (string, error) {
	oldReader, oldWriter, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer oldReader.Close()
	newReader, newWriter, err := os.Pipe()
	if err != nil {
		oldWriter.Close()
		return "", err
	}
	defer newReader.Close()
	cmd := exec.CommandContext(ctx, "diff", "-u", "--label", "a/"+path, "--label", "b/"+path, "/dev/fd/3", "/dev/fd/4")
	cmd.ExtraFiles = []*os.File{oldReader, newReader}
	go func() { defer oldWriter.Close(); io.WriteString(oldWriter, before) }()
	go func() { defer newWriter.Close(); io.WriteString(newWriter, after) }()
	output, err := cmd.CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return "", fmt.Errorf("Creating diff: %w", err)
		}
	}
	return string(output), nil
}

// toolOutcome is what one tool call did, for the model and for the
// transcript: the side panel shows the call, its output and its timing.
type toolOutcome struct {
	Images         []ImagePart
	RecallID       string
	ID, Name, Args string
	Output         string
	Diff           string        // edit_file: the unified diff that was applied
	Elapsed        time.Duration // execution only, approval time excluded
	Err            error
}

// modelText is the tool result the model receives.
func (o toolOutcome) modelText() string {
	if o.Err != nil {
		return o.Output + "\nError: " + o.Err.Error()
	}
	return o.Output
}

// executeTool runs one call. It emits "tool" when the call starts (before
// any approval) and "tool_exec" when it really begins to run.
func (a *app) executeTool(ctx context.Context, root string, call toolCall, emit emitter) (outcome toolOutcome) {
	outcome = toolOutcome{ID: call.ID, Name: call.Function.Name, Args: call.Function.Arguments, RecallID: identifier()[:16]}
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
	if strings.HasPrefix(call.Function.Name, "browser_") {
		begin()
		handler, _ := ctx.Value(browserContextKey{}).(browserHandler)
		if handler == nil {
			outcome.Err = fmt.Errorf("Browser tools are not attached to this conversation")
		} else {
			result, err := handler(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			outcome.Output, outcome.Images, outcome.Err = result.Text, result.Images, err
		}
	} else {
		outcome.Output, outcome.Diff, outcome.Err = a.runTool(ctx, root, call, begin, emit)
	}
	devf("tool", "%s %s: %d bytes of output, error %v", outcome.Name, devText(outcome.Args, 120), len(outcome.Output), outcome.Err)
	return outcome
}

func (a *app) runTool(ctx context.Context, root string, call toolCall, begin func(), emit emitter) (output, diff string, err error) {
	if call.Function.Name == "recall" {
		begin()
		output, err = recallConversation(ctx, json.RawMessage(call.Function.Arguments))
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
	if call.Function.Name == "update_todo" {
		state, _ := ctx.Value(todoContextKey{}).(*todoState)
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
		Path    string `json:"path"`
		Command string `json:"command"`
		Old     string `json:"old_text"`
		New     string `json:"new_text"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "", "", err
	}
	name := call.Function.Name
	if name == "shell" {
		if strings.TrimSpace(args.Command) == "" {
			return "", "", fmt.Errorf("Empty command")
		}
		if !a.confirm(ctx, emit, "Run command?", root+"\n\n$ "+args.Command) {
			return "", "", fmt.Errorf("Command denied")
		}
		begin()
		timeout, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(timeout, "bash", "-c", args.Command)
		cmd.Dir = root
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		var output limitedBuffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		if timeout.Err() != nil {
			return output.String(), "", timeout.Err()
		}
		return output.String(), "", err
	}
	if name != "read_file" && name != "list_directory" && name != "edit_file" {
		return "", "", fmt.Errorf("Unknown tool: %s", name)
	}
	path, err := workspacePath(root, args.Path)
	if err != nil {
		return "", "", err
	}
	switch name {
	case "read_file":
		begin()
		text, err := fileText(path)
		return text, "", err
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
	rel, _ := filepath.Rel(root, path)
	diff, err = unifiedDiff(ctx, rel, before, after)
	if err != nil {
		return "", "", err
	}
	if !a.confirm(ctx, emit, "Apply changes to "+rel+"?", diff) {
		return "", diff, fmt.Errorf("Edit denied")
	}
	begin()
	// Recheck the target after approval: it may have changed while the user reviewed the diff.
	currentPath, err := workspacePath(root, args.Path)
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
	f, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return "", diff, err
	}
	_, writeErr := io.WriteString(f, after)
	closeErr := f.Close()
	if writeErr != nil {
		return "", diff, writeErr
	}
	if closeErr != nil {
		return "", diff, closeErr
	}
	return "Changes applied: " + rel + "\n" + diff, diff, nil
}
