// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"encoding/json"
	"testing"
)

func TestDescribeApplyPatch(t *testing.T) {
	patch := func(text string) string {
		args, _ := json.Marshal(map[string]string{"patch": text})
		return string(args)
	}
	for _, test := range []struct {
		item         ChatItem
		running      bool
		verb, target string
	}{
		{ChatItem{Tool: "apply_patch", Args: patch("*** Begin Patch\n*** Update File: main.go\n@@\n-a\n+b\n*** End Patch")}, false, "Edited", "main.go"},
		{ChatItem{Tool: "apply_patch", Args: patch("*** Begin Patch\n*** Add File: a.go\n+x\n*** Delete File: b.go\n*** Update File: c.go\n@@\n-a\n+b\n*** End Patch")}, true, "Editing", "a.go and 2 more"},
		// Arguments that are not a patch: the path comes from the recorded diff.
		{ChatItem{Tool: "apply_patch", Args: `{}`, Diff: "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b"}, false, "Edited", "x.go"},
	} {
		verb, target, _ := DescribeTool(test.item, test.running)
		if verb != test.verb || target != test.target {
			t.Errorf("DescribeTool(%s) = %q %q, want %q %q", test.item.Args, verb, target, test.verb, test.target)
		}
	}
}

func TestDescribePhoneSession(t *testing.T) {
	for _, test := range []struct {
		args         string
		verb, target string
	}{
		{`{"session":"user","action":"tap","x":10,"y":20}`, "Tapped the phone", "at 10, 20"},
		{`{"action":"state"}`, "Read", "the phone's screen"},
		{`{"session":"hidden","action":"launch","text":"org.mozilla.firefox"}`, "Opened in the hidden session:", "org.mozilla.firefox"},
		{`{"session":"hidden","action":"screenshot"}`, "Looked at", "the hidden phone session"},
		{`{"session":"hidden","action":"profile_install","text":"org.mozilla.firefox"}`, "Added to the work profile:", "org.mozilla.firefox"},
	} {
		verb, target, _ := DescribeTool(ChatItem{Tool: "phone_use", Args: test.args}, false)
		if verb != test.verb || target != test.target {
			t.Errorf("DescribeTool(%s) = %q %q, want %q %q", test.args, verb, target, test.verb, test.target)
		}
	}
}
