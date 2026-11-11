// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/gitrepo"
)

// fakeSubchats is a host with subchats kept in memory.
type fakeSubchats struct {
	list     []SubchatInfo
	started  []SubchatRequest
	messages []string
	stopped  []string
}

func (host *fakeSubchats) access(parentTitle string) *SubchatAccess {
	access := &SubchatAccess{
		ParentTitle: parentTitle,
		List:        func(context.Context) []SubchatInfo { return host.list },
		Message: func(_ context.Context, id, text string) error {
			host.messages = append(host.messages, id+": "+text)
			return nil
		},
		Stop: func(_ context.Context, id string) error { host.stopped = append(host.stopped, id); return nil },
	}
	if parentTitle == "" {
		access.Start = func(_ context.Context, request SubchatRequest) (SubchatInfo, error) {
			host.started = append(host.started, request)
			info := SubchatInfo{ID: "s1", Title: request.Title, State: SubchatWorking, Folder: "/p/.worktrees/subchat-fix", Branch: "subchat/fix"}
			host.list = append(host.list, info)
			return info, nil
		}
	}
	return access
}

// runSubchat calls the tool with a policy (YOLO, or asking the user, who
// answers allow) and returns what the user was asked.
func runSubchat(t *testing.T, access *SubchatAccess, yolo, allow bool, arguments map[string]any) (output string, err error, asked []string) {
	t.Helper()
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: yolo})
	ctx = context.WithValue(ctx, configContextKey{}, Config{Subchats: access})
	for _, field := range []string{"id", "title", "brief", "text", "context", "isolation", "model"} {
		if _, ok := arguments[field]; !ok {
			arguments[field] = nil
		}
	}
	raw, _ := json.Marshal(arguments)
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = "subchat", string(raw)
	backend := &App{Approvals: map[string]chan bool{}}
	output, _, err = backend.runTool(ctx, t.TempDir(), call, func() {}, func(event string, value any) {
		if event == "approval" {
			request := value.(map[string]any)
			asked = append(asked, request["title"].(string)+"\n"+request["preview"].(string))
			backend.Approve(request["id"].(string), allow)
		}
	})
	return output, err, asked
}

func TestSubchatToolOffered(t *testing.T) {
	if (Config{}).toolAvailable("subchat") {
		t.Fatal("subchat is offered without a host")
	}
	host := &fakeSubchats{}
	cfg := Config{Subchats: host.access("")}
	if !cfg.toolAvailable("subchat") {
		t.Fatal("subchat is not offered with a host")
	}
	found := false
	for _, definition := range coreToolDefinitions() {
		if definition["function"].(map[string]any)["name"] == "subchat" {
			found = true
		}
	}
	if !found {
		t.Fatal("subchat is not among the core tools")
	}
	if text := subchatInstructions(cfg); !strings.Contains(text, "start a subchat with subchat") {
		t.Fatalf("parent instructions: %q", text)
	}
	if text := subchatInstructions(Config{Subchats: host.access("Main")}); !strings.Contains(text, "subchat of the conversation “Main”") || !strings.Contains(text, "id parent") {
		t.Fatalf("subchat instructions: %q", text)
	}
	if text := subchatInstructions(Config{Provider: "claudecode", Todo: &TodoState{}, Subchats: host.access("")}); !strings.Contains(text, "mcp__1loop_todo__subchat") {
		t.Fatalf("Claude Code instructions: %q", text)
	}
}

func TestSubchatStart(t *testing.T) {
	host := &fakeSubchats{}
	access := host.access("")

	// Approval mode: the user is asked, with the brief; a refusal starts nothing.
	_, err, asked := runSubchat(t, access, false, false, map[string]any{"action": "start", "title": "Fix", "brief": "Fix the parser in parse.go; done when go test passes."})
	if err == nil || len(host.started) != 0 || len(asked) != 1 || !strings.Contains(asked[0], "Start a subchat?") || !strings.Contains(asked[0], "parse.go") || !strings.Contains(asked[0], "own worktree") {
		t.Fatalf("refused start: err %v, started %v, asked %q", err, host.started, asked)
	}

	// Accepted: started with the defaults, and the result gives its id and branch.
	output, err, _ := runSubchat(t, access, false, true, map[string]any{"action": "start", "title": "Fix", "brief": "Fix the parser."})
	if err != nil || len(host.started) != 1 || !strings.Contains(output, "Started subchat s1 “Fix”") || !strings.Contains(output, "Branch subchat/fix") {
		t.Fatalf("start = %q, %v; started %v", output, err, host.started)
	}
	if request := host.started[0]; request.Context != "fresh" || request.Isolation != "worktree" || request.Model != "" {
		t.Fatalf("defaults: %+v", request)
	}

	// Full access starts without asking; a fork in the shared folder with another model.
	_, err, asked = runSubchat(t, access, true, false, map[string]any{"action": "start", "brief": "Investigate the flaky test\nand report.", "context": "fork", "isolation": "shared", "model": "cheap"})
	if err != nil || len(asked) != 0 {
		t.Fatalf("YOLO start: %v, asked %q", err, asked)
	}
	if request := host.started[1]; request.Title != "Investigate the flaky test" || request.Context != "fork" || request.Isolation != "shared" || request.Model != "cheap" {
		t.Fatalf("fork: %+v", request)
	}

	// A brief is required, and the options are checked.
	if _, err, _ := runSubchat(t, access, true, true, map[string]any{"action": "start", "title": "x"}); err == nil {
		t.Fatal("started without a brief")
	}
	if _, err, _ := runSubchat(t, access, true, true, map[string]any{"action": "start", "brief": "x", "isolation": "elsewhere"}); err == nil {
		t.Fatal("accepted an unknown isolation")
	}
}

func TestSubchatLimits(t *testing.T) {
	host := &fakeSubchats{}
	for i := range MaxRunningSubchats {
		state := SubchatWorking
		if i == 0 {
			state = SubchatWaiting // waiting for the user counts too
		}
		host.list = append(host.list, SubchatInfo{ID: string(rune('a' + i)), State: state})
	}
	host.list = append(host.list, SubchatInfo{ID: "old", State: SubchatDone})
	if _, err, _ := runSubchat(t, host.access(""), true, true, map[string]any{"action": "start", "brief": "more"}); err == nil || !strings.Contains(err.Error(), "already working") {
		t.Fatalf("a fifth running subchat: %v", err)
	}
	host.list[1].State = SubchatDone
	if _, err, _ := runSubchat(t, host.access(""), true, true, map[string]any{"action": "start", "brief": "more"}); err != nil {
		t.Fatalf("once one is done: %v", err)
	}

	// One level: a subchat starts none, and asks its parent instead.
	sub := &fakeSubchats{}
	if _, err, _ := runSubchat(t, sub.access("Main"), true, true, map[string]any{"action": "start", "brief": "x"}); err == nil || !strings.Contains(err.Error(), "cannot start subchats") {
		t.Fatalf("a subchat's start: %v", err)
	}
	output, err, _ := runSubchat(t, sub.access("Main"), true, true, map[string]any{"action": "message", "id": "parent", "text": "Which branch?"})
	if err != nil || len(sub.messages) != 1 || sub.messages[0] != "parent: Which branch?" || !strings.Contains(output, "Sent to the parent") {
		t.Fatalf("ask the parent: %q %v %v", output, err, sub.messages)
	}
}

func TestSubchatFollow(t *testing.T) {
	host := &fakeSubchats{list: []SubchatInfo{
		{ID: "s1", Title: "Fix", State: SubchatDone, Branch: "subchat/fix", Folder: "/w/fix", Used: 1200, Last: "Fixed parse.go; tests pass."},
		{ID: "s2", Title: "Docs", State: SubchatWorking, Folder: "/w", Used: 800},
	}}
	access := host.access("")
	output, err, _ := runSubchat(t, access, false, false, map[string]any{"action": "status"})
	if err != nil || !strings.Contains(output, "s1 “Fix” · done") || !strings.Contains(output, "s2 “Docs” · working") || !strings.Contains(output, "Together: 2000 tokens") {
		t.Fatalf("status = %q, %v", output, err)
	}
	output, err, _ = runSubchat(t, access, false, false, map[string]any{"action": "status", "id": "s1"})
	if err != nil || !strings.Contains(output, "Last answer: Fixed parse.go") || !strings.Contains(output, "Branch subchat/fix") {
		t.Fatalf("status s1 = %q, %v", output, err)
	}
	// message, stop: no approval, the user approved the subchat already.
	if _, err, asked := runSubchat(t, access, false, false, map[string]any{"action": "message", "id": "s2", "text": "Also README"}); err != nil || len(asked) != 0 || host.messages[0] != "s2: Also README" {
		t.Fatalf("message: %v asked %q, %v", err, asked, host.messages)
	}
	if _, err, _ := runSubchat(t, access, false, false, map[string]any{"action": "stop", "id": "s2"}); err != nil || host.stopped[0] != "s2" {
		t.Fatalf("stop: %v %v", err, host.stopped)
	}
	if _, err, _ := runSubchat(t, access, false, false, map[string]any{"action": "stop", "id": "parent"}); err == nil {
		t.Fatal("stopped the parent")
	}
}

func TestSubchatReport(t *testing.T) {
	report := SubchatReport(SubchatInfo{ID: "s1", Title: "Fix", State: SubchatDone, Branch: "subchat/fix", Folder: "/w/fix", Last: strings.Repeat("x", 3000)})
	if !report.Internal || WakeSummary(report) != "Subchat s1 “Fix” ended its turn: done" || !strings.HasSuffix(report.Content, "Branch subchat/fix · worktree /w/fix") || len(report.Content) > 2300 {
		t.Fatalf("report %q (summary %q)", report.Content, WakeSummary(report))
	}
	items := []ChatItem{{Kind: "user", Text: "go"}, {Kind: "assistant", Text: "first"}, {Kind: "tool", Tool: "shell"}, {Kind: "assistant", Text: " last \n"}}
	if got := LastAnswer(items); got != "last" {
		t.Fatalf("LastAnswer = %q", got)
	}
	if got := LastAnswer(append(items, ChatItem{Kind: "user", Text: "again"})); got != "" {
		t.Fatalf("a turn without an answer: %q", got)
	}
	if got := SubchatState(true, true, ""); got != SubchatWaiting {
		t.Fatalf("waiting: %q", got)
	}
	if got := SubchatState(false, false, SubchatStopped); got != SubchatStopped {
		t.Fatalf("stopped: %q", got)
	}
	if got := SubchatBranch("Fix the Parser: edge cases!"); got != "subchat/fix-the-parser-edge-cases" {
		t.Fatalf("branch %q", got)
	}
}

func TestForkHistory(t *testing.T) {
	call := ToolCall{ID: "c"}
	call.Function.Name = "subchat"
	history := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "split this off"},
		{Role: "assistant", ToolCalls: []ToolCall{call}}, // the start call, still open
	}
	fork := ForkHistory(history)
	if len(fork) != 3 || fork[2].Content != "split this off" {
		t.Fatalf("fork %+v", fork)
	}
}

func TestSubchatConfig(t *testing.T) {
	parent := Config{Provider: "claudecode", ConnectionID: "c", Model: "opus", Session: "s", Scratch: "/tmp/x", Workspace: "/tmp/x", RanWith: &RunIdentity{Provider: "claudecode"}}
	cfg := SubchatConfig(parent, nil)
	if cfg.Session != "" || cfg.RanWith != nil || cfg.Scratch != "" || cfg.Model != "opus" || cfg.Workspace != "/tmp/x" {
		t.Fatalf("config %+v", cfg)
	}
	choices := map[string]ModelChoice{
		"a": {Connection: Connection{ID: "c", Enabled: true}, ID: "haiku"},
		"b": {Connection: Connection{ID: "d", Enabled: true}, ID: "haiku"},
		"c": {Connection: Connection{ID: "e", Enabled: false}, ID: "off"},
	}
	choice, err := ChooseSubchatModel(choices, "HAIKU", parent)
	if err != nil || choice.Connection.ID != "c" {
		t.Fatalf("the parent's connection first: %+v %v", choice, err)
	}
	if _, err := ChooseSubchatModel(choices, "off", parent); err == nil || !strings.Contains(err.Error(), "haiku") {
		t.Fatalf("a disabled model: %v", err)
	}
}

func TestSubchatWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	ctx := context.Background()
	// Not a repository yet: shared, said so.
	created, note, err := SubchatWorktree(ctx, gitrepo.Local, dir, "Fix")
	if err != nil || created.Path != "" || !strings.Contains(note, "shares it") {
		t.Fatalf("outside Git: %+v %q %v", created, note, err)
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	git("add", ".")
	git("commit", "-qm", "a")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644) // uncommitted
	created, note, err = SubchatWorktree(ctx, gitrepo.Local, dir, "Fix")
	if err != nil || created.Branch != "subchat/fix" || !strings.Contains(note, "uncommitted") {
		t.Fatalf("first: %+v %q %v", created, note, err)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "a.txt")); err != nil {
		t.Fatalf("the worktree misses the commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "b.txt")); err == nil {
		t.Fatal("the worktree has the uncommitted file")
	}
	// The same title again: the next free branch.
	again, _, err := SubchatWorktree(ctx, gitrepo.Local, dir, "Fix")
	if err != nil || again.Branch != "subchat/fix-2" || again.Path == created.Path {
		t.Fatalf("second: %+v %v", again, err)
	}
}
