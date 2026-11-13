// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"encoding/json"
	"testing"

	"fyne.io/fyne/v2"
)

// splitWindow is a window split side by side, its own pane left and another
// right, without their widgets.
func splitWindow(t *testing.T) (window, right *desktopUI) {
	t.Helper()
	window, _ = twoWindows(t)
	window.windows = window.windows[:1]
	right = &desktopUI{desktopApp: window.desktopApp, owner: window}
	window.tree = &splitNode{pane: window}
	window.tree.splitWith(right, false, 0.5)
	return window, right
}

// What a run does to a chat goes to the pane showing it: a failure the user
// watched in the other pane of the window is not reported as unseen.
func TestRunFailedSeenInTheOtherPane(t *testing.T) {
	window, right := splitWindow(t)
	chat := &conversation{ID: "c", Title: "Shown in the right pane"}
	window.chats = []*conversation{chat}
	right.current = chat
	window.inForeground = true
	window.runFailed(chat) // the run started in the left pane
	if chat.failed {
		t.Fatal("a failure on screen in the other pane was marked unseen")
	}
	if window.viewOf(chat) != right {
		t.Fatal("the chat's updates do not go to the pane showing it")
	}
}

// The streaming turn follows its chat from one pane to another, and back to
// the window's own pane once its pane closed.
func TestTurnFollowsItsChatToAnotherPane(t *testing.T) {
	window, right := splitWindow(t)
	chat := &conversation{ID: "c"}
	window.current = chat
	turn := window.newTurn(chat, chat.Config)
	window.current, right.current = nil, chat
	if turn.view() != right {
		t.Fatal("the turn does not follow its chat to the other pane")
	}
	window.tree.find(right).remove()
	window.tree, right.current = nil, nil
	if turn.view() != window || right.viewOf(chat) != window {
		t.Fatal("a closed pane is still drawn in")
	}
}

// The window's panes are listed left to right, and the one with the focus
// is the window's own until another takes it.
func TestPanesAndFocus(t *testing.T) {
	window, right := splitWindow(t)
	if panes := right.panesOf(); len(panes) != 2 || panes[0] != window || panes[1] != right {
		t.Fatalf("panes = %v", panes)
	}
	if window.focused() != window || right.windowOf() != window {
		t.Fatal("the window's own pane does not start with the focus")
	}
	window.focus = right
	if window.focused() != right || window.activeView() != right {
		t.Fatal("the pane given the focus does not have it")
	}
	if len(window.allPanes()) != 2 {
		t.Fatal("allPanes misses a pane")
	}
	window.tree.find(right).remove()
	if window.tree.pane != window {
		t.Fatal("closing the right pane did not leave the window's own")
	}
}

// A three-pane layout is saved and read back as it was: the window's own
// pane marked, the others with their chats, the ratios kept.
func TestSplitSaved(t *testing.T) {
	window, right := splitWindow(t)
	below := &desktopUI{desktopApp: window.desktopApp, owner: window}
	window.tree.find(right).splitWith(below, true, 0.3)
	right.current = &conversation{ID: "r"}
	below.current = &conversation{ID: "b"}
	window.current = &conversation{ID: "w"}
	data, err := json.Marshal(window.savedLayout())
	if err != nil {
		t.Fatal(err)
	}
	var saved savedSplit
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.First.Window || saved.First.Chat != "" || saved.Vertical || saved.Ratio != 0.5 {
		t.Fatalf("left = %s", data)
	}
	if second := saved.Second; !second.Vertical || second.Ratio != 0.3 || second.First.Chat != "r" || second.Second.Chat != "b" {
		t.Fatalf("right = %s", data)
	}
	if !saved.hasWindow() || (&savedSplit{First: &savedSplit{Chat: "x"}, Second: &savedSplit{Chat: "y"}}).hasWindow() {
		t.Fatal("hasWindow is wrong")
	}
}

// Each half keeps room enough while the window has it.
func TestSplitCut(t *testing.T) {
	for _, c := range []struct{ ratio, total, want float32 }{
		{0.5, 1006, 500},
		{0.1, 1006, minPaneSize},
		{0.9, 1006, 1000 - minPaneSize},
		{0.2, 306, 60}, // too small for both: the ratio alone
		{0, 106, 50},
	} {
		if got := splitCut(c.ratio, c.total); got != c.want {
			t.Errorf("splitCut(%v, %v) = %v, want %v", c.ratio, c.total, got, c.want)
		}
	}
}

// Focus moves to the pane that lies in the arrow's direction.
func TestPaneToward(t *testing.T) {
	at, size := fyne.NewPos(0, 0), fyne.NewSize(500, 800)
	right, below := fyne.NewPos(506, 0), fyne.NewPos(0, 806)
	if ok, _ := paneToward(at, size, right, size, 1, 0); !ok {
		t.Error("the pane on the right is not to the right")
	}
	if ok, _ := paneToward(at, size, right, size, -1, 0); ok {
		t.Error("the pane on the right is to the left")
	}
	if ok, _ := paneToward(at, size, below, size, 0, 1); !ok {
		t.Error("the pane below is not below")
	}
	if ok, _ := paneToward(at, size, below, size, 1, 0); ok {
		t.Error("the pane below is to the right")
	}
}
