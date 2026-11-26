// SPDX-License-Identifier: MPL-2.0

package server

import (
	"testing"

	"1loop/internal/agent"
)

// The context of a Claude Code chat is the same everywhere, during a turn
// (after a compaction) and once it is over.
func TestClaudeContextFollowsTheTurn(t *testing.T) {
	server, root, _ := testServer(t)
	server.mu.Lock()
	chat := &serverChat{ID: "c", Config: agent.Config{Provider: "claudecode", Model: "sonnet", Workspace: root}}
	server.chats = append(server.chats, chat)
	run := &serverRun{index: -1, calls: map[string]int{}, states: map[int]string{}}
	chat.run = run
	server.mu.Unlock()
	check := func(when string, used int) {
		t.Helper()
		server.mu.Lock()
		meta := server.metaLocked(chat)
		server.mu.Unlock()
		if meta.Used != used || meta.ContextUsage == nil || meta.ContextUsage.Used != used {
			t.Errorf("%s: used %d, breakdown %+v", when, meta.Used, meta.ContextUsage)
		}
	}
	server.emit(chat, run, "usage", 955)
	server.emit(chat, run, "context_usage", agent.ContextUsage{Used: 955, Window: 1000, Categories: []agent.ContextCategory{{ID: "messages", Tokens: 955, Kind: "used"}}})
	check("counted", 955)
	server.emit(chat, run, "usage", 98)
	check("compacted during the turn", 98)
	server.mu.Lock()
	chat.run = nil
	server.mu.Unlock()
	check("the turn is over", 98)
}
