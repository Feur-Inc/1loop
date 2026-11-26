// SPDX-License-Identifier: MPL-2.0

//go:build !windows && !js

package python

import (
	"os/exec"
	"syscall"
)

// alone gives a script its own process group, ended whole.
func alone(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

func hidden(*exec.Cmd) {}
