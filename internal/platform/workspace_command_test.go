package platform

import (
	"context"
	"reflect"
	"testing"
)

func TestWorkspaceCommandWorkingDirectory(t *testing.T) {
	t.Setenv("ONELOOP_PROJECT_LAUNCHER", "/fixture/project-launcher")
	for _, yolo := range []bool{false, true} {
		cmd := ShellCommand(context.Background(), "pwd")
		program, args := cmd.Path, append([]string(nil), cmd.Args[1:]...)
		WorkspaceCommandIn(cmd, "/projects/demo", "/projects/demo/child", yolo)
		want := []string{"/fixture/project-launcher"}
		if yolo {
			want = append(want, "--yolo")
		}
		want = append(want, "/projects/demo", "--workdir", "/projects/demo/child", program)
		want = append(want, args...)
		if cmd.Dir != "/projects/demo/child" || !reflect.DeepEqual(cmd.Args, want) {
			t.Fatalf("%q %+v", cmd.Dir, cmd.Args)
		}
	}
	cmd := ShellCommand(context.Background(), "pwd")
	WorkspaceCommand(cmd, "/projects/demo")
	if len(cmd.Args) < 3 || cmd.Args[1] != "/projects/demo" || cmd.Args[2] == "--workdir" {
		t.Fatal(cmd.Args)
	}
}
