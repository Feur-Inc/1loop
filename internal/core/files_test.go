// SPDX-License-Identifier: MPL-2.0

package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The settings and credentials files are written whole, with their
// permissions, and leave no temporary file behind (#523).
func TestWriteFileSafely(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "credentials.json")
	if err := WriteFileSafely(path, 0600, false, []byte(`{"token":`), []byte(`"x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileSafely(path, 0600, true, []byte(`{"token":"y"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"token":"y"}` {
		t.Fatalf("content: %q %v", data, err)
	}
	if old, _ := os.ReadFile(path + ".bak"); string(old) != `{"token":"x"}` {
		t.Fatalf("backup: %q", old)
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 2 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("files left: %v", names)
	}
}
