// SPDX-License-Identifier: MPL-2.0

package update

import (
	"os"
	"path/filepath"
	"testing"
)

// A file that cannot be replaced undoes the ones before it: never two
// versions side by side (#394).
func TestReplacePutsEveryFileBack(t *testing.T) {
	staging, dir := t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"1loop", "added", "z-server"} {
		write(filepath.Join(staging, name), "new")
	}
	write(filepath.Join(dir, "1loop"), "old")
	write(filepath.Join(dir, "z-server"), "old")
	// z-server's backup cannot be made: a folder that is not empty is in its place.
	write(filepath.Join(dir, "z-server.old", "in the way"), "x")
	if err := replace(staging, dir); err == nil {
		t.Fatal("the update went through")
	}
	for _, name := range []string{"1loop", "z-server"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != "old" {
			t.Errorf("%s: %q, %v", name, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "added")); !os.IsNotExist(err) {
		t.Errorf("a file of the new version stayed: %v", err)
	}
}
