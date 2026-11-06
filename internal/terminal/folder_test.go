// SPDX-License-Identifier: MPL-2.0

package terminal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A missing folder is named as such, not as a missing shell (#197).
func TestTerminalMissingFolder(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{filepath.Join(dir, "gone"): "doesn't exist", file: "not a folder"} {
		_, err := NewTerminalProcess(context.Background(), path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if err := folderProblem(dir); err != nil {
		t.Fatal(err)
	}
}
