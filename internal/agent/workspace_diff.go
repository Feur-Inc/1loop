package agent

// The workspace's uncommitted changes, as one patch (new files included).

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"1loop/internal/platform"
)

func WorkspaceDiff(ctx context.Context, root string, staged bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-C", root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	cmd := exec.CommandContext(ctx, "git", args...)
	platform.WorkspaceCommand(cmd, root, platform.WorkspaceYOLO(ctx))
	platform.PrepareProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		var detail string
		if failure, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(failure.Stderr))
		}
		return "", fmt.Errorf("Unable to read Git changes: %w\n%s", err, detail)
	}
	if staged {
		return string(out), nil
	}
	list := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	platform.WorkspaceCommand(list, root, platform.WorkspaceYOLO(ctx))
	platform.PrepareProcess(list)
	names, err := list.Output()
	if err != nil {
		return "", fmt.Errorf("Unable to read new Git files: %w", err)
	}
	var result strings.Builder
	result.Write(out)
	for _, name := range strings.Split(strings.TrimSuffix(string(names), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		fmt.Fprintf(&result, "diff --git a/%s b/%s\n", name, name)
		path, err := WorkspacePath(root, name)
		var text string
		if err == nil {
			text, err = fileText(path)
		}
		if err != nil {
			result.WriteString("New file · preview unavailable\n")
			continue
		}
		patch, err := platform.UnifiedDiff(ctx, name, "", text)
		if err != nil {
			return "", err
		}
		result.WriteString(patch)
	}
	return result.String(), nil
}
