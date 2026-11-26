// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"1loop/internal/core"
	"1loop/internal/platform"
)

// An edit waiting for approval can be reviewed (#242): each file's text
// before and after, to read whole, change before approving, or refuse file
// by file. Its answer says what the user kept.

// ReviewFile is a file an edit would change.
type ReviewFile struct {
	Path   string `json:"path"`             // as shown, relative to the workspace
	Before string `json:"before,omitempty"` // "" for a new file
	After  string `json:"after,omitempty"`
	Op     string `json:"op,omitempty"`    // "add", "delete", or "" for a change
	From   string `json:"from,omitempty"`  // where it moves from
	Fixed  bool   `json:"fixed,omitempty"` // its text cannot be changed here
}

// ApprovalAnswer is what the user decides. Files holds the texts they
// changed and Denied the files they refused, by ReviewFile.Path.
type ApprovalAnswer struct {
	Allow  bool              `json:"allow"`
	Note   string            `json:"note,omitempty"` // to the model, with a refusal
	Files  map[string]string `json:"files,omitempty"`
	Denied []string          `json:"denied,omitempty"`
}

// maxReviewText is what one file may hold to be reviewed; a larger one is
// approved from its diff.
const maxReviewText = 1 << 20

// denial is the error of a refused action, with the user's reason.
func (answer ApprovalAnswer) denial(what string) error {
	if note := strings.TrimSpace(answer.Note); note != "" {
		return fmt.Errorf("%s denied by the user: %s", what, note)
	}
	return fmt.Errorf("%s denied", what)
}

// text is a file's text as approved, and whether the user changed it. The
// review shows LF: the proposal's CRLF comes back.
func (answer ApprovalAnswer) text(path, proposed string) (string, bool) {
	edited, ok := answer.Files[path]
	if !ok {
		return proposed, false
	}
	edited = strings.ReplaceAll(edited, "\r\n", "\n")
	if edited == strings.ReplaceAll(proposed, "\r\n", "\n") {
		return proposed, false
	}
	if strings.Contains(proposed, "\r\n") {
		edited = strings.ReplaceAll(edited, "\n", "\r\n")
	}
	return edited, true
}

// confirmEdits is confirm for an edit whose files can be reviewed.
func (backend *App) confirmEdits(ctx context.Context, emit emitter, title, preview string, files []ReviewFile) ApprovalAnswer {
	for _, file := range files {
		if len(file.Before) > maxReviewText || len(file.After) > maxReviewText {
			files = nil
			break
		}
	}
	return backend.confirmAnswer(ctx, emit, title, preview, files)
}

// approveEdit asks for one file's edit: the text to write, its diff, and what
// to tell the model about the user's own changes.
func (backend *App) approveEdit(ctx context.Context, emit emitter, rel, before, after, diff string, create bool) (text, shown, note string, err error) {
	file := ReviewFile{Path: rel, Before: before, After: after}
	if create {
		file.Op = "add"
	}
	answer := backend.confirmEdits(ctx, emit, "Apply changes to "+rel+"?", diff, []ReviewFile{file})
	if !answer.Allow {
		return "", diff, "", answer.denial("Edit")
	}
	text, edited := answer.text(rel, after)
	if !edited {
		return after, diff, "", nil
	}
	if len(text) > 1<<20 {
		return "", diff, "", fmt.Errorf("Content exceeds 1 MiB")
	}
	shown, err = platform.UnifiedDiff(ctx, rel, before, text)
	if err != nil {
		return "", diff, "", err
	}
	return text, shown, "The user edited your proposal before approving it; this is what was written. Keep their version.\n", nil
}

// reviewPatch asks for a patch: the changes to write, their diff, and what
// to tell the model.
func (backend *App) reviewPatch(ctx context.Context, emit emitter, title, diff string, changes []patchChange) (kept []patchChange, shown, note string, err error) {
	files := make([]ReviewFile, len(changes))
	for i, change := range changes {
		files[i] = ReviewFile{Path: change.targetRel, Before: change.before, After: change.after}
		switch change.op {
		case "add", "delete":
			files[i].Op = change.op
		}
		if change.targetRel != change.rel {
			files[i].From = change.rel
		}
		files[i].Fixed = change.op == "delete"
	}
	answer := backend.confirmEdits(ctx, emit, title, diff, files)
	if !answer.Allow {
		return nil, diff, "", answer.denial("Patch")
	}
	if len(answer.Files) == 0 && len(answer.Denied) == 0 {
		return changes, diff, "", nil
	}
	var edited, denied, diffs []string
	for _, change := range changes {
		if Contains(answer.Denied, change.targetRel) {
			denied = append(denied, change.targetRel)
			continue
		}
		if change.op != "delete" {
			var changed bool
			if change.after, changed = answer.text(change.targetRel, change.after); changed {
				edited = append(edited, change.targetRel)
			}
			if len(change.after) > 1<<20 {
				return nil, diff, "", fmt.Errorf("%s: content exceeds 1 MiB", change.targetRel)
			}
		}
		one, err := platform.UnifiedDiff(ctx, change.targetRel, change.before, change.after)
		if err != nil {
			return nil, diff, "", err
		}
		diffs = append(diffs, one)
		kept = append(kept, change)
	}
	if len(kept) == 0 {
		return nil, diff, "", answer.denial("Patch")
	}
	if len(edited) > 0 {
		note += "The user edited your proposal before approving it: " + strings.Join(edited, ", ") + ". The diff below is what was written. Keep their version.\n"
	}
	if len(denied) > 0 {
		note += "The user refused the changes to " + strings.Join(denied, ", ") + ": nothing was written there."
		if reason := strings.TrimSpace(answer.Note); reason != "" {
			note += " Their reason: " + reason
		}
		note += "\n"
	}
	return kept, strings.Join(diffs, "\n"), note, nil
}

// claudeReview is the file a Claude Code edit would change, nil when its
// text before or after cannot be told here.
func claudeReview(root, name string, input json.RawMessage) *ReviewFile {
	var in struct {
		Path    string  `json:"file_path"`
		Content *string `json:"content"`
		Old     string  `json:"old_string"`
		New     string  `json:"new_string"`
		All     bool    `json:"replace_all"`
		Edits   []struct {
			Old string `json:"old_string"`
			New string `json:"new_string"`
			All bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if json.Unmarshal(input, &in) != nil || in.Path == "" {
		return nil
	}
	path := in.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	file := &ReviewFile{Path: claudeRelPath(root, path)}
	before, err := fileText(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil
		}
		file.Op = "add"
	}
	// Claude Code matches and writes with LF, and keeps the file's own.
	file.Before = strings.ReplaceAll(before, "\r\n", "\n")
	file.After = file.Before
	replace := func(old, text string, all bool) bool {
		switch count := strings.Count(file.After, old); {
		case old == "":
			if file.After != "" {
				return false
			}
			file.After = text
		case count == 0 || count > 1 && !all:
			return false // Claude Code's own tool says what is wrong
		case all:
			file.After = strings.ReplaceAll(file.After, old, text)
		default:
			file.After = strings.Replace(file.After, old, text, 1)
		}
		return true
	}
	switch name {
	case "Write":
		if in.Content == nil {
			return nil
		}
		file.After = strings.ReplaceAll(*in.Content, "\r\n", "\n")
	case "Edit":
		if !replace(in.Old, in.New, in.All) {
			return nil
		}
	case "MultiEdit":
		for _, edit := range in.Edits {
			if !replace(edit.Old, edit.New, edit.All) {
				return nil
			}
		}
	default:
		return nil
	}
	return file
}

// claudeEditedInput is a tool's input once the user changed the text it
// writes: the whole file, replaced by theirs.
func claudeEditedInput(name string, input json.RawMessage, file *ReviewFile, text string) (json.RawMessage, error) {
	var in map[string]any
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	switch name {
	case "Write":
		in["content"] = text
	case "Edit":
		in["old_string"], in["new_string"], in["replace_all"] = file.Before, text, false
	case "MultiEdit":
		in["edits"] = []map[string]any{{"old_string": file.Before, "new_string": text}}
	default:
		return nil, fmt.Errorf("%s cannot be edited", name)
	}
	return json.Marshal(in)
}

// askApproval asks the user, whatever the approval mode.
func (backend *App) askApproval(ctx context.Context, emit emitter, title, preview string) bool {
	return backend.askAnswer(ctx, emit, title, preview, nil).Allow
}

// askAnswer asks the user; an edit's files wait beside it, for Review.
func (backend *App) askAnswer(ctx context.Context, emit emitter, title, preview string, files []ReviewFile) ApprovalAnswer {
	if ctx.Err() != nil {
		return ApprovalAnswer{}
	}
	id := core.Identifier()
	ch := make(chan ApprovalAnswer, 1)
	backend.mu.Lock()
	backend.Approvals[id] = ch
	if len(files) > 0 {
		if backend.reviews == nil {
			backend.reviews = map[string][]ReviewFile{}
		}
		backend.reviews[id] = files
	}
	backend.mu.Unlock()
	defer func() {
		backend.mu.Lock()
		delete(backend.Approvals, id)
		delete(backend.reviews, id)
		backend.mu.Unlock()
	}()
	emit("approval", map[string]any{"id": id, "title": title, "preview": preview, "review": len(files) > 0})
	select {
	case answer := <-ch:
		return answer
	case <-ctx.Done():
		return ApprovalAnswer{}
	}
}

// Review is the files of an edit waiting for approval.
func (backend *App) Review(id string) []ReviewFile {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.reviews[id]
}

// Approve answers the approval id; false when none of that id waits (it
// was answered elsewhere, or its run stopped).
func (backend *App) Approve(id string, allow bool) bool {
	return backend.AnswerApproval(id, ApprovalAnswer{Allow: allow})
}

// AnswerApproval is Approve with what the user made of the edit.
func (backend *App) AnswerApproval(id string, answer ApprovalAnswer) bool {
	backend.mu.Lock()
	ch := backend.Approvals[id]
	backend.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- answer:
		return true
	default:
		return false // already answered
	}
}
