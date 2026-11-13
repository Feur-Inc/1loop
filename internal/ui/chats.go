// SPDX-License-Identifier: MPL-2.0

package ui

// Messages between this computer's chats (agent/chats.go): the chats
// tool's list and messages, and the reports a turn sends back when it
// ends. Every message, a subchat's included, goes through wake.

import (
	"context"
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/agent"
)

// chatRelation is how chat stands to other: parent, subchat or "".
func chatRelation(chat, other *conversation) string {
	switch {
	case other.Subchat != nil && other.Subchat.Parent == chat.ID:
		return agent.RelationSubchat
	case chat.Subchat != nil && chat.Subchat.Parent == other.ID:
		return agent.RelationParent
	}
	return ""
}

// chatInfo is other as chat's chats tool lists it.
func (ui *desktopUI) chatInfo(chat, other *conversation) agent.ChatInfo {
	waiting := other.run != nil && (other.run.approvalID != "" || other.run.questionID != "")
	info := agent.ChatInfo{ID: other.ID, Title: agent.SubchatTitle(other.Title), State: agent.ChatState(other.run != nil, waiting),
		Relation: chatRelation(chat, other)}
	if !other.Config.InScratch() {
		info.Project = filepath.Base(ui.projectOf(other.Config.Workspace))
	}
	if machine, ok := ui.machineSettings(other.Config.MachineID); ok && other.Config.MachineID != "" {
		info.Machine = machine.Name
	}
	return info
}

// findChat is the chat id that chat's tool names ("parent": its parent),
// or why it cannot be reached.
func (ui *desktopUI) findChat(chat *conversation, id string) (*conversation, error) {
	if id == "parent" {
		if parent := ui.parentOf(chat); parent != nil {
			return parent, nil
		}
		if chat.Subchat != nil {
			return nil, fmt.Errorf("This chat's parent conversation was deleted")
		}
		return nil, fmt.Errorf("This chat has no parent conversation")
	}
	if ui.chatByID(chat.ID) != chat {
		return nil, fmt.Errorf("This conversation was deleted")
	}
	if id == chat.ID {
		return nil, fmt.Errorf("That is this chat: give another chat's id")
	}
	if other := ui.chatByID(id); other != nil {
		return other, nil
	}
	if remote := ui.server; remote != nil {
		for _, other := range remote.chats {
			if other.ID == id {
				return nil, fmt.Errorf("Chat %s “%s” is on the 1loop server %s, which chats on this computer cannot reach", id, other.Title, remote.name)
			}
		}
	}
	return nil, fmt.Errorf("No chat %s on this computer: it was deleted, or it is on another computer or 1loop server, which chats here cannot reach. List the chats you can message with action list", id)
}

// chatAccess is what chat's chats tool reaches: this computer's other
// chats, those working on SSH machines included.
func (ui *desktopUI) chatAccess(chat *conversation) *agent.ChatAccess {
	return &agent.ChatAccess{
		Find: func(ctx context.Context, id string) (info agent.ChatInfo, err error) {
			err = fmt.Errorf("1loop is closing")
			ui.onUI(ctx, func() {
				var other *conversation
				if other, err = ui.findChat(chat, id); err == nil {
					info = ui.chatInfo(chat, other)
				}
			})
			return info, err
		},
		List: func(ctx context.Context) (list []agent.ChatInfo) {
			ui.onUI(ctx, func() {
				for _, other := range ui.chats {
					if other != chat {
						list = append(list, ui.chatInfo(chat, other))
					}
				}
			})
			return list
		},
		Send: func(ctx context.Context, id, text string) (err error) {
			err = fmt.Errorf("1loop is closing")
			ui.onUI(ctx, func() { err = ui.sendToChat(chat, id, text) })
			return err
		},
	}
}

// sendToChat delivers text from chat's agent to the chat id.
func (ui *desktopUI) sendToChat(chat *conversation, id, text string) error {
	ui = ui.viewOf(chat) // the window showing it (windows.go)
	other, err := ui.findChat(chat, id)
	if err != nil {
		return err
	}
	hops := chat.hops + 1
	if hops > agent.MaxChatHops {
		return agent.TooManyHops(hops)
	}
	sender := agent.ChatSender{ID: chat.ID, Title: agent.SubchatTitle(chat.Title), Hops: hops}
	ui.wake(other, agent.ChatMessage(sender, chatRelation(other, chat), text))
	if other == ui.current && other.run != nil { // it waits in the queue under the composer
		ui.renderQueue()
	}
	return nil
}

// turnEnded is the end of a chat's last turn in a row: a subchat notes how
// it ended, and the chats that caused it hear it.
func (ui *desktopUI) turnEnded(chat *conversation, ended string) {
	if chat.Subchat != nil {
		chat.Subchat.Ended = ended
		if parent := ui.parentOf(chat); parent != nil {
			ui.panel.todoChanged(parent)
			ui.remote.metaChanged(parent)
		}
	}
	reportTo := chat.ReportTo
	chat.ReportTo = nil
	for _, id := range reportTo {
		target := ui.chatByID(id)
		if target == nil {
			continue // deleted meanwhile
		}
		info := agent.SubchatInfo{ID: chat.ID, Title: agent.SubchatTitle(chat.Title), State: agent.SubchatState(false, false, ended),
			Folder: chat.Config.Workspace, Last: agent.LastAnswer(chat.Items)}
		if chat.Subchat != nil {
			info = ui.subchatInfo(chat)
		}
		sender := agent.ChatSender{ID: chat.ID, Title: info.Title, Hops: chat.hops + 1}
		ui.wake(target, agent.TurnReport(sender, chatRelation(target, chat), info))
	}
}

// appendChatMessage draws another chat's message in chat's transcript: a
// card under its sender's name, which opens the sender, apart from the
// user's own bubbles (it has no Edit).
func (ui *desktopUI) appendChatMessage(chat *conversation, item agent.ChatItem) {
	sender := item.From
	title := []rune(sender.Title)
	if len(title) > 40 {
		title = append(title[:40], '…')
	}
	from := newRow("From the chat “"+string(title)+"”", iconSubchat, styleChip, func() { ui.openSubchat(chat, sender.ID) })
	text := newFlowText(plainSpans(item.Text), theme.TextSize(), colText)
	text.tail, text.tight = "", true
	pad := theme.InnerPadding()
	card := surface(container.NewVBox(container.NewHBox(from, layout.NewSpacer()),
		padded(0, pad, pad, pad, container.NewStack(text, newSelectLayer(ui, text)))), colCode, colBorderSoft, 14, 2)
	copyRow := container.NewHBox(layout.NewSpacer(), copyChip(func() string { return item.Text }))
	ui.place(container.New(layout.NewCustomPaddedVBoxLayout(2), card, copyRow))
}
