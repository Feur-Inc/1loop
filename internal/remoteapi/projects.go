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

type ProjectChange struct {
	Path    string
	Name    string
	Confirm string
}

// ProjectWithin also includes registered projects nested under a renamed or deleted folder.
func ProjectWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func MovedProject(root, destination, path string) string {
	if !ProjectWithin(root, path) {
		return path
	}
	relative, _ := filepath.Rel(root, path)
	return filepath.Join(destination, relative)
}

// ChangeProject changes only a registered directory, never the host's private state.
// An explicit path confirmation is required for recursive deletion.
func ChangeProject(projects []string, privateDir, action string, args ProjectChange) (string, error) {
	if !slices.Contains(projects, args.Path) {
		return "", fmt.Errorf("Choose a registered project")
	}
	path, err := filepath.EvalSymlinks(args.Path)
	if err != nil || path != args.Path {
		return "", fmt.Errorf("The project folder changed; add it again before continuing")
	}
	privateDir, err = filepath.Abs(privateDir)
	if err != nil {
		return "", err
	}
	if path == filepath.Dir(path) || ProjectWithin(path, privateDir) {
		return "", fmt.Errorf("This folder contains 1loop's private state or is a filesystem root")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Choose a project directory")
	}
	switch action {
	case "rename":
		name := strings.TrimSpace(args.Name)
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`+"\x00") {
			return "", fmt.Errorf("Enter a folder name without path separators")
		}
		destination := filepath.Join(filepath.Dir(path), name)
		if destination == path {
			return path, nil
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			return "", fmt.Errorf("A folder or file with that name already exists")
		}
		if err := os.Rename(path, destination); err != nil {
			return "", err
		}
		return destination, nil
	case "delete":
		if args.Confirm != path {
			return "", fmt.Errorf("Confirm the exact project folder before deleting its files")
		}
		return "", os.RemoveAll(path)
	default:
		return "", fmt.Errorf("Unknown project action")
	}
}
