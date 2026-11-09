// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"path"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
)

// A project shows this many chats until "Show more" is pressed.
const groupChatLimit = 5

// sideChat is a chat as the sidebar lists it: this computer's, a machine's
// or a server's.
type sideChat struct {
	title    string
	folder   string // its project, or a worktree of it
	scratch  bool   // in a folder 1loop made for it: listed under No folder
	running  bool
	selected bool
	open     func()
	menu     []menuAction
	text     func() []string // its messages, for search; nil when they are not at hand
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

// renderSidebar lists the projects as groups that fold, each with its own
// chats. selectProject opens the new chat screen in a project or worktree;
// projectActions are a project's menu after "New chat".
func (ui *desktopUI) renderSidebar(projects []string, chats []sideChat, selectProject func(string), projectActions func(string) []menuAction) {
	ui.sideChats = chats
	worktrees := ui.worktreeMap()
	var listed []string
	for _, p := range projects {
		if worktrees[p] == "" { // a worktree shows under its project
			listed = append(listed, p)
		}
	}
	opened := ui.projectOf(ui.cfg.Workspace)
	ui.projectList.RemoveAll()
	var scratch []sideChat
	chats = slices.DeleteFunc(slices.Clone(chats), func(chat sideChat) bool {
		if chat.scratch {
			scratch = append(scratch, chat)
		}
		return chat.scratch
	})
	if ui.server == nil && ui.cfg.MachineID == "" && (ui.cfg.InScratch() || len(scratch) > 0) {
		ui.addScratchGroup(scratch)
	}
	for _, group := range groupByProject(listed, chats, func(chat sideChat) string { return chat.folder }, ui.projectOf) {
		p := group.project
		folded := ui.foldedProjects[p]
		name := "Other chats"
		if p != "" {
			name = ui.projectName(p)
		}
		icon := iconChevron
		if folded {
			icon = iconChevronRight
		}
		header := newRow(name, icon, styleNav, func() { ui.foldProject(p, !folded) })
		header.accent = true
		header.truncate = true
		header.selected = p != "" && p == ui.cfg.Workspace && ui.current == nil
		if state := ui.repoFor(p); p != "" && state.repo != nil {
			header.trailing = state.repo.Current // the branch of the project's folder
		}
		if folded && groupRunning(group.chats) {
			header.badge = iconDot // a hidden chat is at work
		}
		if p == "" {
			ui.projectList.Add(header)
		} else {
			ui.projectList.Add(ui.withMore(header, func(anchor fyne.CanvasObject) *sheet {
				actions := []menuAction{{label: "New chat in " + name, icon: iconPlus, run: func() { selectProject(p); ui.startNew() }}}
				return showMenu(ui.window, anchor, append(actions, projectActions(p)...))
			}))
		}
		if folded {
			continue
		}
		ui.addGroupChats(p, group.chats)
		if p != "" && p == opened { // its worktrees, after its chats
			for _, object := range ui.worktreeRows(p, selectProject) {
				ui.projectList.Add(object)
			}
			ui.loadRepo(p, true, 20*time.Second)
		}
	}
	if ui.sideLists != nil {
		ui.sideLists.Refresh() // the groups changed height: lay the column out again
	}
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
	ui.projectList.Add(ui.withMore(header, func(anchor fyne.CanvasObject) *sheet {
		return showMenu(ui.window, anchor, []menuAction{{label: "New chat with no folder", icon: iconPlus, run: ui.chooseNoFolder}})
	}))
	if !folded {
		ui.addGroupChats(scratchGroup, chats)
	}
}

// addGroupChats lists a project's chats under it, the first few unless it
// was unfolded in full or the open chat is further down.
func (ui *desktopUI) addGroupChats(project string, chats []sideChat) {
	if len(chats) == 0 {
		ui.projectList.Add(padded(4, 4, 38, 0, textLine("No chats yet", 13, colFaint)))
		return
	}
	shown := chats
	if !ui.allChatsShown[project] && len(chats) > groupChatLimit {
		shown = chats[:groupChatLimit]
		for _, chat := range chats[groupChatLimit:] {
			if chat.selected {
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
		if chat.folder != project && project != "" && !chat.scratch {
			row.trailing = path.Base(ui.worktreeLabel(chat.folder)) // a worktree's chat: its branch's last part, short
		}
		if chat.running {
			row.badge = iconDot // the agent works on it in the background
		}
		ui.projectList.Add(padded(0, 0, 28, 0, ui.withMore(row, func(anchor fyne.CanvasObject) *sheet {
			return showMenu(ui.window, anchor, chat.menu)
		})))
	}
	if len(chats) > groupChatLimit {
		label := fmt.Sprintf("Show %d more", len(chats)-len(shown))
		if len(shown) == len(chats) {
			label = "Show less"
		}
		more := newRow(label, nil, styleNav, func() {
			ui.allChatsShown[project] = len(shown) < len(chats)
			ui.renderChats()
		})
		more.ink = colFaint
		more.size = 13
		ui.projectList.Add(padded(0, 0, 28, 0, more))
	}
}

func groupRunning(chats []sideChat) bool {
	for _, chat := range chats {
		if chat.running {
			return true
		}
	}
	return false
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
	ui.newChat = newRow("New chat", iconPlus, styleNav, ui.startNew)
	ui.newChat.trailing = "Ctrl K"
	search := newRow("Search", iconSearch, styleNav, ui.search)
	search.trailing = "Ctrl F"
	add := newRow("Add project", iconFolderAdd, styleNav, ui.addProject)
	return container.New(layout.NewCustomPaddedVBoxLayout(1), ui.newChat, search, add)
}
