package agent

import "testing"

func TestCallMarkIsTheTerminalForCommands(t *testing.T) {
	for tool, want := range map[string]string{"shell": "bash", "adb_shell": "bash", "terminal_command": "bash", "terminal_tabs": "call", "read_file": "call", "": "call"} {
		if got := CallMark(tool); got != want {
			t.Errorf("CallMark(%q) = %q, want %q", tool, got, want)
		}
	}
}
