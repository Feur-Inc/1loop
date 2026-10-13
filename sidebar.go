package main

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
)

const sidebarWidth = 280

// paneLayout places the sidebar and the main area edge to edge (Fyne's
// Border inserts a padding gap between them) and the shadow over the main
// area's left edge. shown (0..1) slides the sidebar out when it is folded.
type paneLayout struct {
	shown float32
	shade float32
}

func (l *paneLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	main := objects[1].MinSize()
	return fyne.NewSize(sidebarWidth*l.shown+main.Width, main.Height)
}

func (l *paneLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := sidebarWidth * l.shown
	objects[0].Move(fyne.NewPos(width-sidebarWidth, 0))
	objects[0].Resize(fyne.NewSize(sidebarWidth, size.Height))
	objects[1].Move(fyne.NewPos(width, 0))
	objects[1].Resize(fyne.NewSize(size.Width-width, size.Height))
	objects[2].Move(fyne.NewPos(width, 0))
	objects[2].Resize(fyne.NewSize(l.shade, size.Height))
}

// toggleSidebar folds or unfolds the sidebar with an eased slide (Ctrl+B).
func (u *desktopUI) toggleSidebar() {
	from := u.pane.shown
	to := float32(1)
	if from > 0.5 {
		to = 0
	}
	sidebar := u.root.Objects[0]
	shade := u.root.Objects[2]
	sidebar.Show()
	shade.Show()
	if u.paneAnim != nil {
		u.paneAnim.Stop()
	}
	u.paneAnim = fyne.NewAnimation(240*time.Millisecond, func(p float32) {
		u.pane.shown = from + (to-from)*p
		relayout(u.root) // never Refresh: it would redraw the whole conversation on every frame
		if p >= 1 && to == 0 {
			sidebar.Hide()
			shade.Hide()
		}
	})
	u.paneAnim.Curve = easeOutQuint
	u.paneAnim.Start()
	if to == 0 {
		u.expand.Show()
	} else {
		u.expand.Hide()
	}
	u.headerBar.Refresh()
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
	// The button stays visible while its menu is open, so the menu keeps a
	// stable anchor even when the pointer leaves the row to reach it.
	show := func() {
		menuOpen = true
		more.Show()
		if menu := open(more); menu != nil {
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

func (u *desktopUI) ensureProject(path string) {
	if path == "" {
		return
	}
	for _, p := range u.projects {
		if p == path {
			return
		}
	}
	u.projects = append(u.projects, path)
}

func (u *desktopUI) selectProject(path string) {
	if path == u.cfg.Workspace {
		return
	}
	devf("ui", "project %s", path)
	u.cfg.Workspace = path
	u.ensureProject(path)
	u.current = nil
	u.save()
	u.startNew()
}

func (u *desktopUI) addProject() {
	start := filepath.Dir(u.cfg.Workspace)
	u.pick(pickRequest{title: "Add a project folder", start: start, folder: true}, func(paths []string) {
		root, err := resolveWorkspace(paths[0])
		if err != nil {
			u.notice("Add project", err.Error())
			return
		}
		u.ensureProject(root)
		if root == u.cfg.Workspace {
			u.renderChats()
			return
		}
		u.selectProject(root)
	})
}

func (u *desktopUI) chatsIn(path string) []*conversation {
	var chats []*conversation
	for _, chat := range u.chats {
		if chat.Config.Workspace == path {
			chats = append(chats, chat)
		}
	}
	return chats
}

func (u *desktopUI) removeProject(path string) {
	chats := u.chatsIn(path)
	text := fmt.Sprintf("“%s” will no longer be listed in 1loop.", filepath.Base(path))
	if len(chats) > 0 {
		text += fmt.Sprintf(" Its %d chat(s) will be removed from 1loop.", len(chats))
	}
	text += " No files in the directory will be changed."
	u.confirm("Remove project?", text, "Remove", func() {
		var kept []*conversation
		for _, chat := range u.chats {
			if chat.Config.Workspace != path {
				kept = append(kept, chat)
			} else if chat.run != nil {
				chat.run.cancel()
			}
		}
		u.chats = kept
		var projects []string
		for _, p := range u.projects {
			if p != path {
				projects = append(projects, p)
			}
		}
		u.projects = projects
		if u.cfg.Workspace == path {
			next := ""
			if len(projects) > 0 {
				next = projects[0]
			}
			u.cfg.Workspace = next
			u.current = nil
			u.startNew()
		}
		u.save()
		u.renderChats()
	})
}

func (u *desktopUI) projectMenu(anchor fyne.CanvasObject) {
	var actions []menuAction
	for _, p := range u.projects {
		p := p
		var icon fyne.Resource = blankIcon
		if p == u.cfg.Workspace {
			icon = iconCheck
		}
		actions = append(actions, menuAction{label: filepath.Base(p), icon: icon, run: func() { u.selectProject(p) }})
	}
	actions = append(actions, menuAction{label: "Add folder…", icon: iconPlus, run: u.addProject})
	showMenu(u.window, anchor, actions)
}

// ---------------------------------------------------------------------------
// Conversations: open, rename, delete.

func (u *desktopUI) openChat(chat *conversation) {
	if chat == u.current {
		return
	}
	u.reloadChat(chat)
}

func (u *desktopUI) reloadChat(chat *conversation) {
	devf("ui", "open chat %q [%s]: %d items, running %v", chat.Title, chat.ID, len(chat.Items), chat.run != nil)
	defer devSlow("opening the chat", 30*time.Millisecond, time.Now())
	u.finishEdit(false)
	u.prefs.close()
	u.clearSelection()
	u.current = chat
	u.cfg = chat.Config
	u.followTail = true
	u.panel.switchTo(chat)
	u.transcript.RemoveAll()
	u.items.reset()
	u.views, u.copyRows = map[int]any{}, map[int]*fyne.Container{}
	u.building = true
	for i := range chat.Items {
		u.appendItem(chat, i)
	}
	u.building = false
	u.showTranscript(true)
	u.syncRun()
	u.updateHeader()
	u.renderChats()
	u.showChatModel()
	u.refreshContextRing()
}

func (u *desktopUI) renameChat(chat *conversation) {
	entry := widget.NewEntry()
	entry.SetText(chat.Title)
	var s *sheet
	apply := func() {
		if title := []rune(entry.Text); len(title) > 0 {
			if len(title) > 80 {
				title = title[:80]
			}
			chat.Title = string(title)
			u.save()
			u.updateHeader()
			u.renderChats()
		}
		s.Hide()
	}
	entry.OnSubmitted = func(string) { apply() }
	cancel := newRow("Cancel", nil, styleOutline, func() { s.dismiss() })
	ok := newRow("Rename", nil, stylePrimary, apply)
	s = u.modal("Rename chat", entry, 420, nil, cancel, ok)
	u.window.Canvas().Focus(entry)
}

func (u *desktopUI) deleteChat(chat *conversation) {
	u.confirm("Delete chat?", fmt.Sprintf("“%s” will be permanently deleted from 1loop.", chat.Title), "Delete", func() {
		var kept []*conversation
		for _, c := range u.chats {
			if c != chat {
				kept = append(kept, c)
			}
		}
		u.chats = kept
		if chat.run != nil {
			chat.run.cancel()
		}
		if u.panel.owner != chat {
			chat.disposePanel() // its terminals end with it
		}
		if u.current == chat {
			u.current = nil
			u.startNew()
		}
		u.save()
		u.renderChats()
	})
}

func (u *desktopUI) chatMenu(chat *conversation, anchor fyne.CanvasObject) *sheet {
	return showMenu(u.window, anchor, []menuAction{
		{label: "Rename", icon: iconRename, run: func() { u.renameChat(chat) }},
		{label: "Delete", icon: iconTrash, danger: true, run: func() { u.deleteChat(chat) }},
	})
}

// confirm asks before a destructive action.
func (u *desktopUI) confirm(title, text, verb string, run func()) {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	var s *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { s.dismiss() })
	ok := newRow(verb, nil, stylePrimary, func() { s.Hide(); run() })
	ok.danger = true
	s = u.modal(title, label, 440, nil, cancel, ok)
}

// renderChats redraws the project list and the current project's conversations.
func (u *desktopUI) renderChats() {
	u.remote.chatsChanged()
	u.ensureProject(u.cfg.Workspace)
	u.projectList.RemoveAll()
	for _, p := range u.projects {
		p := p
		row := newRow(filepath.Base(p), iconFolder, styleNav, func() { u.selectProject(p) })
		row.selected = p == u.cfg.Workspace
		row.accent = true
		u.projectList.Add(withMore(row, func(anchor fyne.CanvasObject) *sheet {
			return showMenu(u.window, anchor, []menuAction{
				{label: "New chat", icon: iconPlus, run: func() { u.selectProject(p); u.startNew() }},
				{label: "Remove from 1loop", icon: iconTrash, danger: true, run: func() { u.removeProject(p) }},
			})
		}))
	}
	u.chatList.RemoveAll()
	chats := u.chatsIn(u.cfg.Workspace)
	for _, chat := range chats {
		chat := chat
		row := newRow(chat.Title, nil, styleNav, func() { u.openChat(chat) })
		row.accent = true
		row.selected = chat == u.current
		if chat.run != nil {
			row.badge = iconDot // the agent works on it in the background
		}
		u.chatList.Add(withMore(row, func(anchor fyne.CanvasObject) *sheet { return u.chatMenu(chat, anchor) }))
	}
	if len(chats) == 0 {
		u.chatList.Add(padded(4, 0, 10, 0, textLine("No chats yet", 13, colFaint)))
	}
	if u.sideLists != nil {
		u.sideLists.Refresh() // both lists changed height: lay the column out again
	}
}

// buildSidebar assembles the sidebar column.
func (u *desktopUI) buildSidebar() fyne.CanvasObject {
	u.newChat = newRow("New chat", theme.ContentAddIcon(), styleNav, u.startNew)
	u.newChat.trailing = "Ctrl K"
	u.connections = newRow("Settings", theme.SettingsIcon(), styleNav, u.settings)
	u.projectList = container.New(layout.NewCustomPaddedVBoxLayout(1))
	u.chatList = container.New(layout.NewCustomPaddedVBoxLayout(1))

	logo := brandImage("1loop-logo.png", 30*1483.0/512.0, 30)
	collapse := newRow("", iconSidebar, styleWindow, u.toggleSidebar)
	var brand fyne.CanvasObject = padded(10, 12, 0, 0, container.NewBorder(nil, nil, logo, container.NewCenter(collapse)))
	if u.chrome {
		brand = container.NewStack(newDragArea(u.window), brand)
	}
	addProject := newRow("", iconPlus, styleWindow, u.addProject)
	u.sideLists = container.NewVBox(
		sectionHeader("Projects", addProject), u.projectList,
		sectionHeader("Conversations", nil), u.chatList)
	lists := u.sideLists
	divider := canvas.NewRectangle(colBorderSoft)
	divider.SetMinSize(fyne.NewSize(0, 1))
	bottom := container.NewVBox(divider, padded(6, 8, 0, 0, u.connections))
	// Under the logo: the chats, or the settings' sections while they are open.
	u.sideApp = container.NewBorder(u.newChat, bottom, nil, nil, newKineticVScroll(lists))
	content := padded(0, 0, 12, 12, container.NewBorder(brand, nil, nil, nil, container.NewStack(u.sideApp, u.prefs.side)))
	return container.NewStack(canvas.NewRectangle(colSidebar), content)
}
