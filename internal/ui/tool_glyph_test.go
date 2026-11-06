// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

func TestToolGlyphPhoneUse(t *testing.T) {
	for _, tool := range []string{"phone_use", "mcp__1loop_todo__phone_use", "install_apk", "uninstall_app", "extract_apk"} {
		if toolGlyph(tool) != iconPhone {
			t.Errorf("%s does not show the phone icon", tool)
		}
	}
	if toolGlyph("adb_shell") != iconTerminal { // a command, as in #138
		t.Error("adb_shell lost the terminal icon")
	}
}
