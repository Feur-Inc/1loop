package platform

import (
	"context"
	"os"
	"os/exec"
)

// WorkspaceCommand attaches the phone's project launcher to model-controlled
// processes. Desktop and remote-server processes keep their existing behavior.
type workspaceModeKey struct{}

func WithWorkspaceMode(ctx context.Context, yolo bool) context.Context {
	return context.WithValue(ctx, workspaceModeKey{}, yolo)
}
func WorkspaceYOLO(ctx context.Context) bool {
	value, _ := ctx.Value(workspaceModeKey{}).(bool)
	return value
}

func WorkspaceCommand(command *exec.Cmd, workspace string, yolo ...bool) {
	command.Dir = workspace
	if launcher := os.Getenv("ONELOOP_PROJECT_LAUNCHER"); launcher != "" {
		prefix := []string{launcher}
		if len(yolo) > 0 && yolo[0] {
			prefix = append(prefix, "--yolo")
		}
		command.Args = append(append(prefix, workspace, command.Path), command.Args[1:]...)
		command.Path = launcher
	}
}
