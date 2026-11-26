// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func globFiles(pattern string) ([]string, error) { return filepath.Glob(pattern) }
