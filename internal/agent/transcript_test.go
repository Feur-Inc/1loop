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
