// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package ui

import (
	"os"
	"path/filepath"
)

// instanceAddress is a socket file in the user's config folder.
func instanceAddress() string {
	dir, err := os.UserConfigDir()
	if err != nil || os.MkdirAll(filepath.Join(dir, "1loop"), 0700) != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "desktop.sock")
}
