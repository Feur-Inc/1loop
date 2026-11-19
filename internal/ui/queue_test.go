// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"
)

// Enter while the agent works queues the message for the end of the task;
// Ctrl+Enter (Steer) joins the task now (#433).
func TestEnterQueuesWhileRunningCtrlEnterSteers(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	chat := window.chatByID("a1") // running
	window.openLocalChat(chat)
	window.prompt.SetText("after the task")
	window.submit()
	window.prompt.SetText("now")
	window.steerNow()
	if len(chat.queue) != 1 || chat.queue[0].Typed() != "after the task" {
		t.Fatalf("queue = %+v, want the Enter message", chat.queue)
	}
	if len(chat.steer) != 1 || chat.steer[0].Typed() != "now" {
		t.Fatalf("steer = %+v, want the Ctrl+Enter message", chat.steer)
	}
	if !window.queueTray.Visible() {
		t.Fatal("the queued message is not shown in the tray")
	}
}
