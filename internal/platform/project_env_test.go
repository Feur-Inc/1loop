// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"testing"
)

// Project variables keep valid names only, sorted, and reach a command of
// this computer and a shell of another machine as they are, quotes and
// dollars included.
func TestProjectEnv(t *testing.T) {
	ctx := WithProjectEnv(context.Background(), map[string]string{"B_2": "it's $HOME", "A": "1", "9X": "no", "": "no"})
	if got := ProjectEnv(ctx); !slices.Equal(got, []string{"A=1", "B_2=it's $HOME"}) {
		t.Fatalf("pairs %q", got)
	}
	if ProjectEnv(context.Background()) != nil || ExportScript(context.Background()) != "" {
		t.Fatal("variables without a project")
	}
	if runtime.GOOS == "windows" {
		return
	}
	command := exec.Command("sh", "-c", `printf %s "$B_2"`)
	ApplyProjectEnv(ctx, command)
	if out, err := command.Output(); err != nil || string(out) != "it's $HOME" {
		t.Fatalf("command: %q %v", out, err)
	}
	out, err := exec.Command("sh", "-c", ExportScript(ctx)+`printf %s "$B_2|$A"`).Output()
	if err != nil || string(out) != "it's $HOME|1" {
		t.Fatalf("export script: %q %v", out, err)
	}
}
