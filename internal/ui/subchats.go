// SPDX-License-Identifier: MPL-2.0

package ui

// Subchats on this computer (agent/subchats.go): the chats a conversation
// started, listed under it in the sidebar and in its panel's Tasks tab, and
// the reports their turns send back to it.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/gitrepo"
)

var iconSubchat = lineIcon("subchat", `<path d="M3 3.5h7.5a1.5 1.5 0 0 1 1.5 1.5v3a1.5 1.5 0 0 1-1.5 1.5H7l-2.5 2v-2H3A1.5 1.5 0 0 1 1.5 8V5A1.5 1.5 0 0 1 3 3.5z"/><path d="M9 12.5h2.5l2 1.5v-1.5h.5a1 1 0 0 0 1-1v-2"/>`)

// subchatsOf are a chat's subchats, the newest first.
func (ui *desktopUI) subchatsOf(parent *conversation) []*conversation {
	var out []*conversation
	for _, chat := range ui.chats {
		if chat.Subchat != nil && chat.Subchat.Parent == parent.ID {
			out = append(out, chat)
		}
	}
	return out
}

// parentOf is a subchat's parent, nil for another chat or a deleted parent.
func (ui *desktopUI) parentOf(chat *conversation) *conversation {
	if chat == nil || chat.Subchat == nil {
		return nil
	}
	return ui.chatByID(chat.Subchat.Parent)
}

func (ui *desktopUI) subchatInfo(chat *conversation) agent.SubchatInfo {
	link := chat.Subchat
	waiting := chat.run != nil && (chat.run.approvalID != "" || chat.run.questionID != "")
	used := agent.ContextUsed(chat.History)
	if chat.run != nil && chat.run.turn != nil && chat.run.turn.usage > 0 {
		used = chat.run.turn.usage
	}
	return agent.SubchatInfo{ID: chat.ID, Title: chat.Title, State: agent.SubchatState(chat.run != nil, waiting, link.Ended),
		Folder: chat.Config.Workspace, Branch: link.Branch, Model: ui.modelName(chat.Config),
		Used: used, Last: agent.LastAnswer(chat.Items), Provider: chat.Config.Provider}
}

// subchatDepth is a chat's level: 0 for a chat the user started, 1 for
// its subchat, 2 for theirs (agent.MaxSubchatDepth).
func (ui *desktopUI) subchatDepth(chat *conversation) int {
	return agent.SubchatDepth(chat.ID, func(id string) string {
		if found := ui.chatByID(id); found != nil && found.Subchat != nil && ui.chatByID(found.Subchat.Parent) != nil {
			return found.Subchat.Parent
		}
		return ""
	})
}

// subchatAccess is what the chat's subchat tool reaches: its subchats, and
// in a subchat its parent's title.
func (ui *desktopUI) subchatAccess(chat *conversation) *agent.SubchatAccess {
	access := &agent.SubchatAccess{
		List: func(ctx context.Context) (list []agent.SubchatInfo) {
			ui.onUI(ctx, func() {
				for _, sub := range ui.subchatsOf(chat) {
					list = append(list, ui.subchatInfo(sub))
				}
			})
			return list
		},
		Stop: func(ctx context.Context, id string) (err error) {
			ui.onUI(ctx, func() {
				sub := ui.chatByID(id)
				if sub == nil || sub.Subchat == nil || sub.Subchat.Parent != chat.ID {
					err = fmt.Errorf("No subchat %s here", id)
					return
				}
				if sub.run == nil && !ui.treeWorking(sub) {
					err = agent.SubchatNotWorking(ui.subchatInfo(sub))
					return
				}
				ui.stopSubchat(sub)
			})
			return err
		},
	}
	if chat.Subchat != nil {
		access.ParentTitle = "(deleted)"
		if parent := ui.parentOf(chat); parent != nil {
			access.ParentTitle = parent.Title
		}
		access.Depth = ui.subchatDepth(chat)
	}
	if agent.SubchatLimit(access.Depth) != "" { // the deepest level: no subchat of its own
		return access
	}
	access.Start = func(ctx context.Context, request agent.SubchatRequest) (agent.SubchatInfo, error) {
		return ui.startSubchat(ctx, chat, request)
	}
	access.Model = func(ctx context.Context, name string) (display string, err error) {
		ui.onUI(ctx, func() {
			var found agent.ModelChoice
			if found, err = agent.ChooseSubchatModel(ui.choices, name, chat.Config); err == nil {
				display = found.Display()
			}
		})
		return display, err
	}
	return access
}

// startSubchat makes and starts a subchat of parent. It runs off the UI
// thread: making its worktree takes a moment.
func (ui *desktopUI) startSubchat(ctx context.Context, parent *conversation, request agent.SubchatRequest) (info agent.SubchatInfo, err error) {
	var cfg agent.Config
	var history []agent.Message
	var items []agent.ChatItem
	var machine agent.Machine
	ui.onUI(ctx, func() {
		if ui.chatByID(parent.ID) != parent {
			err = fmt.Errorf("The conversation was deleted")
			return
		}
		var choice *agent.ModelChoice
		if request.Model != "" {
			found, problem := agent.ChooseSubchatModel(ui.choices, request.Model, parent.Config)
			if problem != nil {
				err = problem
				return
			}
			choice = &found
		}
		cfg = agent.SubchatConfig(parent.Config, choice)
		machine = ui.attachmentMachine(cfg.MachineID)
		if request.Context == "fork" {
			history = agent.ForkHistory(parent.History)
			items = slices.Clone(parent.Items)
		}
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return info, err
	}
	id := core.Identifier()[:16]
	var created gitrepo.Created
	note := ""
	if request.Isolation == "worktree" {
		if cfg.MachineID != "" {
			note = "Worktrees are not made on SSH machines yet, so the subchat shares this folder."
		} else if created, note, err = agent.SubchatWorktree(ctx, gitrepo.Local, cfg.Workspace, request.Title, id); err != nil {
			return info, fmt.Errorf("Could not make the subchat's worktree: %v", err)
		}
	}
	var files []agent.FileAttachment
	if err == nil && len(request.Attachments.Files) > 0 { // copied into its workspace, as the composer's
		workspace := cfg.Workspace
		if created.Path != "" {
			workspace = created.Path
		}
		files, err = agent.DeliverFiles(ctx, machine, workspace, request.Attachments.Files)
	}
	ui.onUI(ctx, func() {
		if err != nil {
			return
		}
		if ui.chatByID(parent.ID) != parent {
			err = fmt.Errorf("The conversation was deleted")
			return
		}
		sub := &conversation{ID: id, Title: request.Title, Config: cfg, History: history, Items: items,
			Subchat: &agent.SubchatLink{Parent: parent.ID, Branch: created.Branch}, ReportTo: []string{parent.ID}}
		if created.Path != "" {
			if ui.worktrees == nil {
				ui.worktrees = map[string]string{}
			}
			ui.worktrees[created.Path] = ui.projectOf(cfg.Workspace)
			sub.Config.Workspace = created.Path
		}
		if request.Context == "fork" {
			sub.Items = append(sub.Items, agent.ChatItem{Kind: "note", Text: "Forked from “" + parent.Title + "”"})
		}
		ui.chats = append([]*conversation{sub}, ui.chats...)
		ui.startIn(sub, agent.PromptMessage(request.Brief, request.Attachments.Images, files), "")
		ui.panel.showTodo(parent) // its live state, beside the parent
		ui.panel.todoChanged(parent)
		info = ui.subchatInfo(sub)
		info.Note = note
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil && created.Path != "" && info.ID == "" { // no subchat works in it: it goes
		go func() {
			_, _ = agent.RemoveSubchatWorktree(context.WithoutCancel(ctx), gitrepo.Local, created.Path, true)
		}()
	}
	return info, err
}

// stopSubchat stops a subchat's turn, and those of the subchats under it.
func (ui *desktopUI) stopSubchat(sub *conversation) {
	ui.stopTree(sub, 0)
}

func (ui *desktopUI) stopTree(chat *conversation, depth int) {
	if chat.run != nil {
		chat.run.cancel()
	}
	if depth > agent.MaxSubchatDepth { // a loop of links: stop there
		return
	}
	for _, sub := range ui.subchatsOf(chat) {
		ui.stopTree(sub, depth+1)
	}
}

// subchatWorktree is the worktree a subchat of this computer was given,
// "" when it shares a folder or another chat works there too.
func (ui *desktopUI) subchatWorktree(chat *conversation) string {
	if chat.Subchat == nil || chat.Subchat.Branch == "" || chat.Config.MachineID != "" || ui.worktrees[chat.Config.Workspace] == "" {
		return ""
	}
	for _, other := range ui.chats {
		if other != chat && other.Config.Workspace == chat.Config.Workspace && other.Config.MachineID == "" {
			return ""
		}
	}
	return chat.Config.Workspace
}

// confirmDeleteSubchat asks before deleting a subchat with its own
// worktree, and removes the worktree and its branch with it unless the
// user keeps them.
func (ui *desktopUI) confirmDeleteSubchat(chat *conversation, text string) {
	folder := ui.subchatWorktree(chat)
	project := ui.worktrees[folder]
	removeWorktree := true
	body := container.New(layout.NewCustomPaddedVBoxLayout(10), note(text),
		card(settingRow("Also remove its worktree and branch "+chat.Subchat.Branch,
			"The folder "+worktreeFolder(folder)+" goes unless it has uncommitted changes; the branch only if it is merged or has no commits of its own.",
			newToggle(true, func(on bool) { removeWorktree = on }))))
	var dialog *sheet
	remove := newRow("Delete", nil, stylePrimary, func() {
		dialog.Hide()
		ui.removeChat(chat)
		if !removeWorktree {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, time.Minute)
			kept, err := agent.RemoveSubchatWorktree(ctx, gitrepo.Local, folder, false)
			cancel()
			fyne.Do(func() {
				if err != nil {
					ui.notice("Worktree kept", folder+": "+err.Error())
					return
				}
				ui.forgetLocalWorktree(project, folder)
				if kept != "" {
					ui.notice("Worktree removed", kept)
				}
			})
		}()
	})
	remove.danger = true
	dialog = ui.modal("Delete chat?", body, 500, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }), remove)
}

// worktreeFolder is a worktree's folder from its project's: .worktrees/…
func worktreeFolder(folder string) string {
	marker := string(filepath.Separator) + gitrepo.Folder + string(filepath.Separator)
	if i := strings.LastIndex(folder, marker); i >= 0 {
		return folder[i+1:]
	}
	return folder
}

// subchatCommand is /subchat <brief> in the composer.
func (ui *desktopUI) subchatCommand(brief string) {
	if problem := ui.splitOff(ui.current, brief); problem != "" {
		ui.notice("/subchat", problem)
		return
	}
	ui.prompt.clearSent()
}

// splitOff starts a subchat of parent for /subchat <brief>: the user splits
// work off it, fresh and in its own worktree. It says why not, if it cannot.
func (ui *desktopUI) splitOff(parent *conversation, brief string) string {
	brief = strings.TrimSpace(brief)
	switch {
	case brief == "":
		return "Write what the subchat should do after /subchat: the task, the files and what done means."
	case parent == nil:
		return "Open the conversation to start the subchat from first."
	case agent.SubchatLimit(ui.subchatDepth(parent)) != "":
		return fmt.Sprintf("This subchat is %d levels down, the most: start it from “%s”.", agent.MaxSubchatDepth, parentTitle(ui, parent))
	}
	running := 0
	for _, sub := range ui.subchatsOf(parent) {
		if agent.SubchatActive(ui.subchatInfo(sub)) {
			running++
		}
	}
	if running >= agent.MaxRunningSubchats {
		return fmt.Sprintf("%d subchats of this chat are already working, the most at once.", running)
	}
	title := []rune(agent.FirstLine(brief))
	if len(title) > 36 {
		title = title[:36]
	}
	request := agent.SubchatRequest{Title: string(title), Brief: brief, Context: "fresh", Isolation: "worktree"}
	go func() {
		if _, err := ui.startSubchat(ui.ctx, parent, request); err != nil {
			fyne.Do(func() { ui.notice("/subchat", err.Error()) })
		}
	}()
	return ""
}

// treeWorking says whether a subchat under chat, at any depth, is working.
func (ui *desktopUI) treeWorking(chat *conversation) bool {
	working := false
	var walk func(*conversation, int)
	walk = func(parent *conversation, depth int) {
		for _, sub := range ui.subchatsOf(parent) {
			if sub.run != nil {
				working = true
			}
			if depth < agent.MaxSubchatDepth {
				walk(sub, depth+1)
			}
		}
	}
	walk(chat, 0)
	return working
}

func parentTitle(ui *desktopUI, chat *conversation) string {
	if parent := ui.parentOf(chat); parent != nil {
		return parent.Title
	}
	return "its parent"
}

// startedSubchat is the subchat a start call made, from its result.
func startedSubchat(item agent.ChatItem) string {
	rest, ok := strings.CutPrefix(item.Text, "Started subchat ")
	if !ok || item.Tool != "subchat" {
		return ""
	}
	id, _, _ := strings.Cut(rest, " ")
	return id
}

// addSubchats lists a chat's subchats in its Tasks tab, after its
// background commands: their state, and Open and Stop.
func (panel *toolPanel) addSubchats(box *fyne.Container, chat *conversation) {
	if chat == nil {
		return
	}
	ui := panel.ui
	subs := ui.subchatsOf(chat)
	if len(subs) == 0 {
		return
	}
	title := textLine("Subchats", 16, colText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	total := 0
	for _, sub := range subs {
		total += ui.subchatInfo(sub).Used
	}
	box.Add(padded(22, 6, 0, 0, container.NewBorder(nil, nil, title, textLine(fmt.Sprintf("%s tokens together", compactTokens(total)), 12.5, colMuted))))
	for _, sub := range subs {
		sub := sub
		info := ui.subchatInfo(sub)
		status, state := "in_progress", "Working"
		switch info.State {
		case agent.SubchatWaiting:
			state = "Waiting for you"
		case agent.SubchatDone:
			status, state = "completed", "Done"
		case agent.SubchatFailed:
			status, state = "pending", "Failed"
		case agent.SubchatStopped:
			status, state = "pending", "Stopped"
		}
		if info.Branch != "" {
			state += " · " + info.Branch
		}
		mark := newTaskMark(status, func() bool { return false })
		actions := container.NewHBox(newRow("Open", iconSubchat, styleChip, func() { ui.openChat(sub) }))
		if sub.run != nil {
			actions.Add(newRow("Stop", iconStop, styleChip, func() { ui.stopSubchat(sub) }))
		}
		name := newFitLabel(sub.Title, 13.5, colText)
		box.Add(padded(7, 7, 0, 0, container.NewBorder(nil, nil, container.NewVBox(mark), container.NewVBox(actions),
			padded(1, 0, 12, 8, container.NewVBox(name, textLine(state, 11.5, colMuted))))))
	}
	box.Add(layout.NewSpacer())
}

// compactTokens is a token count at a glance: 950, 12k, 1.2M.
func compactTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// openSubchat opens a subchat from its parent's start call: on this
// computer, or on the server the parent is on.
func (ui *desktopUI) openSubchat(parent *conversation, id string) {
	if parent.server != nil {
		parent.server.openChat(id)
		return
	}
	if sub := ui.chatByID(id); sub != nil {
		ui.openChat(sub)
	}
}

// openParent opens the open subchat's parent.
func (ui *desktopUI) openParent() {
	chat := ui.current
	if chat == nil {
		return
	}
	if chat.server != nil {
		if parent := chat.server.parentOf(chat.serverID); parent != "" {
			chat.server.openChat(parent)
		}
		return
	}
	if parent := ui.parentOf(chat); parent != nil {
		ui.openChat(parent)
	}
}

// showParentLink shows "Subchat of …" beside an open subchat's title.
func (ui *desktopUI) showParentLink() {
	if ui.parentLink == nil {
		return
	}
	title := ""
	if chat := ui.current; chat != nil && (ui.prefs == nil || !ui.prefs.open) {
		if chat.server != nil {
			title = chat.server.parentTitle(chat.serverID)
		} else if parent := ui.parentOf(chat); parent != nil {
			title = parent.Title
		}
	}
	if title == "" {
		ui.parentLink.Hide()
		return
	}
	if runes := []rune(title); len(runes) > 28 {
		title = string(runes[:28]) + "…"
	}
	ui.parentLink.SetText("Subchat of " + title)
	ui.parentLink.Show()
}

// parentOf is a server chat's parent, by its ID there.
func (remote *remoteDesktop) parentOf(id string) string {
	for _, chat := range remote.chats {
		if chat.ID == id {
			return chat.Parent
		}
	}
	return ""
}

// parentTitle is a server subchat's parent's title, "" for another chat.
func (remote *remoteDesktop) parentTitle(id string) string {
	parent := remote.parentOf(id)
	for _, chat := range remote.chats {
		if parent != "" && chat.ID == parent {
			return chat.Title
		}
	}
	return ""
}

// addSubchats lists the open server chat's subchats in its Tasks tab.
func (remote *remoteDesktop) addSubchats() {
	if len(remote.meta.Subchats) == 0 {
		return
	}
	remote.todoTasks.Add(sectionTitle("Subchats"))
	for _, sub := range remote.meta.Subchats {
		sub := sub
		state := map[string]string{agent.SubchatWorking: "Working", agent.SubchatWaiting: "Waiting for you", agent.SubchatDone: "Done",
			agent.SubchatFailed: "Failed", agent.SubchatStopped: "Stopped"}[sub.State]
		if sub.Branch != "" {
			state += " · " + sub.Branch
		}
		actions := container.NewHBox(newRow("Open", iconSubchat, styleChip, func() { remote.openChat(sub.ID) }))
		if agent.SubchatActive(sub) {
			actions.Add(newRow("Stop", iconStop, styleChip, func() { remote.action("/api/stop", map[string]string{"chat": sub.ID}) }))
		}
		remote.todoTasks.Add(container.NewBorder(nil, nil, nil, actions, container.NewVBox(textLine(sub.Title, 13, colText), textLine(state, 11.5, colMuted))))
	}
}
