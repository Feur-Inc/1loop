// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
)

// shownText is every text drawn under object, joined.
func shownText(object fyne.CanvasObject) string {
	var parts []string
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch value := object.(type) {
		case *canvas.Text:
			parts = append(parts, value.Text)
		case *fyne.Container:
			for _, child := range value.Objects {
				walk(child)
			}
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(value).Objects() {
				walk(child)
			}
		}
	}
	walk(object)
	return strings.Join(parts, " ")
}

// The Tasks panel's context is drawn again when the panel opens or its tab
// is chosen: a count that came while it was folded, or while another tab
// was shown, used to stay off screen until the next one.
func TestTodoContextFollowsWhatChangedOffScreen(t *testing.T) {
	test.NewTempApp(t)
	ui, _ := twoWindows(t)
	ui.windows = ui.windows[:1]
	ui.window = test.NewWindow(nil)
	ui.window.SetContent(ui.build())
	ui.window.Resize(fyne.NewSize(1400, 820))
	window := 1000000
	count := func(used int) *agent.ContextUsage {
		return &agent.ContextUsage{Used: used, Window: window, Categories: []agent.ContextCategory{
			{ID: "messages", Name: "Messages", Tokens: used, Kind: "used"},
			{ID: "free_space", Name: "Free space", Tokens: window - used, Kind: "free"},
		}}
	}
	chat := &conversation{ID: "c", Title: "Chat", Config: agent.Config{Provider: "claudecode", Model: "opus"},
		ContextUsage: count(70000), Items: []agent.ChatItem{{Kind: "tool", Tool: "shell", Args: `{"command":"ls"}`, Text: "out"}}}
	ui.chats = []*conversation{chat}
	ui.openLocalChat(chat)
	ui.panel.openTodo()
	todo := ui.panel.active
	footer := func() string { return shownText(ui.panel.tabs[todo].todoContext) }
	if !strings.Contains(footer(), "7% used") {
		t.Fatalf("the panel does not show the first count: %q", footer())
	}

	ui.panel.fold()
	chat.ContextUsage = count(400000)
	ui.setContextUsage(400000, window)
	ui.panel.unfold()
	if !strings.Contains(footer(), "40% used") {
		t.Errorf("after the panel opened again: %q, want 40%% used", footer())
	}

	ui.panel.open(chat, 0) // a tool's tab
	chat.ContextUsage = count(600000)
	ui.setContextUsage(600000, window)
	ui.panel.choose(todo)
	if !strings.Contains(footer(), "60% used") {
		t.Errorf("after the Tasks tab was chosen again: %q, want 60%% used", footer())
	}
}
