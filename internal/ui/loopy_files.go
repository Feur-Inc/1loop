// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os"
	"path/filepath"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// moveLoopyFile moves an earlier version's file, unless the new one is there.
func moveLoopyFile(from, to string) {
	if !fileExists(from) || fileExists(to) {
		return
	}
	if os.MkdirAll(filepath.Dir(to), 0700) == nil {
		os.Rename(from, to)
	}
}
