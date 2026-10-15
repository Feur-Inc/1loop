package agent

// apply_patch: several files changed in one call, in Codex's patch format.
//
//	*** Begin Patch
//	*** Add File: path
//	+line
//	*** Delete File: path
//	*** Update File: path
//	*** Move to: new/path
//	@@ optional line to find first
//	 context
//	-removed
//	+added
//	*** End Patch
//
// The whole patch is checked before anything is written, shown as one diff
// and approved once.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"1loop/internal/platform"
)

type patchHunk struct {
	anchor     string   // the @@ line, searched for before the hunk
	old, fresh []string // lines before and after
	endOfFile  bool
}

type patchFile struct {
	op       string // add, delete, update
	path     string
	moveTo   string
	contents string // add
	hunks    []patchHunk
}

func patchToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "apply_patch",
		"description": "Change several files in one call: add, update (with an optional move) or delete. The patch is checked as a whole, shown as one diff and approved once. Format:\n*** Begin Patch\n*** Add File: <path>\n+<each line>\n*** Update File: <path>\n*** Move to: <new path> (optional)\n@@ <a line to find first, optional>\n <context line>\n-<removed line>\n+<added line>\n*** Delete File: <path>\n*** End Patch\nGive 3 lines of context around each change, unique enough to be found; paths are relative to the workspace.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"patch": map[string]any{"type": "string"},
		}, "required": []string{"patch"}, "additionalProperties": false},
	}}
}

func parsePatch(text string) ([]patchFile, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("The patch must start with *** Begin Patch and end with *** End Patch")
	}
	var files []patchFile
	var current *patchFile
	var hunk *patchHunk
	closeHunk := func() {
		if current != nil && hunk != nil && (len(hunk.old) > 0 || len(hunk.fresh) > 0) {
			current.hunks = append(current.hunks, *hunk)
		}
		hunk = nil
	}
	for i, line := range lines[1 : len(lines)-1] {
		switch {
		case strings.HasPrefix(line, "*** Add File: "), strings.HasPrefix(line, "*** Delete File: "), strings.HasPrefix(line, "*** Update File: "):
			closeHunk()
			op, path, _ := strings.Cut(strings.TrimPrefix(line, "*** "), " File: ")
			files = append(files, patchFile{op: strings.ToLower(op), path: strings.TrimSpace(path)})
			current = &files[len(files)-1]
		case current == nil:
			return nil, fmt.Errorf("Line %d: expected *** Add File, *** Update File or *** Delete File", i+2)
		case strings.HasPrefix(line, "*** Move to: ") && current.op == "update":
			current.moveTo = strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
		case current.op == "add":
			if !strings.HasPrefix(line, "+") {
				return nil, fmt.Errorf("Line %d: every line of an added file starts with +", i+2)
			}
			current.contents += line[1:] + "\n"
		case current.op == "delete":
			return nil, fmt.Errorf("Line %d: a deleted file takes no lines", i+2)
		case strings.HasPrefix(line, "@@"):
			closeHunk()
			hunk = &patchHunk{anchor: strings.TrimSpace(strings.TrimPrefix(line, "@@"))}
		case line == "*** End of File":
			if hunk != nil {
				hunk.endOfFile = true
			}
		default:
			if hunk == nil {
				hunk = &patchHunk{}
			}
			marker, rest := " ", ""
			if line != "" {
				marker, rest = line[:1], line[1:]
			}
			switch marker {
			case " ":
				hunk.old, hunk.fresh = append(hunk.old, rest), append(hunk.fresh, rest)
			case "-":
				hunk.old = append(hunk.old, rest)
			case "+":
				hunk.fresh = append(hunk.fresh, rest)
			default:
				return nil, fmt.Errorf("Line %d: a change line starts with a space, - or +", i+2)
			}
		}
	}
	closeHunk()
	if len(files) == 0 {
		return nil, fmt.Errorf("The patch changes no file")
	}
	for _, file := range files {
		if file.op == "update" && len(file.hunks) == 0 && file.moveTo == "" {
			return nil, fmt.Errorf("%s: no change given", file.path)
		}
	}
	return files, nil
}

// findLines is where want starts in lines, from start: exactly, then
// ignoring trailing spaces, then ignoring the indentation as well.
func findLines(lines, want []string, start int, atEnd bool) int {
	for _, normalize := range []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRight(s, " \t") },
		strings.TrimSpace,
	} {
		from, to := start, len(lines)-len(want)
		if atEnd {
			from = max(start, to)
		}
	search:
		for i := from; i <= to; i++ {
			for j := range want {
				if normalize(lines[i+j]) != normalize(want[j]) {
					continue search
				}
			}
			return i
		}
	}
	return -1
}

// applyHunks returns the file's new text.
func applyHunks(path, text string, hunks []patchHunk) (string, error) {
	lines := strings.Split(text, "\n")
	trailingNewline := strings.HasSuffix(text, "\n")
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	position := 0
	for _, hunk := range hunks {
		if hunk.anchor != "" {
			at := findLines(lines, []string{hunk.anchor}, position, false)
			if at < 0 {
				return "", fmt.Errorf("%s: cannot find the line %q", path, hunk.anchor)
			}
			position = at + 1
		}
		if len(hunk.old) == 0 { // pure addition: at the anchor, or at the end
			at := position
			if hunk.anchor == "" {
				at = len(lines)
			}
			lines = append(lines[:at], append(append([]string{}, hunk.fresh...), lines[at:]...)...)
			position = at + len(hunk.fresh)
			continue
		}
		at := findLines(lines, hunk.old, position, hunk.endOfFile)
		if at < 0 {
			return "", fmt.Errorf("%s: cannot find these lines:\n%s", path, strings.Join(hunk.old, "\n"))
		}
		lines = append(lines[:at], append(append([]string{}, hunk.fresh...), lines[at+len(hunk.old):]...)...)
		position = at + len(hunk.fresh)
	}
	result := strings.Join(lines, "\n")
	if trailingNewline || len(lines) > 0 {
		result += "\n"
	}
	return result, nil
}

type patchChange struct {
	op             string
	path, target   string // resolved; target differs on a move
	rel, targetRel string
	before, after  string
	beforeMissing  bool
}

// applyPatch checks the whole patch, asks once, then writes.
func (backend *App) applyPatch(ctx context.Context, root, text string, begin func(), emit emitter) (string, string, error) {
	files, err := parsePatch(text)
	if err != nil {
		return "", "", err
	}
	var changes []patchChange
	var diffs []string
	deletes := false
	for _, file := range files {
		resolve := toolPath
		if file.op == "add" {
			resolve = newFilePath
		}
		path, err := resolve(ctx, root, file.path)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", file.path, err)
		}
		change := patchChange{op: file.op, path: path, target: path, rel: relPath(root, path)}
		change.targetRel = change.rel
		switch file.op {
		case "add":
			if _, err := os.Stat(path); err == nil {
				return "", "", fmt.Errorf("%s already exists; update it instead", file.path)
			}
			change.beforeMissing, change.after = true, file.contents
		case "delete":
			if change.before, err = fileText(path); err != nil {
				return "", "", fmt.Errorf("%s: %w", file.path, err)
			}
			deletes = true
		case "update":
			if change.before, err = fileText(path); err != nil {
				return "", "", fmt.Errorf("%s: %w", file.path, err)
			}
			if change.after, err = applyHunks(file.path, change.before, file.hunks); err != nil {
				return "", "", err
			}
			if file.moveTo != "" {
				if change.target, err = newFilePath(ctx, root, file.moveTo); err != nil {
					return "", "", fmt.Errorf("%s: %w", file.moveTo, err)
				}
				if _, err := os.Stat(change.target); err == nil {
					return "", "", fmt.Errorf("%s already exists", file.moveTo)
				}
				change.targetRel = relPath(root, change.target)
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
			if _, err := os.Stat(change.path); err == nil {
				return "", diff, fmt.Errorf("%s appeared since the preview; nothing was written", change.rel)
			}
		} else if current, err := fileText(change.path); err != nil || current != change.before {
			return "", diff, fmt.Errorf("%s changed since the preview; nothing was written", change.rel)
		}
	}
	for _, change := range changes {
		switch change.op {
		case "delete":
			err = os.Remove(change.path)
		case "add":
			err = writeNewFile(change.path, change.after)
		default:
			if change.target != change.path {
				if err = writeNewFile(change.target, change.after); err == nil {
					err = os.Remove(change.path)
				}
			} else {
				err = os.WriteFile(change.path, []byte(change.after), 0644)
			}
		}
		if err != nil {
			return "", diff, fmt.Errorf("%s: %w (the files before it in the patch were written)", change.rel, err)
		}
	}
	return "Patch applied: " + strings.Join(names, ", ") + "\n" + diff, diff, nil
}

func writeNewFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = io.WriteString(file, text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
