// SPDX-License-Identifier: MPL-2.0

package ui

// Projects belong to a target (#359): this computer, an SSH machine of
// Settings → General, or the paired 1loop server. The sidebar lists the
// projects of every target at once, those of another target than this
// computer marked with its icon, and a project whose target does not
// answer stays listed, marked offline, instead of disappearing.
// Opening a project or a chat makes its target the one the window works
// on: there is no switch to flip before reaching it.
//
// A new chat with no folder runs on the device in use (noFolderTarget);
// the target chip under the composer moves a new chat to another target.

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// browserClient is the browser build (web_js.go): it runs nothing itself,
// and the server the page came from is its own target.
const browserClient = runtime.GOOS == "js"

// targetKind is the kind of place a project lives on.
type targetKind int

const (
	targetLocal  targetKind = iota // this computer, the device in use
	targetSSH                      // an SSH machine: its files and commands are there, the agent runs here
	targetServer                   // a paired headless 1loop server: everything runs there
)

// projectTarget is where a project lives and its chats run: its kind and,
// but for this computer, its ID there (the SSH machine's ID, the server's
// address).
type projectTarget struct {
	kind targetKind
	id   string
}

var localTarget = projectTarget{}

func sshTarget(id string) projectTarget {
	return projectTarget{kind: targetSSH, id: id}
}

func serverTarget(address string) projectTarget {
	return projectTarget{kind: targetServer, id: address}
}

// key names the target in maps.
func (target projectTarget) key() string {
	switch target.kind {
	case targetSSH:
		return "ssh:" + target.id
	case targetServer:
		return "server:" + target.id
	}
	return "local"
}

// groupKey names a target's project in the sidebar's keys and folds. This
// computer's project keeps its path, the key it had before targets, and
// another target's can never be mistaken for it.
func groupKey(target projectTarget, project string) string {
	if target.kind == targetLocal {
		return project
	}
	return target.key() + "|" + project
}

// mark is the small icon after the name of the target's projects: none
// for the device in use; the same icons as the target menu's otherwise.
func (target projectTarget) mark() fyne.Resource {
	switch target.kind {
	case targetSSH:
		return iconTerminal
	case targetServer:
		if browserClient {
			return nil
		}
		return iconBrowser
	}
	return nil
}

// targetProjectName names a target's project: its folder's name there.
func targetProjectName(target projectTarget, project string) string {
	if target.kind == targetLocal {
		return filepath.Base(project)
	}
	return path.Base(filepath.ToSlash(project))
}

// noFolderTarget is where a new chat with no folder runs: the device in
// use, which is this computer. One function, so that another default is a
// one-line change; the target chip under the composer moves a new chat
// elsewhere. (The browser client has no folder of its own to offer: its
// chats run on the page's server.)
func noFolderTarget() projectTarget { return localTarget }

// reach is whether a target answers, as last seen.
type reach int

const (
	reachUnknown reach = iota // not tried yet: listed as usual
	reachOnline
	reachOffline
)

type targetReach struct {
	state   reach
	problem string // why it could not be reached
}

// setReach records whether a target answered, and lists it again when
// that changed. It says whether it did.
func (ui *desktopUI) setReach(target projectTarget, state reach, problem string) bool {
	next := targetReach{state: state, problem: problem}
	if ui.reaches[target.key()] == next {
		return false
	}
	if ui.reaches == nil {
		ui.reaches = map[string]targetReach{}
	}
	ui.reaches[target.key()] = next
	if ui.projectList != nil {
		ui.renderChats()
	}
	return true
}

// targetGroup is a target as the sidebar lists it: its projects and their
// chats, and whether it answers.
type targetGroup struct {
	target    projectTarget
	reach     targetReach
	active    bool              // the window works on it now
	projects  []string          // its projects; a worktree among them shows under its project
	chats     []sideChat        // its chats
	known     bool              // its chats are at hand: not those of a server that never answered
	worktrees map[string]string // its worktrees: folder → project
}

// listed are the target's projects the sidebar lists: not its worktrees,
// which show under their project.
func (group targetGroup) listed() []string {
	var listed []string
	for _, project := range group.projects {
		if group.worktrees[project] == "" {
			listed = append(listed, project)
		}
	}
	return listed
}

// projectOf is the target's project a folder belongs to: itself, or the
// project of the worktree it is.
func (group targetGroup) projectOf(folder string) string {
	if project := group.worktrees[folder]; project != "" {
		return project
	}
	return folder
}

// keyOf is the key of the sidebar group that lists a chat of the target
// (groupKey): its project's among those listed, Other chats' when none
// holds it, No folder's for a chat with no folder.
func (group targetGroup) keyOf(listed []string, chat sideChat) string {
	if chat.scratch {
		return scratchGroup
	}
	project := group.projectOf(chat.folder)
	if !slices.Contains(listed, project) {
		project = ""
	}
	return groupKey(group.target, project)
}

// sidebarGroups are the targets the sidebar lists, in its order: this
// computer, the SSH machines as Settings lists them, then the server. A
// target that has neither a project nor a chat is left out.
func (ui *desktopUI) sidebarGroups() []targetGroup {
	var groups []targetGroup
	add := func(group targetGroup) {
		if len(group.projects) > 0 || len(group.chats) > 0 {
			groups = append(groups, group)
		}
	}
	add(ui.localGroup())
	for _, machine := range ui.sshMachines {
		add(ui.machineGroup(machine.ID))
	}
	if group, ok := ui.serverGroup(); ok {
		add(group)
	}
	return groups
}

func (ui *desktopUI) localGroup() targetGroup {
	group := targetGroup{target: localTarget, reach: targetReach{state: reachOnline}, known: true,
		active: ui.server == nil && ui.cfg.MachineID == "", projects: ui.projects, worktrees: ui.worktrees}
	for _, chat := range ui.chats {
		if chat.Config.MachineID == "" {
			group.chats = append(group.chats, ui.sideChatOf(chat))
		}
	}
	return group
}

// machineGroup is an SSH machine's: its projects and chats are kept here,
// so they are listed whether it answers or not.
func (ui *desktopUI) machineGroup(id string) targetGroup {
	target := sshTarget(id)
	group := targetGroup{target: target, reach: ui.reaches[target.key()], known: true,
		active: ui.server == nil && ui.cfg.MachineID == id, projects: ui.machineProjects[id]}
	for _, chat := range ui.chats {
		if chat.Config.MachineID == id {
			group.chats = append(group.chats, ui.sideChatOf(chat))
		}
	}
	return group
}

// serverGroup is the server's: the window's own connection to it while it
// works on it, else what its link last read. Until either has answered,
// its projects are those remembered from the last time it did, and its
// chats are not at hand.
func (ui *desktopUI) serverGroup() (targetGroup, bool) {
	var address string
	var listing *serverListing
	active := false
	switch {
	case ui.server != nil:
		remote := ui.server
		address, active = remote.client.address.URL, true
		listing = &serverListing{name: remote.name, projects: remote.projects, chats: remote.chats, worktrees: remote.worktrees}
	case ui.link != nil:
		address = ui.link.address.URL
		if ui.link.answered {
			listing = &ui.link.listing
		}
	default:
		return targetGroup{}, false
	}
	target := serverTarget(address)
	group := targetGroup{target: target, reach: ui.reaches[target.key()], active: active, projects: ui.serverProjects[address]}
	if listing != nil {
		group.known, group.worktrees = true, listing.worktrees
		group.chats = ui.serverSideChats(target, listing.chats)
		if len(listing.projects) > 0 || group.reach.state == reachOnline {
			group.projects = listing.projects
		}
	}
	return group, true
}

// serverListing is what a server lists of itself for the sidebar.
type serverListing struct {
	name      string
	projects  []string
	chats     []remoteapi.Chat
	worktrees map[string]string // folder → project
}

// serverProjectList is a server's projects as the sidebar lists them: its
// default folder, those it lists, then its chats' folders, each once, its
// worktrees left out (they show under their project).
func serverProjectList(workspace string, listed []string, chats []remoteapi.Chat, worktrees map[string]string) []string {
	projects := []string{}
	add := func(folder string) {
		if folder != "" && !slices.Contains(projects, folder) && worktrees[folder] == "" {
			projects = append(projects, folder)
		}
	}
	add(workspace)
	for _, folder := range listed {
		add(folder)
	}
	for _, chat := range chats {
		add(chat.Workspace)
	}
	return projects
}

// rememberServerProjects keeps the server's projects for the next start,
// when it may not answer: they are listed then, marked offline. Only
// the server paired now is remembered.
func (ui *desktopUI) rememberServerProjects(address string, projects []string) {
	if len(ui.serverProjects) == 1 && slices.Equal(ui.serverProjects[address], projects) {
		return
	}
	ui.serverProjects = map[string][]string{address: slices.Clone(projects)}
	ui.save()
}

// sideChatOf is a chat of this computer or of an SSH machine as the
// sidebar lists it.
func (ui *desktopUI) sideChatOf(chat *conversation) sideChat {
	status, blocked := agent.WrapUpStatus(chat.Items)
	menu := []menuAction{
		{label: "Rename", icon: iconRename, run: func() { ui.renameChat(chat) }},
		{label: "Delete", icon: iconTrash, danger: true, run: func() { ui.deleteChat(chat) }},
	}
	parent := ""
	if chat.Subchat != nil {
		parent = chat.Subchat.Parent
		menu = append([]menuAction{{label: "Open parent", icon: iconSubchat, run: func() {
			if parent := ui.parentOf(chat); parent != nil {
				ui.openLocalChat(parent)
			}
		}}}, menu...)
		if chat.run != nil {
			menu = append([]menuAction{{label: "Stop", icon: iconStop, run: func() { ui.stopSubchat(chat) }}}, menu...)
		}
	}
	return sideChat{
		id: chat.ID, parent: parent,
		title: chat.Title, folder: chat.Config.Workspace, scratch: chat.Config.InScratch(), running: chat.run != nil, selected: chat == ui.current,
		status: status, blocked: blocked,
		open: func() { ui.openLocalChat(chat) },
		menu: menu,
		text: func() []string { return chatTexts(chat.Items) },
	}
}

// serverSideChats are a server's chats as the sidebar lists them: opening
// one makes the server the window's target first.
func (ui *desktopUI) serverSideChats(target projectTarget, chats []remoteapi.Chat) []sideChat {
	onServer := func(run func(*remoteDesktop)) func() {
		return func() {
			if remote := ui.server; remote != nil && remote.client.address.URL == target.id {
				run(remote)
				return
			}
			ui.reachServer(target, run)
		}
	}
	current := ui.current
	var side []sideChat
	for _, chat := range chats {
		chat := chat
		side = append(side, sideChat{
			id: chat.ID, parent: chat.Parent,
			title: chat.Title, folder: chat.Workspace, running: chat.Running, // the agent works on it on the server
			status: chat.Status, blocked: chat.Blocked,
			selected: current != nil && current.server != nil && current.server.client.address.URL == target.id && current.serverID == chat.ID,
			open:     onServer(func(remote *remoteDesktop) { remote.openChat(chat.ID) }),
			menu:     []menuAction{{label: "Delete", icon: iconTrash, danger: true, run: onServer(func(remote *remoteDesktop) { remote.deleteChat(chat) })}},
		})
	}
	return side
}

// openLocalChat opens a chat of this computer or of an SSH machine: the
// window comes back from a server first, and works on the chat's target.
func (ui *desktopUI) openLocalChat(chat *conversation) {
	if ui.server != nil {
		ui.useComputer()
	}
	if ui.cfg.MachineID == "" && chat.Config.MachineID != "" {
		ui.localWorkspace = ui.cfg.Workspace // where the target menu's "This computer" comes back to
	}
	ui.openChat(chat)
	ui.machine.SetText(ui.targetLabel())
}

// openProjectOn opens the new chat screen in a project (or a worktree) of
// a target, which becomes the one the window works on.
func (ui *desktopUI) openProjectOn(target projectTarget, project string) {
	switch target.kind {
	case targetLocal:
		if ui.server != nil {
			ui.useComputer()
		}
		if ui.cfg.MachineID != "" { // the project is opened below, whatever the machine's was
			ui.cfg.MachineID, ui.cfg.Workspace = "", ""
			ui.machine.SetText(ui.targetLabel())
		}
		ui.selectProject(project)
	case targetSSH:
		if ui.server == nil && ui.cfg.MachineID == target.id {
			ui.selectProject(project)
			return
		}
		ui.useMachineOn(target.id, project)
	case targetServer:
		if remote := ui.server; remote != nil && remote.client.address.URL == target.id {
			remote.selectProject(project)
			return
		}
		ui.reachServer(target, func(remote *remoteDesktop) { remote.selectProject(project) })
	}
}

// reachServer makes the paired server the window's target, then runs then
// on it. When its link does not know it to answer, it is tried first: if
// it still does not, the window stays where it is and says why.
func (ui *desktopUI) reachServer(target projectTarget, then func(*remoteDesktop)) {
	saved := loadServerAddress()
	if saved.URL != target.id || saved.Token == "" {
		ui.notice("Remote server", "This server is no longer paired. Connect it again with the target menu under the conversation.")
		return
	}
	client, err := newRemoteClient(saved)
	if err != nil {
		ui.notice("Remote server", err.Error())
		return
	}
	use := func() {
		ui.useServer(client)
		then(ui.server)
	}
	if ui.reaches[target.key()].state == reachOnline {
		use()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 10*time.Second)
		defer cancel()
		var state map[string]any
		err := client.json(ctx, "GET", "/api/state", nil, &state)
		fyne.Do(func() {
			if err != nil {
				ui.setReach(target, reachOffline, err.Error())
				ui.notice(serverName(target.id), fmt.Sprintf("%s cannot be reached: %v", serverName(target.id), err))
				return
			}
			ui.setReach(target, reachOnline, "")
			use()
		})
	}()
}

// projectsOn are a target's projects as this computer keeps them: its
// own, or an SSH machine's (paths there). A server keeps its own list.
func (ui *desktopUI) projectsOn(target projectTarget) []string {
	if target.kind == targetSSH {
		return ui.machineProjects[target.id]
	}
	return ui.projects
}

func (ui *desktopUI) setProjectsOn(target projectTarget, projects []string) {
	if target.kind != targetSSH {
		ui.projects = projects
		return
	}
	if ui.machineProjects == nil {
		ui.machineProjects = map[string][]string{}
	}
	ui.machineProjects[target.id] = projects
}

// projectActionsOn are a project's menu after New chat: every one when the
// window works on its target, else those that need no connection to it.
func (ui *desktopUI) projectActionsOn(group targetGroup, project string) []menuAction {
	remove := menuAction{label: "Remove from 1loop", icon: iconTrash, danger: true, run: func() { ui.removeProject(group.target, project) }}
	switch {
	case group.target.kind == targetServer && group.active:
		return append(ui.repositoryActions(project), ui.projectMemoryAction(project))
	case group.target.kind == targetServer: // the server keeps its own list
		return nil
	case !group.active:
		return []menuAction{remove}
	}
	actions := append(ui.repositoryActions(project), ui.projectMemoryAction(project))
	if group.target.kind == targetLocal { // a machine's folder is renamed there, in its terminal
		actions = append(actions, menuAction{label: "Rename", icon: iconRename, run: func() { ui.renameProject(project) }})
	}
	return append(actions, remove)
}

// repoStateOn is what the window read of a project's repository on a
// target, nil when it read nothing.
func (ui *desktopUI) repoStateOn(target projectTarget, project string) *repoState {
	switch target.kind {
	case targetSSH: // not read on an SSH machine yet
		return nil
	case targetServer:
		return ui.repos[target.id+"\x00"+project]
	}
	return ui.repos["local\x00"+project]
}
