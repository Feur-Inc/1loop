// SPDX-License-Identifier: MPL-2.0

package server

// Subchat concurrency and memory queue (#649): before starting a subchat, the
// server checks the configured limit and the machine's available memory / PSI.
// When at capacity or short on memory, the subchat waits in FIFO order and
// starts automatically once another ends or memory returns.

import (
	"slices"
	"time"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

const defaultSubchatQueuePoll = 5 * time.Second

func (server *headlessServer) memoryStatus() platform.MemoryStatus {
	if server.readMemory != nil {
		return server.readMemory()
	}
	return platform.ReadMemory()
}

func (server *headlessServer) defaultMaxSubchats() int {
	return agent.DefaultMaxSubchats(server.memoryStatus().TotalBytes)
}

func (server *headlessServer) effectiveMaxSubchatsLocked() int {
	if server.maxSubchats > 0 {
		return server.maxSubchats
	}
	return server.defaultMaxSubchats()
}

// runningSubchatsLocked counts subchats currently executing a turn across the
// whole server (memory is machine-wide).
func (server *headlessServer) runningSubchatsLocked() int {
	n := 0
	for _, chat := range server.chats {
		if chat.Subchat != nil && chat.run != nil {
			n++
		}
	}
	return n
}

// startOrQueueSubchatLocked starts sub immediately when a slot and memory are
// available and nothing is ahead of it in the queue; otherwise it queues sub
// and returns (true, reason).
func (server *headlessServer) startOrQueueSubchatLocked(sub *serverChat, prompt agent.Message) (bool, string) {
	ok, reason := agent.SubchatMemoryCheck(server.memoryStatus(), server.runningSubchatsLocked(), server.effectiveMaxSubchatsLocked())
	if ok && len(server.subchatQueue) == 0 {
		server.runLocked(sub, prompt)
		return false, ""
	}
	if ok {
		reason = "Waiting behind earlier queued subchats."
	}
	msg := prompt
	sub.queuedSubchat = &msg
	sub.queuedReason = reason
	server.subchatQueue = append(server.subchatQueue, sub)
	server.ensureSubchatQueueTimerLocked()
	server.broadcastLocked("meta", server.metaLocked(sub))
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	return true, reason
}

func (server *headlessServer) ensureSubchatQueueTimerLocked() {
	if server.subchatQueueTimer != nil || server.closing || server.ctx.Err() != nil {
		return
	}
	interval := server.subchatQueuePoll
	if interval <= 0 {
		interval = defaultSubchatQueuePoll
	}
	server.subchatQueueTimer = time.AfterFunc(interval, func() {
		server.mu.Lock()
		defer server.mu.Unlock()
		server.subchatQueueTimer = nil
		server.drainSubchatQueueLocked()
	})
}

// drainSubchatQueueLocked starts queued subchats in FIFO order as long as
// slots and memory allow.
func (server *headlessServer) drainSubchatQueueLocked() {
	if server.closing || server.ctx.Err() != nil {
		if server.subchatQueueTimer != nil {
			server.subchatQueueTimer.Stop()
			server.subchatQueueTimer = nil
		}
		return
	}
	for len(server.subchatQueue) > 0 {
		sub := server.subchatQueue[0]
		if server.chatLocked(sub.ID) != sub || sub.queuedSubchat == nil || sub.run != nil {
			server.subchatQueue = server.subchatQueue[1:]
			continue
		}
		if ok, _ := agent.SubchatMemoryCheck(server.memoryStatus(), server.runningSubchatsLocked(), server.effectiveMaxSubchatsLocked()); !ok {
			server.ensureSubchatQueueTimerLocked()
			return
		}
		server.subchatQueue = server.subchatQueue[1:]
		msg := *sub.queuedSubchat
		sub.queuedSubchat = nil
		sub.queuedReason = ""
		server.runLocked(sub, msg)
	}
	if server.subchatQueueTimer != nil {
		server.subchatQueueTimer.Stop()
		server.subchatQueueTimer = nil
	}
}

// cancelQueuedSubchatLocked removes a queued subchat from the queue and marks
// it stopped, notifying its parent just like stopping a running subchat.
func (server *headlessServer) cancelQueuedSubchatLocked(sub *serverChat) {
	if sub.queuedSubchat == nil {
		return
	}
	sub.queuedSubchat = nil
	sub.queuedReason = ""
	server.subchatQueue = slices.DeleteFunc(server.subchatQueue, func(c *serverChat) bool { return c == sub })
	server.broadcastLocked("meta", server.metaLocked(sub))
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	server.turnEndedLocked(sub, agent.SubchatStopped)
	server.scheduleSaveLocked()
	server.drainSubchatQueueLocked()
}
