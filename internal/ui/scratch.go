// SPDX-License-Identifier: MPL-2.0

package ui

// Chats without a folder (#258): "No folder" in the project menu starts a
// chat in a folder 1loop makes for it (agent/scratch.go). The start screen
// is in that mode while ui.cfg's workspace is the scratch base itself.

import (
	"context"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

const noFolderLabel = "No folder"

// scratchBase holds this computer's chat folders, beside the state file.
func (ui *desktopUI) scratchBase() string {
	return filepath.Join(filepath.Dir(ui.statePath), "scratch")
}

// chooseNoFolder puts the start screen in "No folder" mode.
func (ui *desktopUI) chooseNoFolder() {
	base := ui.scratchBase()
	if err := os.MkdirAll(base, 0o700); err != nil {
		ui.notice(noFolderLabel, err.Error())
		return
	}
	ui.cfg.MachineID = ""
	ui.cfg.Workspace, ui.cfg.Scratch = base, base
	ui.current = nil
	ui.save()
	ui.startNew()
}

// leaveScratch makes a new chat's start screen ready for "No folder" again
// when the chat that was open had one: the next chat gets its own folder.
func (ui *desktopUI) leaveScratch() {
	if ui.cfg.InScratch() {
		ui.cfg.Workspace, ui.cfg.Scratch = ui.scratchBase(), ui.scratchBase()
	}
}

// scratchChat gives a new chat its folder when the start screen is in "No
// folder" mode; false when the folder could not be made.
func (ui *desktopUI) scratchChat(chat *conversation) bool {
	if !ui.cfg.InScratch() {
		chat.Config.Scratch = ""
		return true
	}
	path, err := agent.NewScratchWorkspace(ui.scratchBase())
	if err != nil {
		ui.notice(noFolderLabel, "1loop could not make a folder for this chat: "+err.Error())
		return false
	}
	chat.Config.Workspace, chat.Config.Scratch = path, path
	ui.cfg.Workspace, ui.cfg.Scratch = path, path
	return true
}

// removeScratch deletes a deleted chat's folder.
func (ui *desktopUI) removeScratch(chat *conversation) {
	if path := chat.Config.Scratch; path != "" {
		go func() { _ = agent.RemoveScratchWorkspace(ui.scratchBase(), path) }()
	}
}

// scratchChats are the chats still working in their own folder.
func (ui *desktopUI) scratchChats() []*conversation {
	var chats []*conversation
	for _, chat := range ui.chats {
		if chat.Config.InScratch() && chat.Config.MachineID == "" {
			chats = append(chats, chat)
		}
	}
	return chats
}

// scratchMove lets a scratch chat's run move it: the folder picker for
// request_directory, the user's projects for its prompt.
func (ui *desktopUI) scratchMove(cfg *agent.Config) {
	if !cfg.InScratch() || cfg.MachineID != "" || ui.server != nil {
		return
	}
	cfg.RecentProjects = append([]string(nil), ui.projects...)
	cfg.Move = &agent.WorkspaceMove{Pick: func(ctx context.Context, reason string) (string, error) {
		chosen := make(chan string, 1)
		fyne.Do(func() {
			title := "Choose a folder for this chat"
			if reason != "" {
				title += ": " + reason
			}
			home, _ := os.UserHomeDir()
			ui.pick(pickRequest{title: title, start: home, folder: true, always: true}, func(paths []string) {
				if len(paths) == 0 {
					chosen <- ""
					return
				}
				chosen <- paths[0]
			})
		})
		select {
		case path := <-chosen:
			return path, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
}

// finishMove moves a chat whose turn was approved to, and tells its agent
// what was copied, which starts its next turn there.
func (ui *desktopUI) finishMove(chat *conversation, move *agent.WorkspaceMove) {
	ui = ui.viewOf(chat)                                     // the window showing it (windows.go)
	if move.Target() == "" || ui.chatByID(chat.ID) != chat { // deleted meanwhile
		return
	}
	note := agent.FinishMove(&chat.Config, move)
	if note == "" {
		return
	}
	ui.ensureProject(chat.Config.Workspace)
	if chat == ui.current {
		ui.cfg.Workspace, ui.cfg.Scratch = chat.Config.Workspace, chat.Config.Scratch
		ui.updateHeader()
	}
	ui.save()
	ui.renderChats()
	ui.remote.metaChanged(chat)
	ui.wake(chat, agent.Message{Role: "user", Internal: true, Content: note})
}
