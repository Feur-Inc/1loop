// SPDX-License-Identifier: MPL-2.0

// Package keys reads the key names the model gives the terminal and browser
// tools, the same for both: "Enter", "Ctrl+C", "Cmd+A", "Ctrl++".
package keys

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Chord is a key with the modifiers held for it. Key is a single character
// or one of the names of Names, in that spelling.
type Chord struct {
	Ctrl, Shift, Alt, Meta bool
	Key                    string
}

// Names are the named keys, as Parse gives them.
var Names = []string{"Enter", "Tab", "Escape", "Backspace", "Delete", "Insert", "Home", "End", "PageUp", "PageDown", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Space", "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12"}

// Help is what an error says about the names a call can use.
const Help = "a single character or Enter, Tab, Escape, Backspace, Delete, Insert, Home, End, PageUp, PageDown, ArrowUp/Down/Left/Right, Space, Plus, F1-F12, after Ctrl, Shift, Alt (Option) or Meta (Cmd, Super): e.g. Ctrl+C, Meta+Shift+T, Ctrl++"

var aliases = map[string]string{
	"return": "Enter", "esc": "Escape", "del": "Delete", "ins": "Insert",
	"pgup": "PageUp", "pgdn": "PageDown", "prior": "PageUp", "next": "PageDown",
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
	"plus": "+", "spacebar": "Space",
}

// Parse reads a key name with its modifiers. An unknown name is an error,
// never text to type: "Escap" or "Cmd+A" typed into a page as letters was
// reported as a key press.
func Parse(text string) (Chord, error) {
	var chord Chord
	text = strings.TrimSpace(text)
	parts := strings.Split(text, "+")
	if strings.HasSuffix(text, "++") || text == "+" {
		// The + key itself: "Ctrl++", or "+" alone.
		parts = append(strings.Split(strings.TrimSuffix(text, "++"), "+"), "+")
		if text == "+" {
			parts = []string{"+"}
		}
	}
	name := strings.TrimSpace(parts[len(parts)-1])
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "ctrl", "control":
			chord.Ctrl = true
		case "shift":
			chord.Shift = true
		case "alt", "option", "opt":
			chord.Alt = true
		case "meta", "cmd", "command", "super", "win", "windows":
			chord.Meta = true
		default:
			return Chord{}, fmt.Errorf("Unknown modifier %q in %q: use %s", part, text, Help)
		}
	}
	if utf8.RuneCountInString(name) == 1 {
		chord.Key = name
		return chord, nil
	}
	lower := strings.ToLower(name)
	if alias, ok := aliases[lower]; ok {
		chord.Key = alias
		return chord, nil
	}
	for _, known := range Names {
		if strings.ToLower(known) == lower {
			chord.Key = known
			return chord, nil
		}
	}
	if name == "" {
		return Chord{}, fmt.Errorf("No key in %q: use %s", text, Help)
	}
	return Chord{}, fmt.Errorf("Unknown key %q: use %s", name, Help)
}
