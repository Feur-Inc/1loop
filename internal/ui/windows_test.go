// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"path/filepath"
	"testing"
)

// twoWindows is the main window and another, sharing one app, without
// their widgets.
func twoWindows(t *testing.T) (main, other *desktopUI) {
	t.Helper()
	app := &desktopApp{ctx: context.Background(), statePath: filepath.Join(t.TempDir(), "state.json"),
		foldedProjects: map[string]bool{}, shortcutKeys: map[string]string{}}
	main, other = &desktopUI{desktopApp: app}, &desktopUI{desktopApp: app}
	app.windows = []*desktopUI{main, other}
	return main, other
}

// What a run does to a chat goes to the window showing it, whichever window
// started the run: a failure the user watched in the other window is not
// reported as unseen (#285).
func TestRunFailedSeenInTheOtherWindow(t *testing.T) {
	main, other := twoWindows(t)
	chat := &conversation{ID: "c", Title: "Shown in the other window"}
	main.chats = []*conversation{chat}
	other.current = chat
	main.inForeground = true
	main.runFailed(chat) // the run started in the main window
	if chat.failed {
		t.Fatal("a failure on screen in the other window was marked unseen")
	}
	other.current = nil
	main.runFailed(chat)
	if !chat.failed {
		t.Fatal("a failure on screen nowhere was not marked unseen")
	}
}

// The streaming turn draws in the window its chat is in now, even after the
// chat moved there from the window the run started in.
func TestTurnFollowsItsChatToAnotherWindow(t *testing.T) {
	main, other := twoWindows(t)
	chat := &conversation{ID: "c"}
	main.current = chat
	turn := main.newTurn(chat, chat.Config)
	if turn.view() != main {
		t.Fatal("the turn does not draw in the window it started in")
	}
	main.current, other.current = nil, chat
	if turn.view() != other {
		t.Fatal("the turn does not follow its chat to the other window")
	}
	main.windows, other.current = main.windows[:1], nil // the other window closed (closeAlone)
	if turn.view() != main || other.viewOf(chat) != main {
		t.Fatal("a closed window is still drawn in")
	}
}

// A hidden main window does not count as showing its chat: a notification
// is due.
func TestShowingIgnoresHiddenWindow(t *testing.T) {
	main, other := twoWindows(t)
	chat := &conversation{ID: "c"}
	main.current = chat
	if !main.showing(chat) {
		t.Fatal("the main window's chat is not on screen")
	}
	main.hiddenAlone = true
	if main.showing(chat) {
		t.Fatal("the hidden main window's chat counts as on screen")
	}
	// Closing the other window now closes the last one on screen: it
	// follows the tray setting instead of only going away.
	if other.otherWindowShown() || !main.otherWindowShown() {
		t.Fatal("a hidden window counts as on screen")
	}
}

// The other windows are saved with their chats and open again on them; a
// window whose chat was deleted, or that repeats a chat, does not.
func TestWindowsSavedAndRestored(t *testing.T) {
	main, other := twoWindows(t)
	kept := &conversation{ID: "kept", Title: "Kept"}
	main.chats = []*conversation{kept}
	other.current = kept
	main.persist(true)
	if main.saveError != "" {
		t.Fatal(main.saveError)
	}
	state, err := readSavedState(main.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Windows) != 1 || state.Windows[0].Chat != "kept" {
		t.Fatalf("saved windows %+v", state.Windows)
	}
	saved := []savedWindow{{Chat: "kept"}, {Chat: "gone"}, {Chat: "kept"}, {}}
	got := restorableWindows(saved, main.chats)
	if len(got) != 2 || got[0].Chat != "kept" || got[1].Chat != "" {
		t.Fatalf("restored %+v", got)
	}
}
