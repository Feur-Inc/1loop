// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2/container"

	"1loop/internal/agent"
)

// While the agent works, Enter queues the message for the next turn and
// Steer (Ctrl+Enter) gives it to the agent at its next step (#433): Enter
// steered too, so nothing waited for the task to end.
func TestEnterQueuesAndSteerSteers(t *testing.T) {
	ui := sidebarUI(t)
	ui.prompt = newChatEntry()
	ui.queueList, ui.queueTray, ui.queueTitle = container.NewVBox(), container.NewVBox(), newFitLabel("", 12.5, colMuted)
	ui.attachRow, ui.attachTray = container.NewHBox(), container.NewVBox()
	chat := &conversation{ID: "busy", Config: agent.Config{Workspace: "/home/u/app"}, run: &chatRun{}}
	ui.current = chat
	ui.loadingChat = &conversationLoad{} // no column to lay out in this window

	ui.prompt.SetText("after the task")
	ui.submit()
	ui.prompt.SetText("right now")
	ui.steerNow()

	if len(chat.queue) != 1 || chat.queue[0].Content != "after the task" {
		t.Errorf("queued: %+v", chat.queue)
	}
	if len(chat.steer) != 1 || chat.steer[0].Content != "right now" {
		t.Errorf("steered: %+v", chat.steer)
	}
}
