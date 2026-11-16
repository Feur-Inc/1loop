// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
)

// A split window has one side panel, beside all its panes: every pane shares
// it, it shows the chat of the pane with the focus, and its strip lists the
// tabs of the other panes' chats too.
func TestSplitWindowSharesOneSidePanel(t *testing.T) {
	test.NewTempApp(t)
	window, _ := twoWindows(t)
	window.windows = window.windows[:1]
	window.window = test.NewWindow(nil)
	window.window.SetContent(window.build())
	window.window.Resize(fyne.NewSize(1400, 820))
	left := &conversation{ID: "l", Title: "Left", Items: []agent.ChatItem{{Kind: "tool", Tool: "shell", Args: `{"command":"ls"}`}}}
	right := &conversation{ID: "r", Title: "Right", Items: []agent.ChatItem{{Kind: "tool", Tool: "shell", Args: `{"command":"pwd"}`}}}
	window.chats = []*conversation{left, right}
	window.openLocalChat(left)
	window.panel.open(left, 0)

	pane := window.splitPane(right, false)
	if pane == nil {
		t.Fatal("the window did not split")
	}
	if pane.panel != window.panel || pane.split != window.split {
		t.Fatal("the new pane has a side panel of its own")
	}
	if window.panel.owner != right || window.panel.ui != pane {
		t.Fatalf("the panel does not follow the pane with the focus: owner %v", window.panel.owner)
	}
	pane.panel.open(right, 0)
	if len(window.panel.tabs) != 1 || window.panel.tabs[0].chat != right {
		t.Fatalf("the panel shows %d tabs", len(window.panel.tabs))
	}
	// The left pane's tab is listed after its chat's title: a label and a
	// tab for each pane.
	if n := len(window.panel.strip.Objects); n != 4 {
		t.Fatalf("the strip has %d objects, want both panes' tabs with their titles", n)
	}

	window.activate()
	if window.panel.owner != left || len(window.panel.tabs) != 1 || window.panel.tabs[0].chat != left {
		t.Fatal("the panel did not come back to the left pane's chat")
	}
	if window.panel.target <= 0 {
		t.Fatal("the shared panel folded as the focus moved")
	}
	if tabs := right.panel.tabs; len(tabs) != 1 {
		t.Fatalf("the right chat kept %d tabs", len(tabs))
	}
}
