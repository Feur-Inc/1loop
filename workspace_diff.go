package main

// The workspace's uncommitted changes, as one patch (new files included).

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func workspaceDiff(ctx context.Context, root string, staged bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-C", root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Unable to read Git changes: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if staged {
		return string(out), nil
	}
	names, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
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
		path, err := workspacePath(root, name)
		var text string
		if err == nil {
			text, err = fileText(path)
		}
		if err != nil {
			result.WriteString("New file · preview unavailable\n")
			continue
		}
		patch, err := unifiedDiff(ctx, name, "", text)
		if err != nil {
			return "", err
		}
		result.WriteString(patch)
	}
	return result.String(), nil
}
