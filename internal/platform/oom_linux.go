// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"os"
	"os/exec"
	"strconv"
)

// ChildOOMScoreAdj is the oom_score_adj of the processes 1loop starts for
// its agents: Claude Code, agy, the commands and builds they run.
const ChildOOMScoreAdj = 500

// OOMFirst makes a started process, and what it starts in turn (they
// inherit it), the one the kernel's out-of-memory killer picks before
// 1loop: a build that fills the memory kills itself, not the server and
// every chat with it (#584). Raising one's own processes' score needs no
// privilege; it is best effort, and a failure is ignored.
func OOMFirst(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	os.WriteFile("/proc/"+strconv.Itoa(cmd.Process.Pid)+"/oom_score_adj", []byte(strconv.Itoa(ChildOOMScoreAdj)), 0)
}
