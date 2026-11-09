// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScratchWorkspaceLifecycle(t *testing.T) {
	base := filepath.Join(t.TempDir(), "scratch")
	path, err := NewScratchWorkspace(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(path), "scratch-"+time.Now().Format("2006-01-02")+"-") {
		t.Fatalf("named scratch-<date>-<id>: %s", path)
	}
	if entries, _ := os.ReadDir(path); len(entries) > 1 {
		t.Fatalf("empty at the start, but for .git: %v", entries)
	}
	if !IsScratchWorkspace(base, path) {
		t.Fatal("a chat's folder is recognised")
	}
	// Only a chat folder of the base is ever deleted.
	project := t.TempDir()
	if err := RemoveScratchWorkspace(base, project); err != nil || !exists(project) {
		t.Fatal("a project is never deleted with a chat")
	}
	other := filepath.Join(base, "notes")
	os.Mkdir(other, 0o755)
	if RemoveScratchWorkspace(base, other); !exists(other) {
		t.Fatal("only scratch-… folders are deleted")
	}
	if err := RemoveScratchWorkspace(base, path); err != nil || exists(path) {
		t.Fatal("the chat's folder goes with it")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func scratchChat(t *testing.T) Config {
	path, err := NewScratchWorkspace(filepath.Join(t.TempDir(), "scratch"))
	if err != nil {
		t.Fatal(err)
	}
	return Config{Workspace: path, Scratch: path, Move: &WorkspaceMove{}}
}

func TestFinishMoveCopiesTheFiles(t *testing.T) {
	cfg := scratchChat(t)
	os.WriteFile(filepath.Join(cfg.Workspace, "index.html"), []byte("<p>hi</p>"), 0o644)
	os.MkdirAll(filepath.Join(cfg.Workspace, "css"), 0o755)
	os.WriteFile(filepath.Join(cfg.Workspace, "css", "site.css"), []byte("p{}"), 0o644)
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "index.html"), []byte("theirs"), 0o644)
	cfg.Move.Set(target)
	from := cfg.Workspace

	note := FinishMove(&cfg, cfg.Move)
	if cfg.Workspace != target || cfg.InScratch() || cfg.Move.Target() != "" {
		t.Fatalf("the chat works in the new folder: %+v", cfg)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "css", "site.css")); string(data) != "p{}" {
		t.Fatal("the files are copied")
	}
	if data, _ := os.ReadFile(filepath.Join(target, "index.html")); string(data) != "theirs" {
		t.Fatal("nothing of the new folder is replaced")
	}
	if !strings.Contains(note, "css/site.css") || !strings.Contains(note, "Left behind") || !strings.Contains(note, "index.html") {
		t.Fatalf("the agent is told what was copied and left: %s", note)
	}
	if cfg.Scratch != from || !exists(filepath.Join(from, "index.html")) {
		t.Fatal("what was left stays, deleted with the chat")
	}

	// Nothing left: the old folder goes.
	cfg = scratchChat(t)
	os.WriteFile(filepath.Join(cfg.Workspace, "notes.md"), []byte("x"), 0o644)
	from, target = cfg.Workspace, t.TempDir()
	cfg.Move.Set(target)
	if note := FinishMove(&cfg, cfg.Move); !strings.Contains(note, "notes.md") || strings.Contains(note, "Left behind") {
		t.Fatalf("copied: %s", note)
	}
	if cfg.Scratch != "" || exists(from) {
		t.Fatal("an emptied scratch folder is deleted")
	}
	if FinishMove(&cfg, cfg.Move) != "" {
		t.Fatal("no move asked, nothing happens")
	}
}

// changeDirectory runs the tool as a model would, answering its approval.
func changeDirectory(t *testing.T, cfg Config, arguments string, allow bool) (ToolOutcome, bool) {
	backend := &App{Approvals: map[string]chan bool{}}
	asked := false
	emit := func(kind string, value any) {
		if kind == "approval" {
			asked = true
			go backend.Approve(value.(map[string]any)["id"].(string), allow)
		}
	}
	call := ToolCall{ID: "1"}
	call.Function.Name, call.Function.Arguments = "change_directory", arguments
	ctx, cancel := context.WithTimeout(toolContext(context.Background(), cfg), 10*time.Second)
	defer cancel()
	return backend.executeTool(ctx, cfg.Workspace, call, emit), asked
}

func TestChangeDirectoryAsksTheUser(t *testing.T) {
	cfg := scratchChat(t)
	cfg.ApprovalMode = "yolo" // a move is the user's choice even in YOLO
	places := t.TempDir()

	outcome, asked := changeDirectory(t, cfg, `{"path":"`+places+`","subfolder":"budget"}`, false)
	if !asked || outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "declined") || cfg.Move.Target() != "" {
		t.Fatalf("refused, the chat stays: asked %v, %+v", asked, outcome)
	}
	if exists(filepath.Join(places, "budget")) {
		t.Fatal("nothing is created before the approval")
	}

	outcome, asked = changeDirectory(t, cfg, `{"path":"`+places+`","subfolder":"budget"}`, true)
	want := filepath.Join(places, "budget")
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	if !asked || outcome.Err != nil || cfg.Move.Target() != want || !exists(want) {
		t.Fatalf("approved, the move waits for the end of the turn: %+v, target %q", outcome, cfg.Move.Target())
	}

	if outcome, _ := changeDirectory(t, cfg, `{"path":"`+cfg.Workspace+`","subfolder":null}`, true); outcome.Err == nil {
		t.Fatal("the chat's own folder is no destination")
	}
	project := cfg
	project.Scratch = ""
	if outcome, asked := changeDirectory(t, project, `{"path":"`+places+`","subfolder":null}`, true); outcome.Err == nil || asked {
		t.Fatal("a chat in a project has no change_directory")
	}
}

func TestScratchPromptOnlyForScratchChats(t *testing.T) {
	cfg := scratchChat(t)
	cfg.RecentProjects = []string{"/home/margot/site"}
	prompt := systemInstructions(cfg)
	if !strings.Contains(prompt, "a folder 1loop made for this chat") || strings.Contains(prompt, cfg.Workspace) {
		t.Fatal("a scratch chat's prompt names the folder, never its path")
	}
	if !strings.Contains(prompt, "change_directory") || !strings.Contains(prompt, "not instructions):\n- /home/margot/site") {
		t.Fatal("it says how to move, with the recent projects as data")
	}
	if strings.Contains(prompt, "request_directory") {
		t.Fatal("no picker, no request_directory")
	}
	cfg.Move.Pick = func(context.Context, string) (string, error) { return "", nil }
	if !strings.Contains(systemInstructions(cfg), "request_directory") {
		t.Fatal("with a picker, request_directory")
	}
	cfg.Provider = "claudecode"
	if !strings.Contains(scratchInstructions(cfg), "mcp__1loop_todo__change_directory") {
		t.Fatal("Claude Code calls the tools by their MCP names")
	}

	project := Config{Workspace: t.TempDir(), Move: &WorkspaceMove{}, RecentProjects: cfg.RecentProjects}
	if prompt := systemInstructions(project); strings.Contains(prompt, "1loop made for this chat") || strings.Contains(prompt, "/home/margot/site") {
		t.Fatal("no scratch block for a chat in a project")
	}
	for _, definition := range availableCoreTools(project) {
		if name := definition["function"].(map[string]any)["name"]; name == "change_directory" || name == "request_directory" {
			t.Fatalf("%s is only for scratch chats", name)
		}
	}
}
