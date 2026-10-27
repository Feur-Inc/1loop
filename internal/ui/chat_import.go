package ui

// Import from Claude Code and Codex (Settings > General): their sessions on
// this computer, grouped by the folder they worked in, copied into 1loop as
// chats of that folder's project. A whole project or single chats can be
// chosen; a chat imported once is marked and not imported twice.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

func (ui *desktopUI) importRow() fyne.CanvasObject {
	return settingRow("Claude Code and Codex chats",
		"Copy your Claude Code and Codex conversations on this computer into 1loop, with their messages, tool calls and edits. Choose whole projects or single chats: each one goes to the project of the folder it worked in. A Claude Code chat continued with a Claude Code model resumes its own session; another model receives it through a handoff.",
		newRow("Import…", nil, styleOutline, ui.importChats))
}

// importedChatID is the 1loop chat a session becomes: always the same, so
// a second import finds it.
func importedChatID(session agent.ImportSession) string {
	sum := sha256.Sum256([]byte(session.Source + ":" + session.ID))
	return "import" + hex.EncodeToString(sum[:5])
}

// alreadyImported tells whether 1loop has the session: imported before, or
// a Claude Code session 1loop ran itself.
func (ui *desktopUI) alreadyImported(session agent.ImportSession) bool {
	if ui.chatByID(importedChatID(session)) != nil {
		return true
	}
	if session.Source == "claudecode" {
		for _, chat := range ui.chats {
			if chat.Config.Session == session.ID {
				return true
			}
		}
	}
	return false
}

func importAge(t time.Time) string {
	switch age := time.Since(t); {
	case age < time.Hour:
		return "just now"
	case age < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(age.Hours()))
	case age < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(age.Hours()/24))
	}
	return t.Format("2 Jan 2006")
}

func (ui *desktopUI) importChats() {
	settings := ui.prefs
	list := container.New(layout.NewCustomPaddedVBoxLayout(0))
	list.Add(padded(12, 0, 2, 0, textLine("Looking for chats…", 13, colFaint)))
	status := textLine("", 13, colMuted)
	importButton := newRow("Import", nil, stylePrimary, nil)
	importButton.disabled = true
	page := container.NewVBox(
		settings.pageTitle("Import chats", "General", nil),
		note("Conversations found in Claude Code's and Codex's history on this computer, by the folder they worked in. Choose a project to take all its chats, or single chats. The files are only read: Claude Code and Codex keep them."),
		list,
	)
	left, busy := false, false
	cancel := newRow("Cancel", nil, styleOutline, settings.back)
	settings.push(settingsFrame{content: page, actions: []fyne.CanvasObject{
		container.NewCenter(sized(360, status)),
		cancel,
		importButton,
	}, leave: func() { left = true }, beforeLeave: func(next func()) {
		if !busy {
			next()
		}
	}})

	var sessions []agent.ImportSession
	selected := map[int]bool{}
	var render func()
	importable := func(i int) bool {
		session := sessions[i]
		if ui.alreadyImported(session) {
			return false
		}
		info, err := os.Stat(session.Folder)
		return err == nil && info.IsDir()
	}
	count := func() {
		n := 0
		for i := range sessions {
			if selected[i] {
				n++
			}
		}
		importButton.disabled = n == 0 || busy
		importButton.text = "Import"
		if n > 0 {
			importButton.text = fmt.Sprintf("Import %d chat", n)
			if n > 1 {
				importButton.text += "s"
			}
		}
		importButton.Refresh()
	}
	render = func() {
		// Projects in the order of their latest chat, the newest first.
		var folders []string
		byFolder := map[string][]int{}
		for i, session := range sessions {
			if _, ok := byFolder[session.Folder]; !ok {
				folders = append(folders, session.Folder)
			}
			byFolder[session.Folder] = append(byFolder[session.Folder], i)
		}
		list.RemoveAll()
		if len(sessions) == 0 {
			list.Add(padded(12, 0, 2, 0, textLine("No Claude Code or Codex chats were found on this computer.", 13, colFaint)))
		}
		known := map[string]bool{}
		for _, project := range ui.projects {
			known[project] = true
		}
		for _, folder := range folders {
			indexes := byFolder[folder]
			var open []int
			for _, i := range indexes {
				if importable(i) {
					open = append(open, i)
				}
			}
			all := len(open) > 0
			for _, i := range open {
				all = all && selected[i]
			}
			detail := folder
			if _, err := os.Stat(folder); err != nil {
				detail += " · folder not found"
			} else if !known[folder] {
				detail += " · new project"
			}
			detail += fmt.Sprintf(" · %d chat", len(indexes))
			if len(indexes) > 1 {
				detail += "s"
			}
			project := newRow(filepath.Base(folder), iconFolder, styleModel, func() {
				for _, i := range open {
					selected[i] = !all
				}
				render()
			})
			project.detail, project.truncate, project.badge = detail, true, blankIcon
			if all {
				project.badge = iconCheck
			}
			project.disabled = len(open) == 0 || busy
			rows := []fyne.CanvasObject{project}
			for _, i := range indexes {
				i, session := i, sessions[i]
				detail := session.SourceName() + " · " + importAge(session.Updated)
				if ui.alreadyImported(session) {
					detail += " · in 1loop"
				}
				icon := modelAccountBadge("claudecode")
				if session.Source == "codex" {
					icon = modelAccountBadge("openai")
				}
				chat := newRow(session.Title, icon, styleModel, func() {
					selected[i] = !selected[i]
					render()
				})
				chat.detail, chat.truncate, chat.badge = detail, true, blankIcon
				if selected[i] {
					chat.badge = iconCheck
				}
				chat.disabled = !importable(i) || busy
				rows = append(rows, padded(0, 0, 22, 0, chat))
			}
			list.Add(padded(4, 0, 0, 0, card(rows...)))
		}
		list.Refresh()
		count()
	}

	importButton.onTap = func() {
		var chosen []agent.ImportSession
		for i := range sessions {
			if selected[i] && importable(i) {
				chosen = append(chosen, sessions[i])
			}
		}
		if len(chosen) == 0 || busy {
			return
		}
		busy = true
		render()
		claude := ""
		for _, conn := range ui.connectionsList {
			if conn.Provider == "claudecode" && conn.Enabled {
				claude = conn.ID
				break
			}
		}
		go func() {
			var failures []string
			added := 0
			// The oldest first: each new chat goes on top of its project.
			sort.SliceStable(chosen, func(i, j int) bool { return chosen[i].Updated.Before(chosen[j].Updated) })
			for n, session := range chosen {
				fyne.Do(func() { status.Text = fmt.Sprintf("Importing %d of %d…", n+1, len(chosen)); status.Refresh() })
				read, err := agent.ReadImportSession(session)
				if err != nil {
					failures = append(failures, session.Title+": "+err.Error())
					continue
				}
				fyne.Do(func() {
					if ui.chatByID(importedChatID(session)) != nil {
						return
					}
					ui.addImportedChat(session, read, claude)
					added++
				})
			}
			fyne.Do(func() {
				busy = false
				ui.persist(false)
				ui.renderChats()
				ui.remote.chatsChanged()
				status.Text = fmt.Sprintf("%d chat", added)
				if added != 1 {
					status.Text += "s"
				}
				status.Text += " imported"
				status.Refresh()
				cancel.text = "Done"
				cancel.Refresh()
				if len(failures) > 0 {
					ui.notice("Some chats were not imported", strings.Join(failures, "\n"))
				}
				if !left {
					for i := range selected {
						delete(selected, i)
					}
					render()
				}
			})
		}()
	}

	go func() {
		found := agent.FindImportSessions()
		fyne.Do(func() {
			if left {
				return
			}
			sessions = found
			render()
		})
	}()
}

// addImportedChat adds a session as a chat of its folder's project. It keeps
// the model the chat now uses; the next turn knows the chat ran elsewhere:
// Claude Code resumes its own session, another model goes through a handoff.
func (ui *desktopUI) addImportedChat(session agent.ImportSession, read agent.ImportedChat, claudeConnection string) {
	cfg := ui.cfg
	cfg.Workspace, cfg.Session, cfg.SessionMark, cfg.WebConversation = session.Folder, "", 0, nil
	ran := agent.RunIdentity{Provider: session.Source, Model: read.Model}
	if session.Source == "claudecode" {
		cfg.Session, cfg.SessionMark = session.ID, len(read.Items)
		ran = agent.RunIdentity{Provider: "claudecode", ConnectionID: claudeConnection}
		for _, conn := range ui.connectionsList {
			if conn.ID == claudeConnection {
				ran.AuthProfile = conn.AuthProfile
			}
		}
	}
	cfg.RanWith = &ran
	ui.ensureProject(session.Folder)
	ui.chats = append([]*conversation{{ID: importedChatID(session), Title: session.Title, Config: cfg,
		History: read.History, Items: read.Items, Todo: &agent.TodoState{}}}, ui.chats...)
}
