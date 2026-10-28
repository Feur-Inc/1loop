package agent

// A conversation on an SSH machine (internal/sshmachine): its files, shell
// commands, background tasks, terminal and Git changes are there. The
// agent itself, its models, Browser Use, Computer Use, hooks, skills and
// MCP servers stay on this computer.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"1loop/internal/core"
	"1loop/internal/platform"
	"1loop/internal/terminal"
)

// Machine is where a conversation's workspace is when it is not this
// computer. Its paths are POSIX paths on the machine.
type Machine interface {
	Label() string
	ReadFile(ctx context.Context, name string) ([]byte, error)
	// WriteFile replaces a file's content, or creates it when create (and
	// fails if it exists).
	WriteFile(ctx context.Context, name string, data []byte, create bool) error
	Remove(ctx context.Context, name string) error
	MkdirAll(ctx context.Context, name string) error
	ReadDir(ctx context.Context, name string) ([]fs.FileInfo, error)
	Lstat(ctx context.Context, name string) (fs.FileInfo, error)
	// RealPath is an existing path with its links resolved.
	RealPath(ctx context.Context, name string) (string, error)
	// Start runs a command in dir with the machine's shell; cancelling ctx
	// stops it and what it started.
	Start(ctx context.Context, dir, command string, output io.Writer) (wait func() (int, error), err error)
	Terminal(ctx context.Context, dir string) (terminal.Process, error)
}

type machineContextKey struct{}

// machineOf is the run's machine, nil on this computer.
func machineOf(ctx context.Context) Machine {
	machine, _ := ctx.Value(machineContextKey{}).(Machine)
	return machine
}

// machineErr refuses a conversation whose machine is gone or unsupported.
func machineErr(cfg Config) error {
	if cfg.MachineID == "" {
		return nil
	}
	if cfg.Machine == nil {
		return fmt.Errorf("This conversation's SSH machine is no longer in Settings → SSH machines")
	}
	switch cfg.Provider {
	case "claudecode":
		return fmt.Errorf("Claude Code works on this computer's projects only. Choose another model for %s", cfg.Machine.Label())
	case "chatgptweb":
		return fmt.Errorf("ChatGPT (Web) works on this computer's projects only. Choose another model for %s", cfg.Machine.Label())
	}
	return nil
}

// MachinePath resolves a path of the workspace on the machine: its deepest
// existing part is resolved there, so a link cannot lead outside it. A
// folder the user granted (request_permissions) is allowed too.
func MachinePath(ctx context.Context, machine Machine, root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("Missing path")
	}
	full := name
	if !path.IsAbs(full) {
		full = path.Join(root, full)
	}
	full = path.Clean(full)
	existing, rest := full, ""
	for {
		if _, err := machine.Lstat(ctx, existing); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := path.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("No folder of %s exists", name)
		}
		rest = path.Join(path.Base(existing), rest)
		existing = parent
	}
	real, err := machine.RealPath(ctx, existing)
	if err != nil {
		return "", err
	}
	resolved := path.Join(real, rest)
	folders := []string{root}
	if grants, _ := ctx.Value(grantsContextKey{}).(*PermissionGrants); grants != nil {
		granted, _ := grants.snapshot()
		folders = append(folders, granted...)
	}
	for _, folder := range folders {
		top, err := machine.RealPath(ctx, folder)
		if err == nil && (resolved == top || strings.HasPrefix(resolved, strings.TrimSuffix(top, "/")+"/")) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("The path is outside the workspace")
}

// machineRel is a machine path relative to the workspace, as the
// approvals show it.
func machineRel(root, name string) string {
	if rel, ok := strings.CutPrefix(name, strings.TrimSuffix(root, "/")+"/"); ok {
		return rel
	}
	return name
}

// machineText is a text file of the machine, refused when binary or larger
// than 1 MiB, as fileText.
func machineText(ctx context.Context, machine Machine, name string) (string, error) {
	info, err := machine.Lstat(ctx, name)
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		if name, err = machine.RealPath(ctx, name); err != nil {
			return "", err
		}
		if info, err = machine.Lstat(ctx, name); err != nil {
			return "", err
		}
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Expected a regular text file")
	}
	if info.Size() > 1<<20 {
		return "", fmt.Errorf("File is binary or larger than 1 MiB")
	}
	data, err := machine.ReadFile(ctx, name)
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", fmt.Errorf("File is binary or larger than 1 MiB")
	}
	return string(data), nil
}

// machineNewFile writes a new file, its folders made first.
func machineNewFile(ctx context.Context, machine Machine, name, text string) error {
	if err := machine.MkdirAll(ctx, path.Dir(name)); err != nil {
		return err
	}
	return machine.WriteFile(ctx, name, []byte(text), true)
}

// machineExists says whether a path exists there (a link counts).
func machineExists(ctx context.Context, machine Machine, name string) bool {
	_, err := machine.Lstat(ctx, name)
	return err == nil
}

// MachineDiff is WorkspaceDiff on a machine: git runs there.
func MachineDiff(ctx context.Context, machine Machine, root string, staged bool) (string, error) {
	args := "diff --no-ext-diff --no-textconv --no-color --unified=3"
	if staged {
		args += " --cached"
	}
	out, code, err := machineOutput(ctx, machine, root, "git rev-parse --git-dir >/dev/null 2>&1 || { echo 1loop-not-a-repository; exit 3; }; git "+args+" --")
	if code == 3 && strings.Contains(out, "1loop-not-a-repository") {
		return "", fmt.Errorf("This folder is not a Git repository")
	}
	if err != nil {
		return "", fmt.Errorf("Unable to read Git changes: %w\n%s", err, strings.TrimSpace(out))
	}
	if staged {
		return out, nil
	}
	names, _, err := machineOutput(ctx, machine, root, "git ls-files --others --exclude-standard -z")
	if err != nil {
		return "", fmt.Errorf("Unable to read new Git files: %w", err)
	}
	var result strings.Builder
	result.WriteString(out)
	for _, name := range strings.Split(strings.TrimSuffix(names, "\x00"), "\x00") {
		if name == "" {
			continue
		}
		fmt.Fprintf(&result, "diff --git a/%s b/%s\n", name, name)
		target, err := MachinePath(ctx, machine, root, name) // a link may not show a file outside it
		var text string
		if err == nil {
			text, err = machineText(ctx, machine, target)
		}
		if err != nil {
			result.WriteString("New file · preview unavailable\n")
			continue
		}
		patch, err := platform.UnifiedDiff(ctx, name, "", text)
		if err != nil {
			return "", err
		}
		result.WriteString(patch)
	}
	return result.String(), nil
}

// machineOutput runs a short command there and returns what it printed
// (8 MiB at most).
func machineOutput(ctx context.Context, machine Machine, dir, command string) (string, int, error) {
	var output cappedOutput
	wait, err := machine.Start(ctx, dir, command, &output)
	if err != nil {
		return "", -1, err
	}
	code, err := wait()
	return output.String(), code, err
}

// cappedOutput keeps what a session writes, from its goroutines.
type cappedOutput struct {
	mu   sync.Mutex
	text strings.Builder
}

func (out *cappedOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	if out.text.Len() < 8<<20 {
		out.text.Write(p[:min(len(p), 8<<20-out.text.Len())])
	}
	return len(p), nil
}

func (out *cappedOutput) String() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.text.String()
}

// CopyAttachmentTo puts a file the user attached in the workspace's tmp
// folder on the machine, as CopyAttachment does here.
func CopyAttachmentTo(ctx context.Context, machine Machine, workspace, name string, source io.Reader) (FileAttachment, error) {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" || strings.ContainsAny(name, "\r\n\x00") {
		return FileAttachment{}, fmt.Errorf("Invalid filename")
	}
	data, err := io.ReadAll(io.LimitReader(source, 256<<20))
	if err != nil {
		return FileAttachment{}, err
	}
	dir := path.Join(workspace, "tmp")
	if err := machine.MkdirAll(ctx, dir); err != nil {
		return FileAttachment{}, err
	}
	destination := path.Join(dir, core.Identifier()[:12]+"-"+name)
	if err := machine.WriteFile(ctx, destination, data, true); err != nil {
		return FileAttachment{}, err
	}
	return FileAttachment{Name: name, Path: destination, Workspace: workspace}, nil
}

// machineTool is runTool's shell and file tools on a machine.
func (backend *App) machineTool(ctx context.Context, machine Machine, root string, call ToolCall, args toolArgs, begin func(), emit emitter) (string, string, error) {
	name := call.Function.Name
	if name == "shell" {
		if strings.TrimSpace(args.Command) == "" {
			return "", "", fmt.Errorf("Empty command")
		}
		review, _, title, preview, problem := shellApproval(ctx, root, args.Command, false, args.Reason)
		if problem != nil {
			return "", "", problem
		}
		if review && !backend.confirm(ctx, emit, title, preview) {
			return "", "", fmt.Errorf("Command denied")
		}
		begin()
		if args.Detach != nil && *args.Detach {
			tasks, _ := ctx.Value(tasksContextKey{}).(*BackgroundTasks)
			output, err := startMachineBackground(tasks, machine, root, args.Command)
			return output, "", err
		}
		timeout, cancel := context.WithTimeout(ctx, shellTimeout(args.Timeout))
		defer cancel()
		output := &liveOutput{}
		wait, err := machine.Start(timeout, root, args.Command, output)
		if err != nil {
			return "", "", err
		}
		stop := output.stream(call.ID, emit)
		_, err = wait()
		stop()
		tokens := 10000
		if args.MaxTokens != nil {
			tokens = min(50000, max(1000, *args.MaxTokens))
		}
		text := output.buffer.String()
		if timeout.Err() != nil {
			return headAndTail(text, tokens), "", timeout.Err()
		}
		return headAndTail(text, tokens), "", err
	}
	if name != "read_file" && name != "list_directory" && name != "edit_file" {
		return "", "", fmt.Errorf("Unknown tool: %s", name)
	}
	target, err := MachinePath(ctx, machine, root, args.Path)
	if err != nil {
		return "", "", err
	}
	switch name {
	case "read_file":
		begin()
		text, err := machineText(ctx, machine, target)
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
		entries, err := machine.ReadDir(ctx, target)
		if err != nil {
			return "", "", err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
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
	before, err := machineText(ctx, machine, target)
	create := errors.Is(err, fs.ErrNotExist)
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
	rel := machineRel(root, target)
	diff, err := platform.UnifiedDiff(ctx, rel, before, after)
	if err != nil {
		return "", "", err
	}
	if !backend.confirm(ctx, emit, "Apply changes to "+rel+"?", diff) {
		return "", diff, fmt.Errorf("Edit denied")
	}
	begin()
	// Recheck the target after approval: it may have changed while the user reviewed the diff.
	if current, err := MachinePath(ctx, machine, root, args.Path); err != nil || current != target {
		return "", diff, fmt.Errorf("The path has changed since the preview")
	}
	current, err := machineText(ctx, machine, target)
	if (create && !errors.Is(err, fs.ErrNotExist)) || (!create && (err != nil || current != before)) {
		return "", diff, fmt.Errorf("The file has changed since the preview; read it again")
	}
	if create {
		err = machineNewFile(ctx, machine, target, after)
	} else {
		err = machine.WriteFile(ctx, target, []byte(after), false)
	}
	if err != nil {
		return "", diff, err
	}
	return "Changes applied: " + rel + "\n" + diff, diff, nil
}

// applyMachinePatch is applyPatch on a machine.
func (backend *App) applyMachinePatch(ctx context.Context, machine Machine, root, text string, begin func(), emit emitter) (string, string, error) {
	files, err := parsePatch(text)
	if err != nil {
		return "", "", err
	}
	var changes []patchChange
	var diffs []string
	deletes := false
	for _, file := range files {
		target, err := MachinePath(ctx, machine, root, file.path)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", file.path, err)
		}
		change := patchChange{op: file.op, path: target, target: target, rel: machineRel(root, target)}
		change.targetRel = change.rel
		switch file.op {
		case "add":
			if machineExists(ctx, machine, target) {
				return "", "", fmt.Errorf("%s already exists; update it instead", file.path)
			}
			change.beforeMissing, change.after = true, file.contents
		case "delete":
			if change.before, err = machineText(ctx, machine, target); err != nil {
				return "", "", fmt.Errorf("%s: %w", file.path, err)
			}
			deletes = true
		case "update":
			if change.before, err = machineText(ctx, machine, target); err != nil {
				return "", "", fmt.Errorf("%s: %w", file.path, err)
			}
			if change.after, err = applyHunks(file.path, change.before, file.hunks); err != nil {
				return "", "", err
			}
			if file.moveTo != "" {
				if change.target, err = MachinePath(ctx, machine, root, file.moveTo); err != nil {
					return "", "", fmt.Errorf("%s: %w", file.moveTo, err)
				}
				if machineExists(ctx, machine, change.target) {
					return "", "", fmt.Errorf("%s already exists", file.moveTo)
				}
				change.targetRel = machineRel(root, change.target)
			}
		}
		if len(change.after) > 1<<20 {
			return "", "", fmt.Errorf("%s: content exceeds 1 MiB", file.path)
		}
		diff, err := platform.UnifiedDiff(ctx, change.targetRel, change.before, change.after)
		if err != nil {
			return "", "", err
		}
		if change.targetRel != change.rel {
			diff = "Move " + change.rel + " → " + change.targetRel + "\n" + diff
		}
		if file.op == "delete" {
			diff = "Delete " + change.rel + "\n" + diff
		}
		changes = append(changes, change)
		diffs = append(diffs, diff)
	}
	var names []string
	for _, change := range changes {
		names = append(names, change.targetRel)
	}
	diff := strings.Join(diffs, "\n")
	title := "Apply changes to " + strings.Join(names, ", ") + "?"
	if deletes {
		title = "Delete or change " + strings.Join(names, ", ") + "?"
	}
	if !backend.confirm(ctx, emit, title, diff) {
		return "", diff, fmt.Errorf("Patch denied")
	}
	begin()
	// Recheck after the approval: a file may have changed while the user read.
	for _, change := range changes {
		if change.beforeMissing {
			if machineExists(ctx, machine, change.path) {
				return "", diff, fmt.Errorf("%s appeared since the preview; nothing was written", change.rel)
			}
		} else if current, err := machineText(ctx, machine, change.path); err != nil || current != change.before {
			return "", diff, fmt.Errorf("%s changed since the preview; nothing was written", change.rel)
		}
	}
	for _, change := range changes {
		switch change.op {
		case "delete":
			err = machine.Remove(ctx, change.path)
		case "add":
			err = machineNewFile(ctx, machine, change.path, change.after)
		default:
			if change.target != change.path {
				if err = machineNewFile(ctx, machine, change.target, change.after); err == nil {
					err = machine.Remove(ctx, change.path)
				}
			} else {
				err = machine.WriteFile(ctx, change.path, []byte(change.after), false)
			}
		}
		if err != nil {
			return "", diff, fmt.Errorf("%s: %w (the files before it in the patch were written)", change.rel, err)
		}
	}
	return "Patch applied: " + strings.Join(names, ", ") + "\n" + diff, diff, nil
}

// workspaceInstructions is the workspace's AGENTS.md, read where the
// workspace is, and on a machine where the work happens.
func workspaceInstructions(cfg Config) string {
	if cfg.Machine == nil {
		return projectInstructions(cfg.Workspace)
	}
	text := "\n\nThe workspace is on the SSH machine " + cfg.Machine.Label() + ": your file tools, shell commands, background tasks and the terminal run there, as the machine's user; there is no sandbox. Browser Use and Computer Use run on the user's own computer, not on the machine."
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := cfg.Machine.ReadFile(ctx, path.Join(cfg.Workspace, "AGENTS.md"))
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return text
	}
	agents := strings.ToValidUTF8(string(data[:min(len(data), agentsFileLimit)]), "")
	if len(data) > agentsFileLimit {
		agents += "\n[AGENTS.md is longer than 32 KiB; the rest was left out.]"
	}
	return text + "\n\nProject instructions from AGENTS.md (the user's rules for this workspace):\n" + agents
}
