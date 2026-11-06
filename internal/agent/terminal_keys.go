// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"1loop/internal/keys"
)

// TerminalKey is what a terminal receives for a key name (see keys.Parse):
// the bytes an xterm sends. Meta is sent as Alt, the terminal's meta key.
func TerminalKey(name string) (string, error) {
	chord, err := keys.Parse(name)
	if err != nil {
		return "", err
	}
	alt := chord.Alt || chord.Meta
	prefix := ""
	if alt {
		prefix = "\x1b"
	}
	if utf8.RuneCountInString(chord.Key) == 1 || chord.Key == "Space" {
		key := chord.Key
		if key == "Space" {
			key = " "
		}
		if chord.Ctrl {
			code, ok := terminalControl(key)
			if !ok {
				return "", fmt.Errorf("A terminal has no %s: Ctrl goes with a letter or one of @ [ \\ ] ^ _ ? Space", name)
			}
			key = code
		} else if chord.Shift {
			key = strings.ToUpper(key)
		}
		return prefix + key, nil
	}
	// xterm's modifier parameter: 1 + Shift 1, Alt 2, Ctrl 4.
	modifier := 1
	if chord.Shift {
		modifier++
	}
	if alt {
		modifier += 2
	}
	if chord.Ctrl {
		modifier += 4
	}
	switch chord.Key {
	case "Enter", "Tab", "Escape", "Backspace":
		plain := map[string]string{"Enter": "\r", "Tab": "\t", "Escape": "\x1b", "Backspace": "\x7f"}[chord.Key]
		switch {
		case chord.Key == "Tab" && chord.Shift && !chord.Ctrl:
			return prefix + "\x1b[Z", nil
		case chord.Key == "Backspace" && chord.Ctrl && !chord.Shift:
			return prefix + "\x08", nil
		case chord.Ctrl || chord.Shift:
			return "", fmt.Errorf("A terminal can't tell %s from %s: send it without Ctrl or Shift", name, chord.Key)
		}
		return prefix + plain, nil
	}
	if letter, ok := map[string]string{"ArrowUp": "A", "ArrowDown": "B", "ArrowRight": "C", "ArrowLeft": "D", "Home": "H", "End": "F", "F1": "P", "F2": "Q", "F3": "R", "F4": "S"}[chord.Key]; ok {
		if modifier > 1 {
			return "\x1b[1;" + strconv.Itoa(modifier) + letter, nil
		}
		if strings.HasPrefix(chord.Key, "F") {
			return "\x1bO" + letter, nil
		}
		return "\x1b[" + letter, nil
	}
	if number, ok := map[string]string{"Insert": "2", "Delete": "3", "PageUp": "5", "PageDown": "6", "F5": "15", "F6": "17", "F7": "18", "F8": "19", "F9": "20", "F10": "21", "F11": "23", "F12": "24"}[chord.Key]; ok {
		if modifier > 1 {
			return "\x1b[" + number + ";" + strconv.Itoa(modifier) + "~", nil
		}
		return "\x1b[" + number + "~", nil
	}
	return "", unsupportedKey(name)
}

// terminalControl is the control character Ctrl gives with a key.
func terminalControl(key string) (string, bool) {
	lower := strings.ToLower(key)
	switch {
	case len(lower) == 1 && lower[0] >= 'a' && lower[0] <= 'z':
		return string(rune(lower[0] - 'a' + 1)), true
	case key == "@" || key == " " || key == "2":
		return "\x00", true
	case key == "[" || key == "3":
		return "\x1b", true
	case key == "\\" || key == "4":
		return "\x1c", true
	case key == "]" || key == "5":
		return "\x1d", true
	case key == "^" || key == "6":
		return "\x1e", true
	case key == "_" || key == "-" || key == "7":
		return "\x1f", true
	case key == "?" || key == "8":
		return "\x7f", true
	}
	return "", false
}
