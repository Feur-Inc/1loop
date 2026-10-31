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
	WorkspaceCommandIn(command, workspace, workspace, yolo...)
}

// WorkspaceCommandIn keeps the project boundary separate from the working directory.
func WorkspaceCommandIn(command *exec.Cmd, workspace, dir string, yolo ...bool) {
	command.Dir = dir
	if launcher := os.Getenv("ONELOOP_PROJECT_LAUNCHER"); launcher != "" {
		prefix := []string{launcher}
		if len(yolo) > 0 && yolo[0] {
			prefix = append(prefix, "--yolo")
		}
		prefix = append(prefix, workspace)
		if dir != workspace {
			prefix = append(prefix, "--workdir", dir)
		}
		command.Args = append(append(prefix, command.Path), command.Args[1:]...)
		command.Path = launcher
	}
}
