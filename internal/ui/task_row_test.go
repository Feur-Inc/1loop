// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"1loop/internal/agent"
)

// A click on a task list update in the conversation opens the side panel's
// Tasks tab, not the call's arguments; any other call still opens its own
// tab (#612).
func TestTaskRowOpensTasks(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	chat := window.chatByID("root")
	chat.Items = []agent.ChatItem{
		{Kind: "user", Text: "Plan it"},
		{Kind: "tool", Tool: "update_todo", Args: `{"todos":[{"content":"Read the code","status":"in_progress"}]}`, Text: "ok"},
		{Kind: "tool", Tool: "read_file", Args: `{"path":"main.go"}`, Text: "package main"},
	}
	window.openLocalChat(chat)
	for index, want := range map[int]string{1: "todo", 2: "tool"} {
		row, ok := window.views[index].(*toolRow)
		if !ok {
			t.Fatalf("item %d has no tool row: %T", index, window.views[index])
		}
		row.Tapped(nil)
		panel := window.panel.of(chat)
		if panel.active < 0 || panel.active >= len(panel.tabs) {
			t.Fatalf("item %d: no tab shown", index)
		}
		if got := panel.tabs[panel.active].kind; got != want {
			t.Errorf("item %d (%s) opened a %q tab, want %q", index, chat.Items[index].Tool, got, want)
		}
	}
	for _, name := range []string{"TodoWrite", "update_todo", "mcp__1loop_todo__update_todo"} {
		if !agent.TodoTool(name) {
			t.Errorf("%s is not a task list update", name)
		}
	}
	if agent.TodoTool("Task") {
		t.Error("Claude Code's Task (an agent) is not a task list update")
	}
}
