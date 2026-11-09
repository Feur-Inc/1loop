// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Notebook and multi-edit approvals show the change, not an empty diff (#240).
func TestClaudeEditApprovals(t *testing.T) {
	root := t.TempDir()
	notebook := `{"cells":[{"id":"c0","cell_type":"markdown","source":"# Title"},{"id":"c1","cell_type":"code","source":["x = 1\n","print(x)"]}]}`
	if err := os.WriteFile(filepath.Join(root, "n.ipynb"), []byte(notebook), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, input, title string
		want               []string
	}{
		{"NotebookEdit", `{"notebook_path":"` + filepath.Join(root, "n.ipynb") + `","cell_id":"c1","new_source":"x 1234"}`,
			"Apply changes to n.ipynb?", []string{"@@ replace cell c1 (code) @@", "-x = 1", "-print(x)", "+x 1234"}},
		{"NotebookEdit", `{"notebook_path":"n.ipynb","cell_id":"cell-0","new_source":"","edit_mode":"delete"}`,
			"Apply changes to n.ipynb?", []string{"@@ delete cell cell-0 @@", "-# Title"}},
		{"NotebookEdit", `{"notebook_path":"n.ipynb","cell_id":"c0","new_source":"y = 2","cell_type":"code","edit_mode":"insert"}`,
			"Apply changes to n.ipynb?", []string{"@@ insert a new code cell after cell c0 @@", "+y = 2"}},
		{"MultiEdit", `{"file_path":"` + filepath.Join(root, "a.go") + `","edits":[{"old_string":"a","new_string":"b"},{"old_string":"c\nd","new_string":"e","replace_all":true}]}`,
			"Apply changes to a.go?", []string{"@@ edit 1 of 2 @@", "-a", "+b", "@@ edit 2 of 2, every occurrence @@", "-c", "-d", "+e"}},
	} {
		title, preview := claudeApproval(root, test.name, []byte(test.input))
		if title != test.title {
			t.Errorf("%s: title %q", test.input, title)
		}
		lines := strings.Split(preview, "\n")
		for _, want := range test.want {
			found := false
			for _, line := range lines {
				found = found || line == want
			}
			if !found {
				t.Errorf("%s: no line %q in\n%s", test.input, want, preview)
			}
		}
	}
	// The insert shows no removed lines.
	if _, preview := claudeApproval(root, "NotebookEdit", []byte(`{"notebook_path":"n.ipynb","cell_id":"c0","new_source":"y","edit_mode":"insert"}`)); strings.Contains(preview, "\n-") {
		t.Errorf("insert removes lines:\n%s", preview)
	}
}
