// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewing answers every approval of a backend, with the files it shows.
func reviewing(backend *App, answer func(files []ReviewFile) ApprovalAnswer) emitter {
	return func(event string, data any) {
		if event == "approval" {
			id := data.(map[string]any)["id"].(string)
			backend.AnswerApproval(id, answer(backend.Review(id)))
		}
	}
}

// The user's text is what is written, and the model is told (#242).
func TestReviewedEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	os.WriteFile(path, []byte("one\r\ntwo\r\n"), 0o600)
	call := ToolCall{}
	call.Function.Name, call.Function.Arguments = "edit_file", `{"path":"a.txt","old_text":"two","new_text":"three"}`
	backend := &App{Approvals: map[string]chan ApprovalAnswer{}}
	output, diff, err := backend.runTool(context.Background(), root, call, func() {}, reviewing(backend, func(files []ReviewFile) ApprovalAnswer {
		if len(files) != 1 || files[0].Path != "a.txt" || files[0].Before != "one\r\ntwo\r\n" || files[0].After != "one\r\nthree\r\n" {
			t.Errorf("files %+v", files)
		}
		return ApprovalAnswer{Allow: true, Files: map[string]string{"a.txt": "one\nfour\n"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "one\r\nfour\r\n" {
		t.Errorf("wrote %q", data)
	}
	if !strings.Contains(output, "The user edited your proposal") || !strings.Contains(diff, "+four") || strings.Contains(diff, "three") {
		t.Errorf("output %q, diff %q", output, diff)
	}
	call.Function.Arguments = `{"path":"a.txt","old_text":"four","new_text":"five"}`
	_, _, err = backend.runTool(context.Background(), root, call, func() {}, reviewing(backend, func([]ReviewFile) ApprovalAnswer { return ApprovalAnswer{Note: "use six"} }))
	if err == nil || err.Error() != "Edit denied by the user: use six" {
		t.Errorf("refused: %v", err)
	}
}

// A patch, file by file.
func TestReviewedPatch(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o600)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("b\n"), 0o600)
	call := ToolCall{}
	call.Function.Name = "apply_patch"
	arguments, _ := json.Marshal(map[string]string{"patch": "*** Begin Patch\n*** Update File: a.txt\n@@\n-a\n+A\n*** Update File: b.txt\n@@\n-b\n+B\n*** Add File: c.txt\n+c\n*** End Patch"})
	call.Function.Arguments = string(arguments)
	backend := &App{Approvals: map[string]chan ApprovalAnswer{}}
	output, _, err := backend.runTool(context.Background(), root, call, func() {}, reviewing(backend, func(files []ReviewFile) ApprovalAnswer {
		if len(files) != 3 || files[2].Op != "add" {
			t.Errorf("files %+v", files)
		}
		return ApprovalAnswer{Allow: true, Denied: []string{"b.txt"}, Note: "b stays", Files: map[string]string{"c.txt": "mine\n"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"a.txt": "A\n", "b.txt": "b\n", "c.txt": "mine\n"} {
		if data, _ := os.ReadFile(filepath.Join(root, name)); string(data) != want {
			t.Errorf("%s: %q, want %q", name, data, want)
		}
	}
	for _, want := range []string{"Patch applied: a.txt, c.txt", "edited your proposal before approving it: c.txt", "refused the changes to b.txt", "b stays"} {
		if !strings.Contains(output, want) {
			t.Errorf("output %q lacks %q", output, want)
		}
	}
}

// What Claude Code's edits do, and the input that writes the user's text.
func TestClaudeReview(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("x\ny\nx\n"), 0o600)
	path, _ := json.Marshal(filepath.Join(root, "a.go"))
	for _, test := range []struct{ name, input, after string }{
		{"Edit", `{"file_path":` + string(path) + `,"old_string":"y","new_string":"z"}`, "x\nz\nx\n"},
		{"Edit", `{"file_path":"a.go","old_string":"x","new_string":"w","replace_all":true}`, "w\ny\nw\n"},
		{"Write", `{"file_path":"a.go","content":"new\n"}`, "new\n"},
		{"MultiEdit", `{"file_path":"a.go","edits":[{"old_string":"y","new_string":"z"},{"old_string":"z","new_string":"v"}]}`, "x\nv\nx\n"},
	} {
		file := claudeReview(root, test.name, []byte(test.input))
		if file == nil || file.Path != "a.go" || file.Before != "x\ny\nx\n" || file.After != test.after {
			t.Errorf("%s: %+v", test.name, file)
			continue
		}
		edited, err := claudeEditedInput(test.name, []byte(test.input), file, "mine\n")
		if err != nil {
			t.Fatal(err)
		}
		if again := claudeReview(root, test.name, edited); again == nil || again.After != "mine\n" {
			t.Errorf("%s edited: %s", test.name, edited)
		}
	}
	for _, input := range []string{`{"file_path":"a.go","old_string":"x","new_string":"w"}`, `{"file_path":"a.go","old_string":"absent","new_string":"w"}`} {
		if file := claudeReview(root, "Edit", []byte(input)); file != nil {
			t.Errorf("%s reviewed: %+v", input, file)
		}
	}
	if file := claudeReview(root, "Write", []byte(`{"file_path":"new.go","content":"n"}`)); file == nil || file.Op != "add" {
		t.Errorf("new file: %+v", file)
	}
}
