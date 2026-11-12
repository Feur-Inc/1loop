// SPDX-License-Identifier: MPL-2.0

package agent

// Subchats: a conversation starts others for work that runs on its own (a
// separate fix, a follow-up, an investigation) while the user keeps talking
// in it. A subchat is an ordinary chat with a parent: the user opens, steers
// or stops it like any other. Its turns that the parent caused (the brief, a
// parent's message, its own background task) report to the parent when they
// end, as a background task's end does. A subchat starts no subchat of its
// own, and a chat runs at most MaxRunningSubchats at once.
//
// The host (the desktop or the server) keeps the chats: SubchatAccess is
// what the tool reaches of it.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"1loop/internal/core"
	"1loop/internal/gitrepo"
)

// MaxRunningSubchats is how many subchats of one chat work at once.
const MaxRunningSubchats = 4

// The states of a subchat, as the tool, the panel and the phone show them.
const (
	SubchatWorking = "working"
	SubchatWaiting = "waiting" // for the user: an approval or a question
	SubchatDone    = "done"
	SubchatFailed  = "failed"
	SubchatStopped = "stopped"
)

// SubchatRequest is a subchat to start. Context is "fresh" (the brief only)
// or "fork" (the conversation so far, then the brief); Isolation is
// "worktree" (its own Git worktree and branch) or "shared" (the parent's
// folder); Model is a model of the picker, "" for the parent's.
type SubchatRequest struct {
	Title, Brief, Context, Isolation, Model string
}

// SubchatInfo is a subchat as its parent sees it.
type SubchatInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Folder string `json:"folder,omitempty"`
	Branch string `json:"branch,omitempty"`
	Model  string `json:"model,omitempty"`
	Used   int    `json:"used,omitempty"` // its context, in tokens
	Last   string `json:"-"`              // its last answer
	Note   string `json:"-"`              // what start says besides (a folder shared for want of Git)
}

// SubchatLink is what a subchat keeps of its parent. Who its turn reports
// to is the chat's ReportTo, as for any chat (chats.go).
type SubchatLink struct {
	Parent string `json:"parent"`
	Branch string `json:"branch,omitempty"` // the worktree's branch, "" when it shares the folder
	Ended  string `json:"ended,omitempty"`  // how its last turn ended: done, stopped or failed
}

// Copy is the link as it is now, for a save encoded beside the chat's
// thread; nil for a chat that is no subchat.
func (link *SubchatLink) Copy() *SubchatLink {
	if link == nil {
		return nil
	}
	saved := *link
	return &saved
}

// SubchatAccess is the host's side of the subchat tool. ParentTitle is set
// in a subchat, whose Start is nil. Messages go through the chats tool
// (chats.go). Model finds a model of the picker before the user is asked to
// start a subchat with it, and gives its name; Stop fails for a subchat
// that is not working.
type SubchatAccess struct {
	ParentTitle string
	Start       func(ctx context.Context, request SubchatRequest) (SubchatInfo, error)
	Model       func(ctx context.Context, name string) (string, error)
	List        func(ctx context.Context) []SubchatInfo
	Stop        func(ctx context.Context, id string) error
}

// Restored is the link as loaded at start. cutOff: the chat's turn was to
// report when 1loop closed (its ReportTo was saved), so it shows stopped;
// the host forgets that ReportTo, so a turn the user starts later does not
// report in its place.
func (link *SubchatLink) Restored(cutOff bool) {
	if link != nil && cutOff {
		link.Ended = SubchatStopped
	}
}

// SubchatState is a subchat's state from its chat's.
func SubchatState(running, waiting bool, ended string) string {
	switch {
	case waiting:
		return SubchatWaiting
	case running:
		return SubchatWorking
	case ended == SubchatFailed || ended == SubchatStopped:
		return ended
	}
	return SubchatDone
}

// SubchatActive says whether a subchat counts against MaxRunningSubchats.
func SubchatActive(info SubchatInfo) bool {
	return info.State == SubchatWorking || info.State == SubchatWaiting
}

// SubchatTitle is a subchat's title on one line: control characters and
// runs of spaces become one space, cut at 60 characters.
func SubchatTitle(title string) string {
	title = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)), " ")
	if runes := []rune(title); len(runes) > 60 {
		title = strings.TrimSpace(string(runes[:60]))
	}
	return title
}

// SubchatBranch is the branch of a subchat's worktree, named after its
// title, or after its id when the title has no letter or digit a branch
// can keep (日本語のタスク).
func SubchatBranch(title, id string) string {
	if !strings.ContainsFunc(title, func(r rune) bool { return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)) }) {
		return "subchat/" + id[:min(8, len(id))]
	}
	slug := strings.ToLower(gitrepo.Slug(title))
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-.")
	}
	return "subchat/" + slug
}

// SubchatNotWorking is Stop's error for a subchat with no turn to stop.
func SubchatNotWorking(info SubchatInfo) error {
	return fmt.Errorf("Subchat %s is not working (%s): nothing to stop", info.ID, info.State)
}

// ContextUsed is the context a chat's history takes: what its last turn
// measured (the system prompt, the tools and a Claude Code session
// included) and an estimate of what came after, or the estimate of it all
// when no turn measured it.
func ContextUsed(history []Message) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Usage > 0 && !history[i].UsageEstimated {
			return history[i].Usage + HistoryEstimate(history[i:])
		}
	}
	return HistoryEstimate(history)
}

// LastAnswer is a chat's last answer, for its report.
func LastAnswer(items []ChatItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Kind == "assistant" && strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
		if items[i].Kind == "user" {
			break // the turn wrote nothing
		}
	}
	return ""
}

func subchatPlace(info SubchatInfo) string {
	if info.Branch != "" {
		return "Branch " + info.Branch + " · worktree " + info.Folder
	}
	if info.Folder != "" {
		return "Folder " + info.Folder + " (shared)"
	}
	return ""
}

// ChooseSubchatModel finds a model of the picker by its id or name, the
// parent's connection first. The error lists the models there are.
func ChooseSubchatModel(choices map[string]ModelChoice, want string, parent Config) (ModelChoice, error) {
	want = strings.TrimSpace(want)
	var found []ModelChoice
	var names []string
	for label, choice := range choices {
		if !choice.Connection.Enabled {
			continue
		}
		names = append(names, choice.ID)
		if strings.EqualFold(choice.ID, want) || strings.EqualFold(choice.Display(), want) || strings.EqualFold(label, want) {
			found = append(found, choice)
		}
	}
	slices.SortFunc(found, func(a, b ModelChoice) int {
		if (a.Connection.ID == parent.ConnectionID) != (b.Connection.ID == parent.ConnectionID) {
			if a.Connection.ID == parent.ConnectionID {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Connection.ID, b.Connection.ID)
	})
	if len(found) > 0 {
		return found[0], nil
	}
	slices.Sort(names)
	names = slices.Compact(names)
	return ModelChoice{}, fmt.Errorf("No model %q: use one of %s, or null for this chat's", want, strings.Join(names, ", "))
}

// SubchatConfig is a new subchat's configuration: the parent's, or with
// another model; never its sessions, which belong to the parent.
func SubchatConfig(parent Config, choice *ModelChoice) Config {
	cfg := parent
	if choice != nil {
		cfg = choice.Connection.Config(parent.Workspace)
		cfg.MachineID = parent.MachineID
		cfg.Model = choice.ID
		cfg.DefaultPreset = choice.IsDefaultPreset()
		cfg.ReasoningMode = choice.ReasoningMode
		cfg.Effort = parent.Effort
		if !Contains(choice.Efforts, cfg.Effort) {
			cfg.Effort = choice.DefaultEffort
		}
		cfg.ServiceTier = TierFor(*choice, parent.ServiceTier)
		options := choice.Connection.ModelOptions[choice.ID]
		cfg.Parameters = MergeOptions(cfg.Parameters, options.Parameters)
		cfg.Routing = MergeOptions(cfg.Routing, options.Provider)
	}
	cfg.ClearSessions()
	cfg.RanWith, cfg.WebConversation = nil, nil
	cfg.Scratch = "" // a scratch folder is its parent's: deleting the subchat must not delete it
	return cfg
}

// ForkHistory is what a forked subchat starts from: the parent's messages,
// without a turn still open (a tool call waiting for its result).
func ForkHistory(history []Message) []Message {
	out := append([]Message(nil), history...)
	for len(out) > 0 {
		last := out[len(out)-1]
		if last.Role == "assistant" && len(last.ToolCalls) == 0 {
			break
		}
		if last.Role == "user" && !last.Internal {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}

func subchatToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name": "subchat",
		"description": "Start and follow subchats: separate conversations that run in parallel with this one, for work that should go on by itself (a separate fix, a follow-up, a long investigation) while the user keeps talking here. The user sees each subchat in the sidebar under this chat and can open, steer or stop it. " +
			"start proposes one (the user approves it, depending on the approval mode) and returns at once with its id: title is short; brief is everything it needs, since it does not see this conversation unless context is fork: the task, the files, what done means. context: fresh (null) or fork (it gets this conversation so far). isolation: worktree (null: its own Git worktree and branch, from this folder's last commit; uncommitted changes are not in it) or shared (this folder, for read-only work). model: a model name from the model picker, null for this chat's. " +
			"When a subchat's turn ends you receive its report (its last answer, branch) as a message: do not poll status in a loop. To send a subchat a follow-up, or in a subchat to ask the parent, use the chats tool (action message, id parent for the parent). status lists the subchats (id null) or one, with state, branch, last answer and context use. stop stops a subchat's turn. " +
			"Use subchats for independent pieces of work, not for a quick step you can do here; a subchat cannot start subchats, and at most 4 work at once. Leave unused fields null.",
		"parameters": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"action", "id", "title", "brief", "text", "context", "isolation", "model"},
			"properties": map[string]any{
				"action":    map[string]any{"type": "string", "enum": []string{"start", "status", "stop"}},
				"id":        map[string]any{"type": []string{"string", "null"}, "description": "status, stop: the subchat's id."},
				"title":     map[string]any{"type": []string{"string", "null"}, "description": "start: a short title."},
				"brief":     map[string]any{"type": []string{"string", "null"}, "description": "start: the task, the files and what done means."},
				"text":      map[string]any{"type": []string{"string", "null"}, "description": "Unused: messages go through the chats tool."},
				"context":   map[string]any{"type": []string{"string", "null"}, "enum": []any{"fresh", "fork", nil}},
				"isolation": map[string]any{"type": []string{"string", "null"}, "enum": []any{"worktree", "shared", nil}},
				"model":     map[string]any{"type": []string{"string", "null"}},
			}},
	}}
}

func subchatInstructions(cfg Config) string {
	if !cfg.toolAvailable("subchat") || (cfg.Provider == "claudecode" && cfg.Todo == nil) { // no MCP server
		return ""
	}
	tool, chats := "subchat", "chats"
	if cfg.Provider == "claudecode" {
		tool, chats = "mcp__1loop_todo__subchat", "mcp__1loop_todo__chats"
	}
	if cfg.Subchats.ParentTitle != "" {
		return "\n\nThis chat is a subchat of the conversation “" + cfg.Subchats.ParentTitle + "”: its first message is the brief from there. When a turn the parent asked for ends, your last answer is reported to it, so end with a short summary of what you did and where (branch, files). When you are blocked on something only the parent can decide, ask it with " + chats + " (action message, id parent) and end your turn: its answer comes back to you. Messages starting with [Message from the parent conversation come from it."
	}
	return "\n\nSubchats: for a separate piece of work that can run by itself while the user goes on here, start a subchat with " + tool + " rather than doing it in this chat; you are told when its turn ends."
}

// subchatTool answers the subchat tool.
func (backend *App) subchatTool(ctx context.Context, arguments string, begin func(), emit emitter) (string, error) {
	cfg, _ := ctx.Value(configContextKey{}).(Config)
	if cfg.Subchats == nil {
		return "", fmt.Errorf("Subchats are not available in this chat")
	}
	access := *cfg.Subchats
	var args struct {
		Action                                            string
		ID, Title, Brief, Text, Context, Isolation, Model *string
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	value := func(field *string) string {
		if field == nil {
			return ""
		}
		return strings.TrimSpace(*field)
	}
	id := value(args.ID)
	switch args.Action {
	case "start":
		if access.Start == nil {
			return "", fmt.Errorf("A subchat cannot start subchats: ask the parent (chats tool, action message, id parent) if more work should split off")
		}
		request := SubchatRequest{Title: value(args.Title), Brief: value(args.Brief), Context: value(args.Context), Isolation: value(args.Isolation), Model: value(args.Model)}
		if request.Brief == "" {
			return "", fmt.Errorf("Give the subchat a brief: the task, the files and what done means")
		}
		request.Title = SubchatTitle(request.Title)
		if request.Title == "" {
			request.Title = SubchatTitle(FirstLine(request.Brief))
		}
		if request.Context == "" {
			request.Context = "fresh"
		}
		if request.Isolation == "" {
			request.Isolation = "worktree"
		}
		if request.Context != "fresh" && request.Context != "fork" {
			return "", fmt.Errorf("context must be fresh or fork")
		}
		if request.Isolation != "worktree" && request.Isolation != "shared" {
			return "", fmt.Errorf("isolation must be worktree or shared")
		}
		if utf8.RuneCountInString(request.Brief) > 20000 {
			return "", fmt.Errorf("The brief is too long: keep it under 20000 characters, and point at files instead")
		}
		running := 0
		for _, info := range access.List(ctx) {
			if SubchatActive(info) {
				running++
			}
		}
		if running >= MaxRunningSubchats {
			return "", fmt.Errorf("%d subchats are already working, the most at once: wait for a report, or stop one", running)
		}
		place := "in its own worktree and branch"
		if request.Isolation == "shared" {
			place = "in this chat's folder"
		}
		model := "this chat's model"
		if request.Model != "" {
			model = request.Model
			if access.Model != nil { // checked now: the user only approves a start that can run
				name, err := access.Model(ctx, request.Model)
				if err != nil {
					return "", err
				}
				if name != "" {
					model = name
				}
			}
		}
		start := "fresh, with the brief only"
		if request.Context == "fork" {
			start = "with this conversation so far"
		}
		preview := fmt.Sprintf("%s\n\nStarts %s, %s, with %s.\n\n%s", request.Title, start, place, model, request.Brief)
		if !backend.confirm(ctx, emit, "Start a subchat?", preview) {
			return "", fmt.Errorf("The user did not start the subchat")
		}
		begin()
		info, err := access.Start(ctx, request)
		if err != nil {
			return "", err
		}
		out := fmt.Sprintf("Started subchat %s “%s”. %s\nIt works in parallel; you will receive its report when its turn ends.", info.ID, info.Title, subchatPlace(info))
		if info.Note != "" {
			out += "\n" + info.Note
		}
		return out, nil
	case "message": // the chats tool's now; kept for calls written before it
		message, _ := json.Marshal(map[string]*string{"id": args.ID, "text": args.Text})
		return backend.chatsTool(ctx, `{"action":"message",`+string(message[1:]), begin, emit)
	case "status":
		begin()
		list := access.List(ctx)
		if access.ParentTitle != "" && id == "" {
			return "This chat is a subchat of “" + access.ParentTitle + "” and has no subchats of its own.", nil
		}
		var lines []string
		total := 0
		for _, info := range list {
			if id != "" && info.ID != id {
				continue
			}
			total += info.Used
			line := fmt.Sprintf("%s “%s” · %s", info.ID, info.Title, info.State)
			if info.Model != "" {
				line += " · " + info.Model
			}
			if info.Used > 0 {
				line += fmt.Sprintf(" · %d tokens of context", info.Used)
			}
			if place := subchatPlace(info); place != "" {
				line += "\n  " + place
			}
			if id != "" {
				last := info.Last
				if last == "" {
					last = "(no answer yet)"
				}
				line += "\n  Last answer: " + core.TruncateText(last, 4000)
			}
			lines = append(lines, line)
		}
		if id != "" && len(lines) == 0 {
			return "", fmt.Errorf("No subchat %s here", id)
		}
		if len(lines) == 0 {
			return "No subchat yet.", nil
		}
		if id == "" && len(lines) > 1 {
			lines = append(lines, fmt.Sprintf("Together: %d tokens of context.", total))
		}
		return strings.Join(lines, "\n"), nil
	case "stop":
		if id == "" || id == "parent" {
			return "", fmt.Errorf("Give the id of one of this chat's subchats")
		}
		begin()
		if err := access.Stop(ctx, id); err != nil {
			return "", err
		}
		return "Stopped subchat " + id + ".", nil
	}
	return "", fmt.Errorf("Unknown action %q", args.Action)
}

// SubchatWorktree makes the worktree of subchat id: a new branch named
// after its title, from the commit folder is on. A folder outside Git, or
// without a commit yet, is shared instead: the note says so.
func SubchatWorktree(ctx context.Context, host gitrepo.Host, folder, title, id string) (created gitrepo.Created, note string, err error) {
	head, err := host.Git(ctx, folder, "rev-parse", "HEAD")
	if err != nil {
		return gitrepo.Created{}, "This folder is not a Git repository with a commit, so the subchat shares it.", nil
	}
	if status, err := host.Git(ctx, folder, "status", "--porcelain"); err == nil && strings.TrimSpace(status) != "" {
		note = "This folder has uncommitted changes, which the subchat's worktree does not have."
	}
	base := SubchatBranch(title, id)
	for n := 1; n <= 20; n++ {
		branch := base
		if n > 1 {
			branch = fmt.Sprintf("%s-%d", base, n)
		}
		created, err = gitrepo.AddWorktree(ctx, host, folder, gitrepo.NewWorktree{Kind: "new", Branch: branch, Base: strings.TrimSpace(head)})
		if (err == nil && created.Existing) || (err != nil && strings.Contains(err.Error(), "already exists")) {
			continue // the name is taken: the next one
		}
		return created, note, err
	}
	return gitrepo.Created{}, "", fmt.Errorf("No free branch name for %s", base)
}

// RemoveSubchatWorktree removes a deleted subchat's worktree and its
// branch. Without force, a worktree with uncommitted changes stays, and so
// does a branch with commits not merged: the note says what was kept.
func RemoveSubchatWorktree(ctx context.Context, host gitrepo.Host, folder string, force bool) (string, error) {
	return gitrepo.RemoveWorktree(ctx, host, folder, gitrepo.Removal{Path: folder, Force: force, DeleteBranch: true})
}
