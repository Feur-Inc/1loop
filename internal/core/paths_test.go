// SPDX-License-Identifier: MPL-2.0

package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWorkspacePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, "sub"), 0755)
	os.Symlink(outside, filepath.Join(root, "out"))
	os.Symlink("sub", filepath.Join(root, "in"))
	for path, inside := range map[string]bool{
		"..env.example":             true,
		"sub/new":                   true,
		"in/new":                    true,
		filepath.Join(root, "a"):    true,
		"../x":                      false,
		"out/x":                     false,
		"out":                       false,
		filepath.Join(outside, "a"): false,
	} {
		if _, err := WorkspacePath(root, path); (err == nil) != inside {
			t.Errorf("WorkspacePath %s: %v", path, err)
		}
		if _, err := NewPath(root, path); (err == nil) != inside {
			t.Errorf("NewPath %s: %v", path, err)
		}
	}
	if _, err := WorkspacePath(root, "new/folder/file"); err == nil {
		t.Error("WorkspacePath needs the folder to exist")
	}
	if resolved, err := NewPath(root, "in/new/folder/file"); err != nil || resolved != filepath.Join(root, "sub/new/folder/file") {
		t.Errorf("NewPath through a link inside: %s %v", resolved, err)
	}
	if _, err := NewPath(root, "out/new/folder"); err == nil {
		t.Error("NewPath through a link out")
	}
}
