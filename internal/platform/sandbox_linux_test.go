//go:build linux

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The test binary is the sandbox helper too, as 1loop is (SandboxCommand
// starts os.Executable).
func TestMain(m *testing.M) {
	SandboxMain()
	os.Exit(m.Run())
}

func TestSandboxConfinesWrites(t *testing.T) {
	if !SandboxAvailable() || os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		t.Skip("no Landlock on this kernel")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{workspace, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// t.TempDir is under /tmp, which the sandbox lets commands write: the
	// home folder is a place it does not.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	escape := filepath.Join(home, ".1loop-sandbox-test")
	run := func(script string) (string, error) {
		command := exec.Command("/bin/sh", "-c", script)
		command.Dir = workspace
		if err := SandboxCommand(command, workspace); err != nil {
			t.Fatal(err)
		}
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := run("echo ok > made.txt && mkdir -p sub && mv made.txt sub/ && rm sub/made.txt && echo tmp > " + filepath.Join(outside, "tmp.txt")); err != nil {
		t.Fatalf("work inside the workspace and /tmp failed: %v\n%s", err, output)
	}
	if output, err := run("echo no > " + escape); err == nil {
		os.Remove(escape)
		t.Fatalf("wrote outside the workspace: %s", output)
	}
	if _, err := os.Stat(escape); err == nil {
		os.Remove(escape)
		t.Fatal("the file outside the workspace exists")
	}
	if output, err := run("cat /etc/hostname >/dev/null && ls " + home + " >/dev/null"); err != nil {
		t.Fatalf("reading outside the workspace failed: %v\n%s", err, output)
	}
}

// In the Flatpak, commands run on the host, where Landlock does not follow.
func TestNoSandboxInFlatpak(t *testing.T) {
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		t.Skip("the phone's launcher confines commands itself")
	}
	t.Setenv("ONELOOP_FLATPAK", "1")
	if SandboxAvailable() {
		t.Fatal("SandboxAvailable in the Flatpak")
	}
}
