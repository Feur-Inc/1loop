// SPDX-License-Identifier: MPL-2.0

package agent

import "testing"

func TestTerminalKey(t *testing.T) {
	for name, want := range map[string]string{
		"Enter":           "\r",
		"Ctrl+C":          "\x03",
		"control+d":       "\x04",
		"Ctrl+[":          "\x1b",
		"Ctrl+Space":      "\x00",
		"Insert":          "\x1b[2~",
		"Shift+Tab":       "\x1b[Z",
		"Alt+x":           "\x1bx",
		"Option+b":        "\x1bb",
		"Meta+f":          "\x1bf",
		"Ctrl+ArrowLeft":  "\x1b[1;5D",
		"Shift+F3":        "\x1b[1;2R",
		"Ctrl+Delete":     "\x1b[3;5~",
		"F5":              "\x1b[15~",
		"ArrowUp":         "\x1b[A",
		"Plus":            "+",
		"Shift+a":         "A",
		"Alt+Backspace":   "\x1b\x7f",
		"Ctrl+Backspace":  "\x08",
		"é":               "é",
		"Alt+Ctrl+c":      "\x1b\x03",
		"Ctrl+Alt+Escape": "",
	} {
		got, err := TerminalKey(name)
		if want == "" {
			if err == nil {
				t.Errorf("TerminalKey(%q) = %q, want an error", name, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("TerminalKey(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"Escap", "Cmd+", "Ctrl+é", "Hyper+x"} {
		if got, err := TerminalKey(name); err == nil {
			t.Errorf("TerminalKey(%q) = %q, want an error", name, got)
		}
	}
}
