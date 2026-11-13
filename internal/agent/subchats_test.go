// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/gitrepo"
)

// fakeChats is the chats tool of each fake host's subchat access.
var fakeChats = map[*SubchatAccess]*ChatAccess{}

// fakeSubchats is a host with subchats kept in memory.
type fakeSubchats struct {
	list     []SubchatInfo
	started  []SubchatRequest
	messages []string
	attached []ChatAttachments // each message's
	stopped  []string
}

func (host *fakeSubchats) access(parentTitle string) *SubchatAccess {
	access := &SubchatAccess{
		ParentTitle: parentTitle,
		List:        func(context.Context) []SubchatInfo { return host.list },
		Stop:        func(_ context.Context, id string) error { host.stopped = append(host.stopped, id); return nil },
	}
	fakeChats[access] = host.chats(parentTitle)
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

// chats is the host's chats tool: its subchats, its parent ("parent", in a
// subchat), and one other chat, "other".
func (host *fakeSubchats) chats(parentTitle string) *ChatAccess {
	find := func(_ context.Context, id string) (ChatInfo, error) {
		if id == "parent" && parentTitle != "" {
			return ChatInfo{ID: "p1", Title: parentTitle, State: ChatIdle, Relation: RelationParent}, nil
		}
		if id == "other" {
			return ChatInfo{ID: "other", Title: "Docs", Project: "site", State: ChatWorking}, nil
		}
		for _, info := range host.list {
			if info.ID == id {
				return ChatInfo{ID: id, Title: info.Title, State: ChatIdle, Relation: RelationSubchat}, nil
			}
		}
		return ChatInfo{}, fmt.Errorf("No chat %s", id)
	}
	return &ChatAccess{Find: find,
		List: func(ctx context.Context) []ChatInfo {
			other, _ := find(ctx, "other")
			return []ChatInfo{other}
		},
		Send: func(ctx context.Context, id, text string, attachments ChatAttachments) error {
			if _, err := find(ctx, id); err != nil {
				return err
			}
			host.messages = append(host.messages, id+": "+text)
			host.attached = append(host.attached, attachments)
			return nil
		}}
}

// runSubchat calls the tool with a policy (YOLO, or asking the user, who
// answers allow) and returns what the user was asked.
func runSubchat(t *testing.T, access *SubchatAccess, yolo, allow bool, arguments map[string]any) (output string, err error, asked []string) {
	t.Helper()
	return runTool(t, "subchat", Config{Subchats: access, Chats: fakeChats[access]}, yolo, allow, arguments)
}

// runTool calls a tool with a policy (YOLO, or asking the user, who answers
// allow) and returns what the user was asked.
func runTool(t *testing.T, tool string, cfg Config, yolo, allow bool, arguments map[string]any) (output string, err error, asked []string) {
	t.Helper()
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: yolo})
	ctx = context.WithValue(ctx, configContextKey{}, cfg)
	for _, field := range []string{"id", "title", "brief", "text", "context", "isolation", "model", "files"} {
		if _, ok := arguments[field]; !ok {
			arguments[field] = nil
		}
	}
	raw, _ := json.Marshal(arguments)
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = tool, string(raw)
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
	// A host's refusal (the subchat is not working) reaches the model (#325).
	access.Stop = func(context.Context, string) error { return SubchatNotWorking(host.list[0]) }
	if output, err, _ := runSubchat(t, access, false, false, map[string]any{"action": "stop", "id": "s1"}); err == nil || !strings.Contains(err.Error(), "s1 is not working (done): nothing to stop") {
		t.Fatalf("stop a done subchat: %q %v", output, err)
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
	if got := SubchatBranch("Fix the Parser: edge cases!", "168740675f50de73"); got != "subchat/fix-the-parser-edge-cases" {
		t.Fatalf("branch %q", got)
	}
	// A title without a letter or digit a branch keeps: named after the id (#327).
	if got := SubchatBranch("日本語のタスク 🚀", "168740675f50de73"); got != "subchat/16874067" {
		t.Fatalf("non-Latin branch %q", got)
	}
	if got := SubchatBranch("日本語 fix 2", "168740675f50de73"); got != "subchat/fix-2" {
		t.Fatalf("mixed branch %q", got)
	}
}

// A title is one line: newlines, tabs and runs of spaces become one space
// before the cut (#326).
func TestSubchatTitle(t *testing.T) {
	for title, want := range map[string]string{
		"line one\nline two":       "line one line two",
		"  tabs\tand\r\n  spaces ": "tabs and spaces",
		strings.Repeat("ab ", 30):  strings.TrimSpace(strings.Repeat("ab ", 20)),
	} {
		if got := SubchatTitle(title); got != want {
			t.Errorf("SubchatTitle(%q) = %q, want %q", title, got, want)
		}
	}
	host := &fakeSubchats{}
	output, err, _ := runSubchat(t, host.access(""), true, true, map[string]any{"action": "start", "title": "line one\nline two", "brief": "Do it."})
	if err != nil || host.started[0].Title != "line one line two" || !strings.Contains(output, "“line one line two”") {
		t.Fatalf("start = %q, %v; started %+v", output, err, host.started)
	}
}

// A model is checked before the user is asked, and the preview names it
// (#330).
func TestSubchatModelChecked(t *testing.T) {
	host := &fakeSubchats{}
	access := host.access("")
	access.Model = func(_ context.Context, name string) (string, error) {
		if name != "haiku" {
			return "", fmt.Errorf("No model %q: use one of haiku", name)
		}
		return "Claude Haiku 5.5", nil
	}
	_, err, asked := runSubchat(t, access, false, true, map[string]any{"action": "start", "brief": "x", "model": "no-such-model-xyz"})
	if err == nil || !strings.Contains(err.Error(), "No model") || len(asked) != 0 || len(host.started) != 0 {
		t.Fatalf("an unknown model: %v, asked %q, started %v", err, asked, host.started)
	}
	_, err, asked = runSubchat(t, access, false, true, map[string]any{"action": "start", "brief": "x", "model": "haiku"})
	if err != nil || len(asked) != 1 || !strings.Contains(asked[0], "with Claude Haiku 5.5.") || host.started[0].Model != "haiku" {
		t.Fatalf("a known model: %v, asked %q, started %v", err, asked, host.started)
	}
}

// The context a chat takes: its last measured turn, then an estimate (#329).
func TestContextUsed(t *testing.T) {
	history := []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello", Usage: 24000}}
	if got := ContextUsed(history); got != 24000+HistoryEstimate(history[1:]) {
		t.Fatalf("measured: %d", got)
	}
	estimated := []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello", Usage: 50, UsageEstimated: true}}
	if got := ContextUsed(estimated); got != HistoryEstimate(estimated) {
		t.Fatalf("estimated: %d", got)
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
	created, note, err := SubchatWorktree(ctx, gitrepo.Local, dir, "Fix", "s1")
	if err != nil || created.Path != "" || !strings.Contains(note, "shares it") {
		t.Fatalf("outside Git: %+v %q %v", created, note, err)
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	git("add", ".")
	git("commit", "-qm", "a")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644) // uncommitted
	created, note, err = SubchatWorktree(ctx, gitrepo.Local, dir, "Fix", "s1")
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
	again, _, err := SubchatWorktree(ctx, gitrepo.Local, dir, "Fix", "s1")
	if err != nil || again.Branch != "subchat/fix-2" || again.Path == created.Path {
		t.Fatalf("second: %+v %v", again, err)
	}
	// A deleted subchat's worktree goes with its branch, which has no
	// commits of its own (#328); one with uncommitted changes stays.
	if note, err := RemoveSubchatWorktree(ctx, gitrepo.Local, again.Path, false); err != nil || note != "" {
		t.Fatalf("remove: %q %v", note, err)
	}
	if _, err := os.Stat(again.Path); err == nil {
		t.Fatal("the worktree is still there")
	}
	if out, _ := exec.Command("git", "-C", dir, "branch", "--list", "subchat/fix-2").Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the branch is still there: %s", out)
	}
	os.WriteFile(filepath.Join(created.Path, "c.txt"), []byte("c"), 0o644)
	if _, err := RemoveSubchatWorktree(ctx, gitrepo.Local, created.Path, false); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("removed a worktree with changes: %v", err)
	}
}

// A subchat starts subchats of its own (#398), down to MaxSubchatDepth: at
// that level the tool refuses with the limit, before asking the user.
func TestNestedSubchatStart(t *testing.T) {
	host := &fakeSubchats{}
	access := host.access("")
	access.ParentTitle, access.Depth = "Main", 2
	output, err, _ := runSubchat(t, access, true, true, map[string]any{"action": "start", "title": "Deeper", "brief": "Fix the lexer"})
	if err != nil || !strings.Contains(output, "Started subchat s1") || len(host.started) != 1 {
		t.Fatalf("a level-2 subchat's start: %q %v", output, err)
	}
	if text := subchatInstructions(Config{Subchats: access}); !strings.Contains(text, "start subchats of its own") {
		t.Fatalf("level-2 instructions: %q", text)
	}

	access.Depth = MaxSubchatDepth
	_, err, asked := runSubchat(t, access, false, true, map[string]any{"action": "start", "brief": "Fix more"})
	if err == nil || !strings.Contains(err.Error(), "the limit of 3 levels") || len(asked) != 0 || len(host.started) != 1 {
		t.Fatalf("a level-3 subchat's start: %v, asked %d", err, len(asked))
	}
	if text := subchatInstructions(Config{Subchats: access}); strings.Contains(text, "start subchats of its own") {
		t.Fatalf("level-3 instructions: %q", text)
	}
	// The subchat lists its own subchats.
	if output, _, _ := runSubchat(t, access, true, true, map[string]any{"action": "status"}); !strings.Contains(output, "s1 “Deeper”") {
		t.Fatalf("status in a subchat: %q", output)
	}

	parents := map[string]string{"c3": "c2", "c2": "c1", "c1": "c0", "x": "y", "y": "x"}
	if got := SubchatDepth("c3", func(id string) string { return parents[id] }); got != 3 {
		t.Fatalf("depth %d", got)
	}
	if got := SubchatDepth("x", func(id string) string { return parents[id] }); got > MaxSubchatDepth+1 {
		t.Fatalf("a loop's depth %d", got)
	}
}
