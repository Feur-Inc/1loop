// SPDX-License-Identifier: MPL-2.0

package server

// Messages between the server's chats (agent/chats.go): the chats tool's
// list and messages, and the reports a turn sends back when it ends. Every
// message, a subchat's included, goes through wakeLocked.

import (
	"context"
	"fmt"
	"path/filepath"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// relationLocked is how chat stands to other: parent, subchat or "".
func relationLocked(chat, other *serverChat) string {
	switch {
	case other.Subchat != nil && other.Subchat.Parent == chat.ID:
		return agent.RelationSubchat
	case chat.Subchat != nil && chat.Subchat.Parent == other.ID:
		return agent.RelationParent
	}
	return ""
}

// chatInfoLocked is other as chat's chats tool lists it.
func (server *headlessServer) chatInfoLocked(chat, other *serverChat) agent.ChatInfo {
	waiting := other.run != nil && (other.run.approval != nil || other.run.question != nil)
	project := ""
	if !other.Config.InScratch() {
		folder := other.Config.Workspace
		if server.worktreeLocked(folder) {
			folder = server.worktrees[folder]
		}
		project = filepath.Base(folder)
	}
	return agent.ChatInfo{ID: other.ID, Title: agent.SubchatTitle(other.Title), Project: project,
		Machine: server.machineLabelLocked(other.Config.MachineID), State: agent.ChatState(other.run != nil, waiting),
		Relation: relationLocked(chat, other), Provider: other.Config.Provider}
}

// findLocked is the chat id that chat's tool names ("parent": its parent).
func (server *headlessServer) findLocked(chat *serverChat, id string) (*serverChat, error) {
	if id == "parent" {
		if parent := server.parentLocked(chat); parent != nil {
			return parent, nil
		}
		if chat.Subchat != nil {
			return nil, fmt.Errorf("This chat's parent conversation was deleted")
		}
		return nil, fmt.Errorf("This chat has no parent conversation")
	}
	if server.chatLocked(chat.ID) != chat {
		return nil, fmt.Errorf("This conversation was deleted")
	}
	if id == chat.ID {
		return nil, fmt.Errorf("That is this chat: give another chat's id")
	}
	other := server.chatLocked(id)
	if other == nil {
		return nil, fmt.Errorf("No chat %s on this 1loop server: it was deleted, or it is on another computer or server, which chats here cannot reach. List the chats you can message with action list", id)
	}
	return other, nil
}

// chatAccess is what chat's chats tool reaches: the server's other chats.
func (server *headlessServer) chatAccess(chat *serverChat) *agent.ChatAccess {
	return &agent.ChatAccess{
		Find: func(_ context.Context, id string) (agent.ChatInfo, error) {
			server.mu.Lock()
			defer server.mu.Unlock()
			other, err := server.findLocked(chat, id)
			if err != nil {
				return agent.ChatInfo{}, err
			}
			return server.chatInfoLocked(chat, other), nil
		},
		List: func(context.Context) (list []agent.ChatInfo) {
			server.mu.Lock()
			defer server.mu.Unlock()
			for _, other := range server.chats {
				if other != chat {
					list = append(list, server.chatInfoLocked(chat, other))
				}
			}
			return list
		},
		Send: func(ctx context.Context, id, text string, attachments agent.ChatAttachments) error {
			var files []agent.FileAttachment
			if len(attachments.Files) > 0 { // copied into its workspace first, outside the lock
				server.mu.Lock()
				other, err := server.findLocked(chat, id)
				var workspace string
				var machine agent.Machine
				if err == nil {
					workspace, machine = other.Config.Workspace, server.attachmentMachineLocked(other.Config.MachineID)
				}
				server.mu.Unlock()
				if err != nil {
					return err
				}
				if files, err = agent.DeliverFiles(ctx, machine, workspace, attachments.Files); err != nil {
					return err
				}
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			return server.sendLocked(chat, id, text, attachments.Images, files)
		},
	}
}

// attachmentMachineLocked is the SSH machine of an ID files are copied
// to, nil for the server's own computer.
func (server *headlessServer) attachmentMachineLocked(id string) agent.Machine {
	if machine := server.machineLocked(id); machine != nil {
		return machine
	}
	return nil
}

// sendLocked delivers text, and the pictures and files (already in its
// workspace) attached to it, from chat's agent to the chat id.
func (server *headlessServer) sendLocked(chat *serverChat, id, text string, images []core.ImagePart, files []agent.FileAttachment) error {
	other, err := server.findLocked(chat, id)
	if err != nil {
		return err
	}
	hops := chat.hops + 1
	if hops > agent.MaxChatHops {
		return agent.TooManyHops(hops)
	}
	sender := agent.ChatSender{ID: chat.ID, Title: agent.SubchatTitle(chat.Title), Hops: hops}
	server.wakeLocked(other, agent.ChatMessage(sender, relationLocked(other, chat), text, images, files))
	return nil
}

// turnEndedLocked is the end of a chat's last turn in a row: a subchat
// notes how it ended, and the chats that caused it hear it.
func (server *headlessServer) turnEndedLocked(chat *serverChat, ended string) {
	if chat.Subchat != nil {
		chat.Subchat.Ended = ended
		if parent := server.parentLocked(chat); parent != nil {
			server.broadcastLocked("meta", server.metaLocked(parent))
		}
	}
	reportTo := chat.ReportTo
	chat.ReportTo = nil
	for _, id := range reportTo {
		target := server.chatLocked(id)
		if target == nil {
			continue // deleted meanwhile
		}
		info := agent.SubchatInfo{ID: chat.ID, Title: agent.SubchatTitle(chat.Title), State: agent.SubchatState(false, false, ended),
			Folder: chat.Config.Workspace, Last: agent.LastAnswer(chat.Items)}
		if chat.Subchat != nil {
			info = server.subchatInfoLocked(chat)
		}
		sender := agent.ChatSender{ID: chat.ID, Title: info.Title, Hops: chat.hops + 1}
		server.wakeLocked(target, agent.TurnReport(sender, relationLocked(target, chat), info))
	}
}
