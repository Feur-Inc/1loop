// SPDX-License-Identifier: MPL-2.0

package settings

// The keyboard shortcuts of the desktop: one setting each, on the General
// section's "Keyboard shortcuts" page, so the page, the search and the
// model's settings tool all know them. A value is a key with its modifiers
// in internal/keys' names ("Ctrl+Shift+F"), or "off" for none.

import (
	"fmt"
	"strings"
	"unicode"

	"1loop/internal/keys"
)

// ShortcutsPage is the desktop page that lists the shortcuts.
const ShortcutsPage = "Keyboard shortcuts"

// ShortcutOff is the value of a shortcut with no key.
const ShortcutOff = "off"

// Keys of the shortcuts.
const (
	ShortcutFind         = "shortcuts.find"
	ShortcutSearch       = "shortcuts.search"
	ShortcutNewChat      = "shortcuts.new_chat"
	ShortcutNewProject   = "shortcuts.new_project"
	ShortcutNewWindow    = "shortcuts.new_window"
	ShortcutCloseWindow  = "shortcuts.close_window"
	ShortcutSplitRight   = "shortcuts.split_right"
	ShortcutSplitDown    = "shortcuts.split_down"
	ShortcutPaneLeft     = "shortcuts.pane_left"
	ShortcutPaneRight    = "shortcuts.pane_right"
	ShortcutPaneUp       = "shortcuts.pane_up"
	ShortcutPaneDown     = "shortcuts.pane_down"
	ShortcutSettings     = "shortcuts.settings"
	ShortcutSidebar      = "shortcuts.sidebar"
	ShortcutPrompt       = "shortcuts.prompt"
	ShortcutModel        = "shortcuts.model"
	ShortcutStop         = "shortcuts.stop"
	ShortcutCopyAnswer   = "shortcuts.copy_answer"
	ShortcutPreviousChat = "shortcuts.previous_chat"
	ShortcutNextChat     = "shortcuts.next_chat"
	ShortcutRestart      = "shortcuts.restart"
	ShortcutQuit         = "shortcuts.quit"
)

func shortcut(key, title, chord, description string, words ...string) Setting {
	return Setting{Key: key, Section: General, Page: ShortcutsPage, Title: title, Description: description, Where: OnDesktop, Kind: Shortcut, Default: chord,
		Keywords: append([]string{"shortcut", "keyboard", "hotkey", "keybinding", "keybind", "key"}, words...)}
}

var shortcuts = []Setting{
	shortcut(ShortcutFind, "Find in the chat", "Ctrl+F", "Finds words in the conversation on screen, from one match to the next.", "find", "search", "chat", "conversation", "text"),
	shortcut(ShortcutSearch, "Search chats and settings", "Ctrl+K", "Opens the search of the sidebar, over every chat and setting.", "search", "find", "chats", "command palette", "quick open"),
	shortcut(ShortcutNewChat, "New chat", "Ctrl+N", "Starts a new chat in the project open.", "new", "chat", "conversation"),
	shortcut(ShortcutNewWindow, "New window", "Ctrl+Shift+N", "Opens another 1loop window, on a new chat in the project open.", "new", "window", "chat", "multiple", "side by side"),
	shortcut(ShortcutCloseWindow, "Close the pane or window", "Ctrl+Shift+W", "Closes the pane with the focus when the window is split, else the window. The last window follows the tray setting.", "close", "window", "pane", "split"),
	shortcut(ShortcutSplitRight, "Split side by side", "Ctrl+Shift+E", "Opens a new chat in a pane beside the one with the focus.", "split", "pane", "side by side", "right", "view"),
	shortcut(ShortcutSplitDown, "Split top and bottom", "Ctrl+Shift+O", "Opens a new chat in a pane under the one with the focus.", "split", "pane", "top", "bottom", "below", "view"),
	shortcut(ShortcutPaneLeft, "Pane on the left", "Shift+Alt+ArrowLeft", "Moves the focus to the pane on the left of a split window.", "pane", "split", "focus", "left"),
	shortcut(ShortcutPaneRight, "Pane on the right", "Shift+Alt+ArrowRight", "Moves the focus to the pane on the right of a split window.", "pane", "split", "focus", "right"),
	shortcut(ShortcutPaneUp, "Pane above", "Shift+Alt+ArrowUp", "Moves the focus to the pane above in a split window.", "pane", "split", "focus", "up"),
	shortcut(ShortcutPaneDown, "Pane below", "Shift+Alt+ArrowDown", "Moves the focus to the pane below in a split window.", "pane", "split", "focus", "down"),
	shortcut(ShortcutNewProject, "Add a project", "Ctrl+P", "Opens a folder as a new project.", "new", "project", "folder", "workspace", "open"),
	shortcut(ShortcutSettings, "Open Settings", "Ctrl+,", "Opens 1loop's settings.", "settings", "preferences", "options"),
	shortcut(ShortcutSidebar, "Show or hide the sidebar", "Ctrl+B", "Folds the sidebar away, or brings it back.", "sidebar", "toggle", "hide", "collapse"),
	shortcut(ShortcutPrompt, "Go to the message box", "Ctrl+L", "Puts the cursor in the message box, ready to type.", "focus", "prompt", "composer", "input", "message"),
	shortcut(ShortcutModel, "Choose the model", "Ctrl+M", "Opens the model menu of the composer.", "model", "switch", "picker", "menu"),
	shortcut(ShortcutStop, "Stop the agent", "Ctrl+.", "Stops the turn running in the chat on screen.", "stop", "cancel", "interrupt", "abort"),
	shortcut(ShortcutCopyAnswer, "Copy the last answer", "Ctrl+Shift+C", "Copies the newest answer of the chat on screen.", "copy", "answer", "response", "clipboard"),
	shortcut(ShortcutPreviousChat, "Previous chat", "Alt+ArrowUp", "Opens the chat above in the sidebar.", "previous", "chat", "up", "navigate", "switch"),
	shortcut(ShortcutNextChat, "Next chat", "Alt+ArrowDown", "Opens the chat below in the sidebar.", "next", "chat", "down", "navigate", "switch"),
	shortcut(ShortcutRestart, "Restart 1loop", "Ctrl+Shift+R", "Saves everything, then starts 1loop again.", "restart", "reload", "relaunch"),
	shortcut(ShortcutQuit, "Quit 1loop", "Ctrl+Q", "Quits 1loop, even when closing the window keeps it in the tray.", "quit", "exit", "close", "tray"),
}

// Shortcuts lists the shortcuts, in the order the page shows them.
func Shortcuts() []Setting { return append([]Setting(nil), shortcuts...) }

// reserved are the keys the text boxes keep for themselves.
var reserved = map[string]string{
	"Ctrl+A": "Select all", "Ctrl+C": "Copy", "Ctrl+Insert": "Copy", "Ctrl+V": "Paste", "Ctrl+X": "Cut",
	"Ctrl+Z": "Undo", "Ctrl+Y": "Redo", "Ctrl+Shift+Z": "Redo", "Ctrl+Enter": "Steer",
}

// ShortcutValue reads a shortcut as typed ("ctrl+shift+f", "Off") and
// gives it as it is stored ("Ctrl+Shift+F", "off"). It needs Ctrl, Alt or
// Meta: a key alone, or with Shift alone, is typing.
func ShortcutValue(text string) (string, error) {
	text = strings.TrimSpace(text)
	switch strings.ToLower(text) {
	case "", "off", "none", "disabled", "unassigned":
		return ShortcutOff, nil
	}
	chord, err := keys.Parse(text)
	if err != nil {
		return "", err
	}
	if !chord.Ctrl && !chord.Alt && !chord.Meta {
		return "", fmt.Errorf("%s has no Ctrl, Alt or Meta: a shortcut needs one of them", text)
	}
	if chord.Key == "+" {
		return "", fmt.Errorf("the + key cannot be a shortcut: its key gives = without Shift")
	}
	value := FormatChord(chord)
	if what, taken := reserved[value]; taken {
		return "", fmt.Errorf("%s is the text boxes' %s", value, what)
	}
	return value, nil
}

// FormatChord writes a key with its modifiers as ShortcutValue stores it.
func FormatChord(chord keys.Chord) string {
	var parts []string
	for _, modifier := range []struct {
		on   bool
		name string
	}{{chord.Ctrl, "Ctrl"}, {chord.Shift, "Shift"}, {chord.Alt, "Alt"}, {chord.Meta, "Meta"}} {
		if modifier.on {
			parts = append(parts, modifier.name)
		}
	}
	key := chord.Key
	if r := []rune(key); len(r) == 1 {
		key = string(unicode.ToUpper(r[0]))
	}
	return strings.Join(append(parts, key), "+")
}
