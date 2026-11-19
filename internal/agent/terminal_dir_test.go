// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// localMachine is an SSH machine whose files are this computer's: enough
// for the path checks, which only look and resolve links.
type localMachine struct{ Machine }

func (localMachine) Lstat(_ context.Context, name string) (fs.FileInfo, error) {
	return os.Lstat(name)
}

func (localMachine) RealPath(_ context.Context, name string) (string, error) {
	return filepath.EvalSymlinks(name)
}

// A terminal's folder on an SSH machine is checked as on the server: a link
// in the project that leads outside it is refused (#526). The desktop only
// compared the joined path's prefix, and accepted it.
func TestTerminalDirOnAMachine(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	workspace, _ = filepath.EvalSymlinks(workspace)
	os.Mkdir(filepath.Join(workspace, "src"), 0755)
	if err := os.Symlink(outside, filepath.Join(workspace, "out")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	machine := localMachine{}
	ctx := context.Background()
	if dir, err := TerminalDir(ctx, machine, workspace, ""); err != nil || dir != workspace {
		t.Errorf("no cwd: %q %v", dir, err)
	}
	if dir, err := TerminalDir(ctx, machine, workspace, "src"); err != nil || dir != filepath.Join(workspace, "src") {
		t.Errorf("a folder of the project: %q %v", dir, err)
	}
	if dir, err := TerminalDir(ctx, machine, workspace, "out"); err == nil {
		t.Errorf("a link out of the project was accepted: %q", dir)
	}
	if dir, err := TerminalDir(ctx, machine, workspace, "../"+filepath.Base(outside)); err == nil {
		t.Errorf("a path out of the project was accepted: %q", dir)
	}
	if dir, err := TerminalDir(ctx, nil, workspace, "out"); err == nil {
		t.Errorf("on this computer, a link out of the project was accepted: %q", dir)
	}
}
