// SPDX-License-Identifier: MPL-2.0

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspacePath resolves path (relative to root, or absolute) with its
// links, as the system would follow them, and refuses it unless it is root
// or inside it. A path that does not exist yet is resolved through its
// folder, which must exist.
func WorkspacePath(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("Missing path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		var parent string
		parent, err = filepath.EvalSymlinks(filepath.Dir(path))
		resolved = filepath.Join(parent, filepath.Base(path))
	}
	if err != nil {
		return "", err
	}
	if !Within(root, resolved) && !Within(realRoot(root), resolved) {
		return "", fmt.Errorf("The path is outside the workspace")
	}
	return resolved, nil
}

// NewPath is WorkspacePath for a path whose folders may not exist yet: the
// deepest one that does is resolved, the rest added to it as written (a
// missing folder holds no link).
func NewPath(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("Missing path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	existing, rest := filepath.Clean(path), ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("No folder of %s exists", path)
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	resolved = filepath.Join(resolved, rest)
	if !Within(root, resolved) && !Within(realRoot(root), resolved) {
		return "", fmt.Errorf("The path is outside the workspace")
	}
	return resolved, nil
}

// Within: path is root or inside it, comparing the names only (both are
// resolved already). Names that merely start with two dots, such as
// "..env.example", are inside.
func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// realRoot is root with its links resolved, or as given when it does not
// exist yet.
func realRoot(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return filepath.Clean(root)
}
