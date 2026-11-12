// SPDX-License-Identifier: MPL-2.0

package server

// Subchats on the server (agent/subchats.go): the chats a conversation
// started, their state in its meta for the clients, and the reports their
// turns send back to it.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/gitrepo"
)

func (server *headlessServer) subchatsLocked(parent *serverChat) []*serverChat {
	var out []*serverChat
	for _, chat := range server.chats {
		if chat.Subchat != nil && chat.Subchat.Parent == parent.ID {
			out = append(out, chat)
		}
	}
	return out
}

func (server *headlessServer) parentLocked(chat *serverChat) *serverChat {
	if chat.Subchat == nil {
		return nil
	}
	return server.chatLocked(chat.Subchat.Parent)
}

func (server *headlessServer) subchatInfoLocked(chat *serverChat) agent.SubchatInfo {
	waiting := chat.run != nil && (chat.run.approval != nil || chat.run.question != nil)
	used := agent.ContextUsed(chat.History)
	if chat.run != nil && chat.run.used > 0 {
		used = chat.run.used
	}
	return agent.SubchatInfo{ID: chat.ID, Title: chat.Title, State: agent.SubchatState(chat.run != nil, waiting, chat.Subchat.Ended),
		Folder: chat.Config.Workspace, Branch: chat.Subchat.Branch, Model: server.labelLocked(chat.Config),
		Used: used, Last: agent.LastAnswer(chat.Items)}
}

// subchatWorktreeLocked is the worktree a subchat was given, "" when it
// shares a folder or another chat works there too.
func (server *headlessServer) subchatWorktreeLocked(chat *serverChat) string {
	if chat.Subchat == nil || chat.Subchat.Branch == "" { // a branch: it was given a worktree
		return ""
	}
	for _, other := range server.chats {
		if other != chat && other.Config.Workspace == chat.Config.Workspace {
			return ""
		}
	}
	return chat.Config.Workspace
}

// wakeLocked gives chat a message that is not its user's: a task's end,
// another chat's message or a turn's report (chats.go). It joins the
// running turn, or starts one, which reports to the chats that caused it.
func (server *headlessServer) wakeLocked(chat *serverChat, message agent.Message) {
	if server.chatLocked(chat.ID) != chat || server.closing || server.ctx.Err() != nil {
		return
	}
	parent := ""
	if chat.Subchat != nil {
		parent = chat.Subchat.Parent
	}
	chat.ReportTo = agent.ReportTo(chat.ReportTo, message, parent)
	if chat.run != nil {
		chat.steer = append(chat.steer, message)
		server.changedLocked(chat, -1)
		return
	}
	server.runLocked(chat, message)
}

// subchatAccess is what the chat's subchat tool reaches: its subchats, or
// in a subchat its parent.
func (server *headlessServer) subchatAccess(chat *serverChat) *agent.SubchatAccess {
	access := &agent.SubchatAccess{
		List: func(context.Context) (list []agent.SubchatInfo) {
			server.mu.Lock()
			defer server.mu.Unlock()
			for _, sub := range server.subchatsLocked(chat) {
				list = append(list, server.subchatInfoLocked(sub))
			}
			return list
		},
		Stop: func(_ context.Context, id string) error {
			server.mu.Lock()
			defer server.mu.Unlock()
			sub := server.chatLocked(id)
			if sub == nil || sub.Subchat == nil || sub.Subchat.Parent != chat.ID {
				return fmt.Errorf("No subchat %s here", id)
			}
			if sub.run == nil {
				return agent.SubchatNotWorking(server.subchatInfoLocked(sub))
			}
			sub.run.cancel()
			return nil
		},
	}
	if chat.Subchat != nil { // one level: a subchat talks to its parent only
		access.ParentTitle = "(deleted)"
		if parent := server.parentLocked(chat); parent != nil {
			access.ParentTitle = parent.Title
		}
		return access
	}
	access.Start = func(ctx context.Context, request agent.SubchatRequest) (agent.SubchatInfo, error) {
		return server.startSubchat(ctx, chat, request)
	}
	access.Model = func(_ context.Context, name string) (string, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		found, err := agent.ChooseSubchatModel(server.choices, name, chat.Config)
		return found.Display(), err
	}
	return access
}

// startSubchat makes and starts a subchat of parent, in a worktree of the
// server's project when asked.
func (server *headlessServer) startSubchat(ctx context.Context, parent *serverChat, request agent.SubchatRequest) (agent.SubchatInfo, error) {
	server.mu.Lock()
	if server.chatLocked(parent.ID) != parent {
		server.mu.Unlock()
		return agent.SubchatInfo{}, fmt.Errorf("The conversation was deleted")
	}
	var choice *agent.ModelChoice
	if request.Model != "" {
		found, err := agent.ChooseSubchatModel(server.choices, request.Model, parent.Config)
		if err != nil {
			server.mu.Unlock()
			return agent.SubchatInfo{}, err
		}
		choice = &found
	}
	cfg := agent.SubchatConfig(parent.Config, choice)
	var history []agent.Message
	var items []agent.ChatItem
	if request.Context == "fork" {
		history = agent.ForkHistory(parent.History)
		items = slices.Clone(parent.Items)
	}
	project := cfg.Workspace
	if server.worktreeLocked(project) {
		project = server.worktrees[project]
	}
	server.mu.Unlock()

	id := core.Identifier()[:16]
	var created gitrepo.Created
	note := ""
	if request.Isolation == "worktree" {
		var err error
		if created, note, err = agent.SubchatWorktree(ctx, gitrepo.Local, cfg.Workspace, request.Title, id); err != nil {
			return agent.SubchatInfo{}, fmt.Errorf("Could not make the subchat's worktree: %v", err)
		}
		if created.Path != "" {
			gitTarget{server}.Opened(project, created.Path)
			cfg.Workspace = created.Path
		}
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.chatLocked(parent.ID) != parent {
		if created.Path != "" { // no subchat works in it: it goes
			go func() {
				if _, err := agent.RemoveSubchatWorktree(context.WithoutCancel(ctx), gitrepo.Local, created.Path, true); err == nil {
					gitTarget{server}.Removed(project, created.Path)
				}
			}()
		}
		return agent.SubchatInfo{}, fmt.Errorf("The conversation was deleted")
	}
	sub := &serverChat{ID: id, Title: request.Title, Config: cfg, History: history, Items: items,
		Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Subchat: &agent.SubchatLink{Parent: parent.ID, Branch: created.Branch}, ReportTo: []string{parent.ID}}
	if request.Context == "fork" {
		server.addLocked(sub, agent.ChatItem{Kind: "note", Text: "Forked from “" + parent.Title + "”"})
	}
	server.chats = append([]*serverChat{sub}, server.chats...)
	server.runLocked(sub, agent.Message{Role: "user", Content: request.Brief})
	server.broadcastLocked("meta", server.metaLocked(parent))
	server.scheduleSaveLocked()
	info := server.subchatInfoLocked(sub)
	info.Note = note
	return info, nil
}

// subchatCommandLocked is /subchat <brief> sent in a chat: the user splits
// work off it, fresh and in its own worktree. The worktree is made in the
// background; the reply comes at once.
func (server *headlessServer) subchatCommandLocked(parent *serverChat, brief string) error {
	brief = strings.TrimSpace(brief)
	switch {
	case brief == "":
		return fmt.Errorf("Write what the subchat should do after /subchat: the task, the files and what done means")
	case parent == nil:
		return fmt.Errorf("Open the conversation to start the subchat from first")
	case parent.Subchat != nil:
		return fmt.Errorf("A subchat cannot have subchats of its own")
	}
	running := 0
	for _, sub := range server.subchatsLocked(parent) {
		if agent.SubchatActive(server.subchatInfoLocked(sub)) {
			running++
		}
	}
	if running >= agent.MaxRunningSubchats {
		return fmt.Errorf("%d subchats of this chat are already working, the most at once", running)
	}
	title := []rune(agent.FirstLine(brief))
	if len(title) > 50 {
		title = title[:50]
	}
	request := agent.SubchatRequest{Title: string(title), Brief: brief, Context: "fresh", Isolation: "worktree"}
	go func() {
		if _, err := server.startSubchat(server.ctx, parent, request); err != nil {
			server.mu.Lock()
			defer server.mu.Unlock()
			if server.chatLocked(parent.ID) == parent {
				index := server.addLocked(parent, agent.ChatItem{Kind: "error", Text: "/subchat: " + err.Error()})
				server.changedLocked(parent, index)
			}
		}
	}()
	return nil
}
