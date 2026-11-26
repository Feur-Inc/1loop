// SPDX-License-Identifier: MPL-2.0

package python

import (
	"os/exec"
	"syscall"
)

func alone(cmd *exec.Cmd) { hidden(cmd) }

// hidden opens no console window.
func hidden(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
