package remoteapi

import (
	"1loop/internal/agent"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Project browsing stays under a root already authorized by the host.
func ProjectFolder(base, path string, create bool) (string, error) {
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	if path == "" {
		path = base
	}
	resolved, err := agent.WorkspacePath(base, path)
	if err != nil {
		return "", err
	}
	if create {
		if err := os.Mkdir(resolved, 0755); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Choose a folder")
	}
	return resolved, nil
}

func ProjectFolders(base, path string) (map[string]any, error) {
	folder, err := ProjectFolder(base, path, false)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	folders := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child, err := ProjectFolder(base, filepath.Join(folder, entry.Name()), false)
		if err == nil {
			folders = append(folders, child)
		}
	}
	slices.Sort(folders)
	parent := ""
	canonicalBase, _ := filepath.EvalSymlinks(base)
	if folder != canonicalBase {
		parent = filepath.Dir(folder)
	}
	return map[string]any{"base": canonicalBase, "path": folder, "parent": parent, "separator": string(filepath.Separator), "folders": folders}, nil
}
