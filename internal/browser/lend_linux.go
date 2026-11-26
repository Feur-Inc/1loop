// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"os/exec"
	"syscall"
)

// lentProcess ends the lent browser with 1loop: killed when 1loop dies,
// it does not keep its profile locked. A setuid launcher (snap's) clears
// the signal; lentOrphan covers that case.
func lentProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
