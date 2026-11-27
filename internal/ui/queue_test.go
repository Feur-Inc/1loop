// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"1loop/internal/remoteapi"
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

// A server chat shows its queued messages in the same queue tray as a local
// chat (not as read-only bubbles in pendingBox) and only shows steer messages
// in pendingBox, with the same queue actions.
func TestServerChatUsesSameQueueTrayAsLocal(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	window.server = &remoteDesktop{
		parent:  window,
		current: "srv-chat",
		meta: remoteapi.Meta{
			Chat:    "srv-chat",
			Running: true,
			Queue:   []string{"first queued", "second queued"},
			Steer:   []string{"steer now"},
		},
	}
	window.renderQueue()

	if !window.queueTray.Visible() {
		t.Fatal("server chat queued messages are not shown in queueTray")
	}
	if got := len(window.queueList.Objects); got != 2 {
		t.Fatalf("queueList has %d rows, want 2", got)
	}
	if got := len(window.pendingBox.Objects); got != 1 {
		t.Fatalf("pendingBox has %d items, want 1 (only steer, not queue)", got)
	}

	window.moveQueued(1, -1)
	if window.server.meta.Queue[0] != "second queued" || window.server.meta.Queue[1] != "first queued" {
		t.Fatalf("after moveQueued: queue = %v", window.server.meta.Queue)
	}

	window.sendQueuedNow(1)
	if len(window.server.meta.Queue) != 1 || len(window.server.meta.Steer) != 2 || window.server.meta.Steer[1] != "first queued" {
		t.Fatalf("after sendQueuedNow: queue=%v steer=%v", window.server.meta.Queue, window.server.meta.Steer)
	}

	window.editQueued(false, 0)
	if window.prompt.Text != "second queued" {
		t.Fatalf("prompt = %q, want %q", window.prompt.Text, "second queued")
	}
	window.prompt.SetText("second updated")
	window.finishEdit(true)
	if window.server.meta.Queue[0] != "second updated" {
		t.Fatalf("after edit: queue = %v", window.server.meta.Queue)
	}

	window.clearQueue()
	if window.queueTray.Visible() || len(window.pendingBox.Objects) != 0 {
		t.Fatalf("after clearQueue: tray visible=%v pending=%d", window.queueTray.Visible(), len(window.pendingBox.Objects))
	}
}
