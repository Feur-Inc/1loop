// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package agent

import (
	"strings"
	"testing"
)

// The terminal tabs run the user's $SHELL, which the instructions name.
func TestTerminalShell(t *testing.T) {
	for shell, want := range map[string]string{"/usr/bin/zsh": "zsh, not bash", "/bin/bash": "interactive bash", "": "interactive bash"} {
		t.Setenv("SHELL", shell)
		if got := terminalShell(); !strings.Contains(got, want) {
			t.Errorf("SHELL=%q: %q, want %q", shell, got, want)
		}
	}
}
