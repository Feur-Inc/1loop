// SPDX-License-Identifier: MPL-2.0

package server

import (
	"path/filepath"
	"slices"

	"1loop/internal/agent"
)

// scratchBase holds the folders of chats started without one (#258): a
// hidden folder of the phone's projects folder, which its tools may reach,
// or beside the server's state, out of every project.
func (server *headlessServer) scratchBase() string {
	if server.local {
		return filepath.Join(server.projectBase(), ".1loop-scratch")
	}
	return filepath.Join(server.dir, "scratch")
}

// scratchChatLocked says whether folder is the folder of a chat without one.
func (server *headlessServer) scratchChatLocked(folder string) bool {
	for _, chat := range server.chats {
		if chat.Config.InScratch() && chat.Config.Workspace == folder {
			return true
		}
	}
	return false
}

// finishMoveLocked moves a chat whose turn was approved to, and returns
// what its agent is told, "" when it stays.
func (server *headlessServer) finishMoveLocked(chat *serverChat, move *agent.WorkspaceMove) string {
	if move.Target() == "" {
		return ""
	}
	moved := agent.FinishMove(&chat.Config, move)
	if moved != "" && !slices.Contains(server.projects, chat.Config.Workspace) && !server.worktreeLocked(chat.Config.Workspace) {
		server.projects = append(server.projects, chat.Config.Workspace)
		_ = server.saveSettingsLocked()
		server.broadcastLocked("projects", map[string]any{"projects": server.projects, "worktrees": server.worktrees})
	}
	return moved
}
