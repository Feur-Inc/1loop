// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
	registry "1loop/internal/settings"
)

// A project shows this many chats until "Show more" is pressed.
const groupChatLimit = 5

// sideChat is a chat as the sidebar lists it: this computer's, a machine's
// or a server's.
type sideChat struct {
	id       string
	parent   string // a subchat's parent, "" for another chat
	ended    string // how a subchat's last turn ended: done, stopped or failed
	title    string
	folder   string // its project, or a worktree of it
	scratch  bool   // in a folder 1loop made for it: listed under No folder
	running  bool
	status   string // its last wrap-up's recap, first sentence
	blocked  bool   // that wrap-up waits on the user
	selected bool
	open     func()
	window   func() // opens it in a new window (middle click), nil when it cannot have one
	menu     []menuAction
	text     func() []string // its messages, for search; nil when they are not at hand
	subchats []sideChat      // listed under it, whatever their folder, each with its own (nestSubchats)
	group    string          // the key of the group it is listed in (groupKey), once listed
}

// nestSubchats puts each subchat under its parent, wherever its folder is
// (its worktree, or a parent's scratch folder), as a tree: a subchat's own
// subchats go under it, down to agent.MaxSubchatDepth. One whose parent is
// not listed stays a chat of its own, and so does one of a loop of links.
func nestSubchats(chats []sideChat) []sideChat {
	listed := map[string]bool{}
	for _, chat := range chats {
		listed[chat.id] = true
	}
	children := map[string][]sideChat{}
	var top []sideChat
	for _, chat := range chats {
		if chat.parent != "" && chat.parent != chat.id && listed[chat.parent] {
			children[chat.parent] = append(children[chat.parent], chat)
			continue
		}
		top = append(top, chat)
	}
	placed := map[string]bool{}
	var grow func(chat sideChat, depth int) sideChat
	grow = func(chat sideChat, depth int) sideChat {
		placed[chat.id] = true
		chat.subchats = nil
		for _, sub := range children[chat.id] {
			if !placed[sub.id] && depth < agent.MaxSubchatDepth {
				chat.subchats = append(chat.subchats, grow(sub, depth+1))
			}
		}
		return chat
	}
	for i := range top {
		top[i] = grow(top[i], 0)
	}
	for _, chat := range chats { // in no tree from the top: listed on its own
		if !placed[chat.id] {
			top = append(top, grow(chat, 0))
		}
	}
	return top
}

// openHere says whether the chat or a subchat under it is open.
func (chat sideChat) openHere() bool {
	return chat.selected || slices.ContainsFunc(chat.subchats, sideChat.openHere)
}

// workingHere says whether the chat or a subchat under it is at work.
func (chat sideChat) workingHere() bool {
	return chat.running || slices.ContainsFunc(chat.subchats, sideChat.workingHere)
}

// subchatState is a subchat's state as its dot in the sidebar shows it.
func (chat sideChat) subchatState() string {
	switch {
	case chat.blocked && chat.running:
		return agent.SubchatWaiting
	case chat.running:
		return agent.SubchatWorking
	case chat.ended == agent.SubchatDone || chat.ended == agent.SubchatFailed:
		return chat.ended
	}
	return "" // idle: not started yet, or stopped
}

// projectGroup is a project of the sidebar and the chats under it. The
// group of no project ("") holds chats whose folder no project lists.
type projectGroup[T any] struct {
	project string
	chats   []T
}

// groupByProject puts each chat under its project, in the projects' order,
// keeping the chats' order. projectOf maps a worktree to its project.
func groupByProject[T any](projects []string, chats []T, folder func(T) string, projectOf func(string) string) []projectGroup[T] {
	groups := make([]projectGroup[T], len(projects))
	index := map[string]int{}
	for i, project := range projects {
		groups[i].project = project
		index[project] = i
	}
	var other []T
	for _, chat := range chats {
		if i, ok := index[projectOf(folder(chat))]; ok {
			groups[i].chats = append(groups[i].chats, chat)
		} else {
			other = append(other, chat)
		}
	}
	if len(other) > 0 {
		groups = append(groups, projectGroup[T]{chats: other})
	}
	return groups
}

// renderSidebar lists every target's projects as groups that fold, each
// with its own chats (#359): this computer's chats with no folder first,
// then this computer's projects, each SSH machine's and the server's.
func (ui *desktopUI) renderSidebar(groups []targetGroup) {
	ui.projectList.RemoveAll()
	ui.sideGlide.begin()
	var all, scratch []sideChat
	for i := range groups {
		listed := groups[i].listed()
		for j := range groups[i].chats {
			groups[i].chats[j].group = groups[i].keyOf(listed, groups[i].chats[j])
		}
		all = append(all, groups[i].chats...)
		groups[i].chats = slices.DeleteFunc(nestSubchats(groups[i].chats), func(chat sideChat) bool {
			if chat.scratch {
				scratch = append(scratch, chat)
			}
			return chat.scratch
		})
	}
	ui.sideChats = all
	if len(scratch) > 0 || ui.server == nil && ui.cfg.MachineID == "" && ui.cfg.InScratch() {
		ui.addScratchGroup(scratch)
	}
	ui.sideOrder = append(ui.sideOrder[:0], scratch...)
	for _, group := range groups {
		ui.addTargetGroup(group)
	}
	ui.sideGlide.end(ui.projectList)
	if ui.sideLists != nil {
		ui.sideLists.Refresh() // the groups changed height: lay the column out again
	}
}

// offlineMark is what a project shows, in place of its branch, while
// its target cannot be reached: down, out of the network or refusing the
// sign-in. Short, for a narrow sidebar.
const offlineMark = "offline"

// addTargetGroup lists a target's projects, each with its chats, then its
// chats of no listed project. A project of another target than this
// computer shows the target's icon after its name, and offlineMark
// while the target does not answer.
func (ui *desktopUI) addTargetGroup(group targetGroup) {
	target := group.target
	open := func(folder string) { ui.openProjectOn(target, folder) }
	opened := ""
	if group.active {
		opened = ui.projectOf(ui.cfg.Workspace)
	}
	for _, chats := range groupByProject(group.listed(), group.chats, func(chat sideChat) string { return chat.folder }, group.projectOf) {
		ui.sideOrder = append(ui.sideOrder, chats.chats...)
		p := chats.project
		key := groupKey(target, p)
		folded := ui.foldedProjects[key]
		name := "Other chats"
		if p != "" {
			name = targetProjectName(target, p)
		}
		icon := iconChevron
		if folded {
			icon = iconChevronRight
		}
		header := newRow(name, icon, styleNav, func() { ui.foldProject(key, !folded) })
		header.accent = true
		header.truncate = true
		header.mark = target.mark(ui.home)
		header.selected = group.active && p != "" && p == ui.cfg.Workspace && ui.current == nil
		if group.reach.state == reachOffline {
			header.trailing = offlineMark
		} else if state := ui.repoStateOn(target, p); p != "" && state != nil && state.repo != nil {
			header.trailing = state.repo.Current // the branch of the project's folder
		}
		if folded && groupRunning(chats.chats) {
			header.badge = iconDot // a hidden chat is at work
		}
		if p == "" {
			ui.addSide("project:"+key, header)
		} else {
			ui.addSide("project:"+key, ui.withMore(header, func(anchor fyne.CanvasObject) *sheet {
				actions := []menuAction{{label: "New chat in " + name, icon: iconPlus, run: func() {
					open(p)
					if group.active {
						ui.startNew() // the project may be open already, with a chat on screen
					}
				}}}
				return showMenu(ui.window, anchor, append(actions, ui.projectActionsOn(group, p)...))
			}))
		}
		if folded {
			continue
		}
		if !group.known { // a server that has not answered: its chats are not at hand
			placeholder := "Connecting…"
			if group.reach.state == reachOffline {
				placeholder = "Chats show once it's back"
			}
			// Cut short rather than widen the sidebar when it is narrow.
			ui.addSide("empty:"+key, padded(4, 4, 38, 0, newFitLabel(placeholder, 13, colFaint)))
			continue
		}
		ui.addGroupChats(key, p, chats.chats)
		if p != "" && p == opened { // its worktrees, after its chats
			for _, object := range ui.worktreeRows(p, open) {
				key := "worktree:" + key
				if row := firstRow(object); row != nil {
					key += ":" + row.text
				}
				ui.addSide(key, object)
			}
			ui.loadRepo(p, true, 20*time.Second)
		}
	}
}

// addSide adds a sidebar entry under its key: the same chat or project
// keeps its key from one rendering to the next, so it glides to its new
// place instead of jumping there.
func (ui *desktopUI) addSide(key string, object fyne.CanvasObject) {
	ui.sideGlide.key(object, key)
	ui.projectList.Add(object)
}

// scratchGroup is the No folder group's key in foldedProjects and
// allChatsShown, which no folder path can be.
const scratchGroup = ":scratch"

// addScratchGroup lists the chats with no folder of the user's first:
// selecting the group starts one.
func (ui *desktopUI) addScratchGroup(chats []sideChat) {
	folded := ui.foldedProjects[scratchGroup]
	icon := iconChevron
	if folded {
		icon = iconChevronRight
	}
	header := newRow(noFolderLabel, icon, styleNav, func() { ui.foldProject(scratchGroup, !folded) })
	header.accent = true
	header.selected = ui.cfg.InScratch() && ui.current == nil
	if folded && groupRunning(chats) {
		header.badge = iconDot
	}
	ui.addSide("project:"+scratchGroup, ui.withMore(header, func(anchor fyne.CanvasObject) *sheet {
		return showMenu(ui.window, anchor, []menuAction{{label: "New chat with no folder", icon: iconPlus, run: ui.chooseNoFolder}})
	}))
	if !folded {
		ui.addGroupChats(scratchGroup, scratchGroup, chats)
	}
}

// addGroupChats lists a project's chats under it, the first few unless it
// was unfolded in full or the open chat is further down. key is the
// group's (groupKey).
func (ui *desktopUI) addGroupChats(key, project string, chats []sideChat) {
	if len(chats) == 0 {
		ui.addSide("empty:"+key, padded(4, 4, 38, 0, textLine("No chats yet", 13, colFaint)))
		return
	}
	shown := chats
	if !ui.allChatsShown[key] && len(chats) > groupChatLimit {
		shown = chats[:groupChatLimit]
		for _, chat := range chats[groupChatLimit:] {
			if chat.openHere() {
				shown = chats
			}
		}
	}
	for _, chat := range shown {
		chat := chat
		row := newRow(chat.title, nil, styleNav, chat.open)
		row.accent = true
		row.truncate = true
		row.selected = chat.selected
		row.onMiddle = chat.window
		if chat.folder != project && project != "" && !chat.scratch {
			row.trailing = path.Base(ui.worktreeLabel(chat.folder)) // a worktree's chat: its branch's last part, short
		}
		if !chat.running {
			row.detail = chat.status
		}
		switch {
		case chat.running:
			row.badge = iconDot // the agent works on it in the background
		case chat.blocked:
			row.badge = iconWaiting // its wrap-up waits on the user
		}
		if nested, _ := nestedCount(chat.subchats); nested > 0 {
			row.mark, row.markText = iconBranch, strconv.Itoa(nested)
		}
		ui.addSide("chat:"+chat.id, padded(0, 0, 28, 0, ui.withMore(row, func(anchor fyne.CanvasObject) *sheet {
			return showMenu(ui.window, anchor, chat.menu)
		})))
		ui.addSubchatTree(chat)
	}
	if len(chats) > groupChatLimit {
		label := fmt.Sprintf("Show %d more", len(chats)-len(shown))
		if len(shown) == len(chats) {
			label = "Show less"
		}
		more := newRow(label, nil, styleNav, func() {
			ui.allChatsShown[key] = len(shown) < len(chats)
			ui.renderChats()
		})
		more.ink = colFaint
		more.size = 13
		ui.addSide("more:"+key, padded(0, 0, 28, 0, more))
	}
}

// A chat with more than this many subchats, all levels counted, lists one
// summary row under it in place of its tree until that row is clicked
// (#426), as Claude Code does.
const subchatsFolded = 3

// nestedCount counts subchats at every level, and how many of them failed.
func nestedCount(subs []sideChat) (total, failed int) {
	for _, sub := range subs {
		total++
		if sub.subchatState() == agent.SubchatFailed {
			failed++
		}
		deeper, deeperFailed := nestedCount(sub.subchats)
		total += deeper
		failed += deeperFailed
	}
	return total, failed
}

// nestedSummary is the text of the row that stands for a chat's subchats.
func nestedSummary(total, failed int) string {
	text := fmt.Sprintf("%d nested sessions", total)
	if failed > 0 {
		text += fmt.Sprintf(" · %d failed", failed)
	}
	return text
}

// addSubchatTree lists a chat's subchats under its row: the tree when they
// are few; when they are many, a muted summary row the line bends into,
// which unfolds the tree under it when clicked and folds it again. Until
// the user clicks it, the tree is unfolded only while one of them is open.
func (ui *desktopUI) addSubchatTree(chat sideChat) {
	total, failed := nestedCount(chat.subchats)
	if total <= subchatsFolded {
		ui.addSubchatRows(chat.subchats, 1, trunkNone)
		return
	}
	shown, chosen := ui.subchatsShown[chat.id]
	if !chosen {
		shown = slices.ContainsFunc(chat.subchats, sideChat.openHere)
	}
	trunk := trunkLast // folded: the line ends on the summary
	if shown {
		trunk = trunkThrough
	}
	summary := newRow(nestedSummary(total, failed), nil, styleNav, func() {
		if ui.subchatsShown == nil {
			ui.subchatsShown = map[string]bool{}
		}
		ui.subchatsShown[chat.id] = !shown
		ui.renderChats()
	})
	summary.ink, summary.size, summary.truncate = colMuted, 13, true
	summary.tips = ui.tips               // the whole text while hovered, when cut short
	summary.endInset = moreButtonInset() // its warning in line with the dots of the rows with a ⋯
	if failed > 0 {
		summary.badge = iconWarning
	}
	ui.addSide("nested:"+chat.id, padded(0, 0, 28, 0, container.NewBorder(nil, nil, newSubchatGuide(1, trunk), nil, summary)))
	if shown {
		ui.addSubchatRows(chat.subchats, 1, trunkNone)
	}
}

// addSubchatRows lists subchats under their parent's row as a tree, as
// Claude Code does: plain text, each level one step further in, a line down
// the left of the first level that bends into its last subchat, and the
// state's dot at the right. trunk is what that line does beside rows under
// the first level.
func (ui *desktopUI) addSubchatRows(subs []sideChat, depth int, trunk treeTrunk) {
	for i, sub := range subs {
		sub := sub
		own := trunk // a deeper row: the line of its first-level ancestor
		if depth == 1 {
			own = trunkThrough
			if i == len(subs)-1 {
				own = trunkLast
			}
		}
		subRow := newRow(sub.title, nil, styleNav, sub.open)
		subRow.accent, subRow.truncate, subRow.selected = true, true, sub.selected
		subRow.onMiddle = sub.window
		subRow.size = 13
		subRow.badge = subchatBadge(sub.subchatState())
		guide := newSubchatGuide(depth, own)
		ui.addSide("chat:"+sub.id, padded(0, 0, 28, 0, container.NewBorder(nil, nil, guide, nil, ui.withMore(subRow, func(anchor fyne.CanvasObject) *sheet {
			return showMenu(ui.window, anchor, sub.menu)
		}))))
		below := own // under it, the line runs on only if more subchats follow
		if own == trunkLast {
			below = trunkNone
		}
		ui.addSubchatRows(sub.subchats, depth+1, below)
	}
}

func groupRunning(chats []sideChat) bool {
	return slices.ContainsFunc(chats, sideChat.workingHere)
}

// foldProject folds or unfolds a project's group, kept for the next start.
func (ui *desktopUI) foldProject(project string, folded bool) {
	if folded {
		ui.foldedProjects[project] = true
	} else {
		delete(ui.foldedProjects, project)
	}
	ui.save()
	ui.renderChats()
}

// sideActions is the labelled block at the top of the sidebar.
func (ui *desktopUI) sideActions() fyne.CanvasObject {
	// They act on the pane with the focus, in a split window (panes.go).
	ui.newChat = newRow("New chat", iconPlus, styleNav, func() { ui.focused().startNew() })
	ui.newChat.trailing = ui.shortcutLabel(registry.ShortcutNewChat)
	ui.searchRow = newRow("Search", iconSearch, styleNav, func() { ui.focused().search() })
	ui.searchRow.trailing = ui.shortcutLabel(registry.ShortcutSearch)
	add := newRow("Add project", iconFolderAdd, styleNav, func() { ui.focused().addProject() })
	add.trailing = ui.shortcutLabel(registry.ShortcutNewProject)
	return container.New(layout.NewCustomPaddedVBoxLayout(1), ui.newChat, ui.searchRow, add)
}
