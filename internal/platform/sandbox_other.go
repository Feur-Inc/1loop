//go:build !linux

package platform

import (
	"fmt"
	"os/exec"
)

// Without Landlock there is no workspace sandbox: Auto mode reviews what is
// not a plain read (see agent/command_safety.go).
func SandboxAvailable() bool { return false }

func SandboxCommand(*exec.Cmd, string) error {
	return fmt.Errorf("No workspace sandbox on this system")
}

func SandboxMain() {}
