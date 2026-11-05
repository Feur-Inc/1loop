// SPDX-License-Identifier: MPL-2.0

package agent

import "testing"

func TestCallMarkIsTheTerminalForCommands(t *testing.T) {
	for tool, want := range map[string]string{"shell": "bash", "adb_shell": "bash", "terminal_command": "bash", "terminal_tabs": "call", "read_file": "call", "": "call",
		"phone_use": "phone", "mcp__1loop_todo__phone_use": "phone", "install_apk": "phone", "extract_apk": "phone", "uninstall_app": "phone"} {
		if got := CallMark(tool); got != want {
			t.Errorf("CallMark(%q) = %q, want %q", tool, got, want)
		}
	}
}
