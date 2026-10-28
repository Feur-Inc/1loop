package ui

import (
	"fmt"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
)

const sidebarWidth = 280

// paneLayout places the sidebar and the main area edge to edge (Fyne's
// Border inserts a padding gap between them) and the shadow over the main
// area's left edge. shown (0..1) slides the sidebar out when it is folded.
type paneLayout struct {
	shown float32
	shade float32
}

func (layoutSpec *paneLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	main := objects[1].MinSize()
	return fyne.NewSize(sidebarWidth*layoutSpec.shown+main.Width, main.Height)
}

func (layoutSpec *paneLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := sidebarWidth * layoutSpec.shown
	objects[0].Move(fyne.NewPos(width-sidebarWidth, 0))
	objects[0].Resize(fyne.NewSize(sidebarWidth, size.Height))
	objects[1].Move(fyne.NewPos(width, 0))
	objects[1].Resize(fyne.NewSize(size.Width-width, size.Height))
	objects[2].Move(fyne.NewPos(width, 0))
	objects[2].Resize(fyne.NewSize(layoutSpec.shade, size.Height))
}

// toggleSidebar folds or unfolds the sidebar with an eased slide (Ctrl+B).
func (ui *desktopUI) toggleSidebar() {
	from := ui.pane.shown
	to := float32(1)
	if from > 0.5 {
		to = 0
	}
	sidebar := ui.root.Objects[0]
	shade := ui.root.Objects[2]
	sidebar.Show()
	shade.Show()
	if ui.paneAnim != nil {
		ui.paneAnim.Stop()
	}
	ui.paneAnim = fyne.NewAnimation(240*time.Millisecond, func(p float32) {
		ui.pane.shown = from + (to-from)*p
		relayout(ui.root) // never Refresh: it would redraw the whole conversation on every frame
		if p >= 1 && to == 0 {
			sidebar.Hide()
			shade.Hide()
		}
	})
	ui.paneAnim.Curve = easeOutQuint
	ui.paneAnim.Start()
	if to == 0 {
		ui.expand.Show()
	} else {
		ui.expand.Hide()
	}
	ui.headerBar.Refresh()
}

func sectionHeader(text string, action fyne.CanvasObject) fyne.CanvasObject {
	label := padded(16, 6, 10, 0, textLine(text, 13, colFaint))
	if action == nil {
		return label
	}
	return container.NewBorder(nil, nil, nil, padded(10, 0, 0, 2, action), label)
}

// withMore adds a "⋯" button that appears while the row is hovered; it and a
// right click on the row open the row's context menu.
func withMore(row *tapRow, open func(anchor fyne.CanvasObject) *sheet) fyne.CanvasObject {
	more := newRow("", iconMore, styleWindow, nil)
	row.endInset = more.MinSize().Width + 4
	more.Hide()
	var rowIn, moreIn, menuOpen bool
	var hide *time.Timer
	sync := func() {
		if hide != nil {
			hide.Stop()
		}
		if (rowIn || moreIn || menuOpen) && !row.disabled {
			more.Show()
			return
		}
		// Small grace period so the pointer can travel from the row to the button.
		hide = time.AfterFunc(90*time.Millisecond, func() {
			fyne.Do(func() {
				if !rowIn && !moreIn && !menuOpen {
					more.Hide()
				}
			})
		})
	}
	row.onHover = func(in bool) { rowIn = in; sync() }
	more.onHover = func(in bool) { moreIn = in; sync() }
	// Anchor to the always-visible row: the hidden button may not have been laid out yet.
	show := func() {
		menuOpen = true
		more.Show()
		if menu := open(row); menu != nil {
			menu.onClosed = func() { menuOpen = false; sync() }
		} else {
			menuOpen = false
			sync()
		}
	}
	more.onTap = show
	row.onMenu = show
	return container.NewStack(row, container.NewBorder(nil, nil, nil, padded(0, 0, 0, 4, container.NewCenter(more))))
}

// ---------------------------------------------------------------------------
// Projects: the folders 1loop works in. Removing one only forgets it in 1loop.

func (ui *desktopUI) ensureProject(path string) {
	if path == "" || ui.worktrees[path] != "" { // a worktree shows under its project
		return
	}
	for _, p := range ui.projects {
		if p == path {
			return
		}
	}
	ui.projects = append(ui.projects, path)
}

func (ui *desktopUI) selectProject(path string) {
	if ui.server != nil {
		ui.server.selectProject(path)
		return
	}
	if path == ui.cfg.Workspace {
		return
	}
	platform.Devf("ui", "project %s", path)
	ui.cfg.Workspace = path
	if ui.worktreeMap()[path] == "" {
		ui.ensureTargetProject(path)
	}
	ui.current = nil
	ui.save()
	ui.startNew()
}

func (ui *desktopUI) addProject() {
	if ui.server == nil && ui.cfg.MachineID != "" {
		ui.addMachineProject()
		return
	}
	start := filepath.Dir(ui.cfg.Workspace)
	ui.pick(pickRequest{title: "Add a project folder", start: start, folder: true}, func(paths []string) {
		root, err := agent.ResolveWorkspace(paths[0])
		if err != nil {
			ui.notice("Add project", err.Error())
			return
		}
		ui.ensureProject(root)
		if root == ui.cfg.Workspace {
			ui.renderChats()
			return
		}
		ui.selectProject(root)
	})
}

// chatsIn are the chats of a project of the target.
func (ui *desktopUI) chatsIn(path string) []*conversation {
	var chats []*conversation
	for _, chat := range ui.chats {
		if chat.Config.Workspace == path && chat.Config.MachineID == ui.cfg.MachineID {
			chats = append(chats, chat)
		}
	}
	return chats
}

func (ui *desktopUI) renameProject(path string) {
	entry := widget.NewEntry()
	entry.SetText(filepath.Base(path))
	problem := widget.NewLabel("")
	problem.Wrapping = fyne.TextWrapWord
	var dialog *sheet
	apply := func() {
		if _, err := ui.changeProject("rename", remoteapi.ProjectChange{Path: path, Name: entry.Text}); err != nil {
			problem.SetText(err.Error())
			return
		}
		dialog.Hide()
	}
	entry.OnSubmitted = func(string) { apply() }
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	rename := newRow("Rename", nil, stylePrimary, apply)
	body := container.NewVBox(note("This also renames the project folder. Its files and subfolders are preserved."), entry, problem)
	dialog = ui.modal("Rename project", body, 480, nil, cancel, rename)
	ui.window.Canvas().Focus(entry)
}

func (ui *desktopUI) removeProject(path string) {
	chats := ui.chatsIn(path)
	text := fmt.Sprintf("“%s” will no longer be listed in 1loop.", ui.projectName(path))
	if len(chats) > 0 {
		text += fmt.Sprintf(" Its %d chat(s) will be removed from 1loop.", len(chats))
	}
	text += " No files in the directory will be changed."
	ui.confirm("Remove project?", text, "Remove", func() {
		var kept []*conversation
		for _, chat := range ui.chats {
			if chat.Config.Workspace != path || chat.Config.MachineID != ui.cfg.MachineID {
				kept = append(kept, chat)
			} else if chat.run != nil {
				chat.run.cancel()
			}
		}
		ui.chats = kept
		var projects []string
		for _, p := range ui.targetProjects() {
			if p != path {
				projects = append(projects, p)
			}
		}
		ui.setTargetProjects(projects)
		if ui.cfg.Workspace == path {
			next := ""
			if len(projects) > 0 {
				next = projects[0]
			}
			ui.cfg.Workspace = next
			ui.current = nil
			ui.startNew()
		}
		ui.save()
		ui.renderChats()
	})
}

func (ui *desktopUI) projectMenu(anchor fyne.CanvasObject) {
	if ui.server != nil {
		ui.server.projectMenu(anchor)
		return
	}
	var actions []menuAction
	for _, p := range ui.targetProjects() {
		p := p
		if ui.worktreeMap()[p] != "" {
			continue
		}
		var icon fyne.Resource = blankIcon
		if p == ui.cfg.Workspace {
			icon = iconCheck
		}
		actions = append(actions, menuAction{label: ui.projectName(p), icon: icon, run: func() { ui.selectProject(p) }})
		actions = append(actions, ui.worktreeChoices(p, ui.selectProject)...)
	}
	add := "Add folder…"
	if ui.cfg.MachineID != "" {
		add = "New project…"
	}
	actions = append(actions, menuAction{label: add, icon: iconPlus, run: ui.addProject})
	showMenu(ui.window, anchor, actions)
}

// ---------------------------------------------------------------------------
// Conversations: open, rename, delete.

func (ui *desktopUI) openChat(chat *conversation) {
	if chat == ui.current {
		return
	}
	ui.reloadChat(chat)
}

func (ui *desktopUI) reloadChat(chat *conversation) {
	platform.Devf("ui", "open chat %q [%s]: %d items, running %v", chat.Title, chat.ID, len(chat.Items), chat.run != nil)
	defer platform.DevSlow("opening the chat", 30*time.Millisecond, time.Now())
	ui.finishEdit(false)
	ui.prefs.close()
	ui.clearSelection()
	ui.current = chat
	ui.cfg = chat.Config
	ui.prompt.switchDraft(chat.ID)
	ui.followTail = true
	ui.panel.switchTo(chat)
	ui.transcript.RemoveAll()
	ui.items.reset()
	ui.views, ui.copyRows = map[int]any{}, map[int]*fyne.Container{}
	ui.building = true
	for i := range chat.Items {
		ui.appendItem(chat, i)
	}
	ui.building = false
	ui.showTranscript(true)
	ui.syncRun()
	ui.updateHeader()
	ui.renderChats()
	ui.showChatModel()
	ui.refreshContextRing()
}

func (ui *desktopUI) renameChat(chat *conversation) {
	entry := widget.NewEntry()
	entry.SetText(chat.Title)
	var dialog *sheet
	apply := func() {
		if title := []rune(entry.Text); len(title) > 0 {
			if len(title) > 80 {
				title = title[:80]
			}
			chat.Title = string(title)
			ui.save()
			ui.updateHeader()
			ui.renderChats()
		}
		dialog.Hide()
	}
	entry.OnSubmitted = func(string) { apply() }
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	row := newRow("Rename", nil, stylePrimary, apply)
	dialog = ui.modal("Rename chat", entry, 420, nil, cancel, row)
	ui.window.Canvas().Focus(entry)
}

func (ui *desktopUI) deleteChat(chat *conversation) {
	ui.confirm("Delete chat?", fmt.Sprintf("“%s” will be permanently deleted from 1loop.", chat.Title), "Delete", func() {
		ui.removeChat(chat)
	})
}

func (ui *desktopUI) removeChat(chat *conversation) {
	var kept []*conversation
	for _, conversationValue := range ui.chats {
		if conversationValue != chat {
			kept = append(kept, conversationValue)
		}
	}
	ui.chats = kept
	if chat.run != nil {
		chat.run.cancel()
	}
	chat.queue, chat.steer = nil, nil
	chat.tasks.StopAll()
	ui.prompt.drafts.set(chat.ID, "")
	if ui.current == chat {
		ui.current = nil
		ui.startNew()
	}
	chat.disposePanel()
	ui.save()
	ui.renderChats()
}

func (ui *desktopUI) chatMenu(chat *conversation, anchor fyne.CanvasObject) *sheet {
	return showMenu(ui.window, anchor, []menuAction{
		{label: "Rename", icon: iconRename, run: func() { ui.renameChat(chat) }},
		{label: "Delete", icon: iconTrash, danger: true, run: func() { ui.deleteChat(chat) }},
	})
}

// confirm asks before a destructive action.
func (ui *desktopUI) confirm(title, text, verb string, run func()) {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	row := newRow(verb, nil, stylePrimary, func() { dialog.Hide(); run() })
	row.danger = true
	dialog = ui.modal(title, label, 440, nil, cancel, row)
}

// renderChats redraws the project list and the current project's conversations.
func (ui *desktopUI) renderChats() {
	ui.remote.chatsChanged()
	if ui.server != nil {
		ui.server.renderChats()
		return
	}
	worktrees := ui.worktreeMap()
	if worktrees[ui.cfg.Workspace] == "" {
		ui.ensureTargetProject(ui.cfg.Workspace)
	}
	ui.projectList.RemoveAll()
	opened := ui.projectOf(ui.cfg.Workspace)
	for _, p := range ui.targetProjects() {
		p := p
		if worktrees[p] != "" {
			continue // shown under its project
		}
		row := newRow(ui.projectName(p), iconFolder, styleNav, func() { ui.selectProject(p) })
		row.selected = p == ui.cfg.Workspace
		row.accent = true
		row.truncate = true
		if state := ui.repoFor(p); state.repo != nil {
			row.trailing = state.repo.Current // the branch of the project's folder
		}
		ui.projectList.Add(withMore(row, func(anchor fyne.CanvasObject) *sheet {
			actions := []menuAction{{label: "New chat", icon: iconPlus, run: func() { ui.selectProject(p); ui.startNew() }}}
			actions = append(actions, ui.repositoryActions(p)...)
			if ui.cfg.MachineID == "" { // a machine's folder is renamed there, in its terminal
				actions = append(actions, menuAction{label: "Rename", icon: iconRename, run: func() { ui.renameProject(p) }})
			}
			actions = append(actions, menuAction{label: "Remove from 1loop", icon: iconTrash, danger: true, run: func() { ui.removeProject(p) }})
			return showMenu(ui.window, anchor, actions)
		}))
		if p == opened {
			for _, object := range ui.worktreeRows(p, ui.selectProject) {
				ui.projectList.Add(object)
			}
			ui.loadRepo(p, true, 20*time.Second)
		}
	}
	ui.chatList.RemoveAll()
	chats := ui.chatsIn(ui.cfg.Workspace)
	for _, chat := range chats {
		chat := chat
		row := newRow(chat.Title, nil, styleNav, func() { ui.openChat(chat) })
		row.accent = true
		row.selected = chat == ui.current
		if chat.run != nil {
			row.badge = iconDot // the agent works on it in the background
		}
		ui.chatList.Add(withMore(row, func(anchor fyne.CanvasObject) *sheet { return ui.chatMenu(chat, anchor) }))
	}
	if len(chats) == 0 {
		ui.chatList.Add(padded(4, 0, 10, 0, textLine("No chats yet", 13, colFaint)))
	}
	if ui.sideLists != nil {
		ui.sideLists.Refresh() // both lists changed height: lay the column out again
	}
}

// buildSidebar assembles the sidebar column.
func (ui *desktopUI) buildSidebar() fyne.CanvasObject {
	ui.newChat = newRow("New chat", theme.ContentAddIcon(), styleNav, ui.startNew)
	ui.newChat.trailing = "Ctrl K"
	ui.connections = newRow("Settings", theme.SettingsIcon(), styleNav, ui.settings)
	ui.projectList = container.New(layout.NewCustomPaddedVBoxLayout(1))
	ui.chatList = container.New(layout.NewCustomPaddedVBoxLayout(1))

	logo := brandImage("1loop-logo.png", 30*1483.0/512.0, 30)
	collapse := newRow("", iconSidebar, styleWindow, ui.toggleSidebar)
	var brand fyne.CanvasObject = padded(10, 12, 0, 0, container.NewBorder(nil, nil, logo, container.NewCenter(collapse)))
	if ui.chrome {
		brand = container.NewStack(newDragArea(ui.window), brand)
	}
	addProject := newRow("", iconPlus, styleWindow, ui.addProject)
	ui.sideLists = container.NewVBox(
		sectionHeader("Projects", addProject), ui.projectList,
		sectionHeader("Conversations", nil), ui.chatList)
	lists := ui.sideLists
	divider := canvas.NewRectangle(colBorderSoft)
	divider.SetMinSize(fyne.NewSize(0, 1))
	// A new version shows above Settings until it is installed.
	ui.updateRow = newRow("", iconArrowTurn, styleNav, func() { ui.settings(); ui.prefs.show("Updates") })
	ui.updateRow.Hide()
	bottom := container.NewVBox(divider, padded(6, 0, 0, 0, ui.updateRow), padded(0, 8, 0, 0, ui.connections))
	// Under the logo: the chats, or the settings' sections while they are open.
	ui.sideApp = container.NewBorder(ui.newChat, bottom, nil, nil, newKineticVScroll(lists))
	content := padded(0, 0, 12, 12, container.NewBorder(brand, nil, nil, nil, container.NewStack(ui.sideApp, ui.prefs.side)))
	return container.NewStack(canvas.NewRectangle(colSidebar), content)
}
