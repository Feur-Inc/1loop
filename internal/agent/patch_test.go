// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyHunksKeepsLineEndings(t *testing.T) {
	hunks := []patchHunk{{old: []string{"one", "two", "three"}, fresh: []string{"one", "TWO", "three"}}}
	for _, test := range []struct{ name, before, want string }{
		{"lf", "one\ntwo\nthree\n", "one\nTWO\nthree\n"},
		{"crlf", "zero\r\none\r\ntwo\r\nthree\r\nfour\r\n", "zero\r\none\r\nTWO\r\nthree\r\nfour\r\n"},
		{"crlf without a final newline", "one\r\ntwo\r\nthree", "one\r\nTWO\r\nthree\r\n"},
		{"one stray lf in a crlf file", "one\r\ntwo\nthree\r\n", "one\r\nTWO\r\nthree\r\n"},
	} {
		got, err := applyHunks("file.txt", test.before, hunks)
		if err != nil || got != test.want {
			t.Errorf("%s: got %q, %v; want %q", test.name, got, err, test.want)
		}
	}
}

func TestApplyPatchCRLF(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "main.go")
	os.WriteFile(path, []byte("package main\r\n\r\nfunc main() {\r\n\tprintln(1)\r\n}\r\n"), 0644)
	patch := "*** Begin Patch\n*** Update File: main.go\n@@ func main() {\n-\tprintln(1)\n+\tprintln(2)\n }\n*** End Patch"
	args, _ := json.Marshal(map[string]string{"patch": patch})
	call := ToolCall{}
	call.Function.Name, call.Function.Arguments = "apply_patch", string(args)
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: true})
	_, diff, err := (&App{}).runTool(ctx, workspace, call, func() {}, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if want := "package main\r\n\r\nfunc main() {\r\n\tprintln(2)\r\n}\r\n"; string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
	var changed []string
	for _, line := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) && !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") {
			changed = append(changed, line)
		}
	}
	if len(changed) != 2 {
		t.Fatalf("the diff changes %d lines, want 2:\n%s", len(changed), diff)
	}
}
