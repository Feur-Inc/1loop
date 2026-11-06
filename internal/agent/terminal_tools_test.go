// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package agent

import (
	"os"
	"path/filepath"
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

// In the Flatpak, $SHELL is host-shell: the instructions name the host's
// login shell it runs, not "host-shell".
func TestTerminalShellFlatpak(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{"host-shell": "#!/bin/sh\n", "host-run": "#!/bin/sh\necho /usr/bin/fish\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SHELL", filepath.Join(dir, "host-shell"))
	if got := terminalShell(); !strings.Contains(got, "interactive fish, not bash") {
		t.Fatalf("%q", got)
	}
}
