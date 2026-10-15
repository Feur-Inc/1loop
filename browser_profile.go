package main

import (
	"os"
	"path/filepath"
)

// browserProfileDir is the browser's profile, beside 1loop's state (so a
// copy of 1loop run with another config folder has a browser of its own).
var browserProfileOverride string

func browserProfileDir() string {
	if browserProfileOverride != "" {
		return browserProfileOverride
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "browser")
}
