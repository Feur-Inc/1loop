package agent

// request_permissions: the model asks for a precise access instead of all or
// nothing. 1loop has no sandbox, so a grant does two things only: a granted
// folder opens to the file tools (read_file, list_directory, edit_file,
// apply_patch) for the rest of the chat, and every grant is told to the
// automatic review, which judges actions knowing what the user allowed.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PermissionGrants are what the user allowed a chat beyond its workspace.
type PermissionGrants struct {
	mu      sync.Mutex
	folders []string
	network bool
}

type grantsContextKey struct{}

func (grants *PermissionGrants) snapshot() ([]string, bool) {
	if grants == nil {
		return nil, false
	}
	grants.mu.Lock()
	defer grants.mu.Unlock()
	return append([]string(nil), grants.folders...), grants.network
}

// summary says what was granted, for the automatic review.
func (grants *PermissionGrants) summary() string {
	folders, network := grants.snapshot()
	var parts []string
	if len(folders) > 0 {
		parts = append(parts, "read and write in "+strings.Join(folders, ", "))
	}
	if network {
		parts = append(parts, "network access")
	}
	if len(parts) == 0 {
		return ""
	}
	return "The user granted this chat: " + strings.Join(parts, "; ") + "."
}

func permissionsToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "request_permissions",
		"description": "Ask the user for a precise access beyond the workspace, with the reason: folders (absolute paths) your file tools may read and write for the rest of this chat, and/or network access. Ask only for what the task needs; the user may refuse. 1loop has no sandbox: commands are approved one by one, grants inform that review.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"reason":  map[string]any{"type": "string"},
			"folders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"network": map[string]any{"type": "boolean"},
		}, "required": []string{"reason", "folders", "network"}, "additionalProperties": false},
	}}
}

func (backend *App) requestPermissions(ctx context.Context, arguments string, begin func(), emit emitter) (string, error) {
	grants, _ := ctx.Value(grantsContextKey{}).(*PermissionGrants)
	if grants == nil {
		return "", fmt.Errorf("Permissions are not attached to this conversation")
	}
	var args struct {
		Reason  string
		Folders []string
		Network bool
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	var folders []string
	for _, folder := range args.Folders {
		if !filepath.IsAbs(folder) {
			return "", fmt.Errorf("%s: give an absolute path", folder)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(folder))
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			return "", fmt.Errorf("%s is not a folder", folder)
		}
		folders = append(folders, resolved)
	}
	if len(folders) == 0 && !args.Network {
		return "", fmt.Errorf("Ask for at least one folder or for network access")
	}
	preview := "Reason: " + strings.TrimSpace(args.Reason)
	if len(folders) > 0 {
		preview += "\n\nRead and write, for this chat:\n  " + strings.Join(folders, "\n  ")
	}
	if args.Network {
		preview += "\n\nNetwork access"
	}
	if !backend.confirm(ctx, emit, "Grant access?", preview) {
		return "", fmt.Errorf("The user refused this access")
	}
	begin()
	grants.mu.Lock()
	grants.folders = append(grants.folders, folders...)
	grants.network = grants.network || args.Network
	grants.mu.Unlock()
	return "Granted. " + grants.summary(), nil
}

// toolPath resolves a file tool's path: inside the workspace, or inside a
// folder the user granted this chat.
func toolPath(ctx context.Context, root, path string) (string, error) {
	resolved, err := WorkspacePath(root, path)
	if err == nil || !filepath.IsAbs(path) {
		return resolved, err
	}
	grants, _ := ctx.Value(grantsContextKey{}).(*PermissionGrants)
	folders, _ := grants.snapshot()
	for _, folder := range folders {
		if inside, err := WorkspacePath(folder, path); err == nil {
			return inside, nil
		}
	}
	return "", err
}

// newFilePath resolves where a new file goes, its folders possibly not made
// yet: the deepest folder that exists is resolved, the rest added to it.
func newFilePath(ctx context.Context, root, path string) (string, error) {
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	full = filepath.Clean(full)
	existing, rest := full, ""
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
	resolved, err := toolPath(ctx, root, existing)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, rest), nil
}
