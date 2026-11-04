// SPDX-License-Identifier: MPL-2.0

package agent

import "testing"

func TestAPKToolsArePhoneToolsWithDefinitions(t *testing.T) {
	defined := map[string]bool{}
	for _, definition := range phoneToolDefinitions() {
		defined[definition["function"].(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"install_apk", "extract_apk", "uninstall_app"} {
		if !IsAPKTool(name) || !isPhoneTool(name) || !defined[name] {
			t.Errorf("%s: apk tool %v, phone tool %v, defined %v", name, IsAPKTool(name), isPhoneTool(name), defined[name])
		}
	}
	if IsAPKTool("phone_use") || IsAPKTool("adb_shell") {
		t.Error("ADB tools are not APK tools")
	}
}
