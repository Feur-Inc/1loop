// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"fmt"
	"os"
	"runtime/debug"

	"1loop/internal/platform"
)

// recovered turns a panic in a turn or a tool call into an error for that
// turn or call. Uncaught, a panic in one goroutine ends the whole process:
// every chat at work would stop with it. Use as defer recovered(&err, "…").
func recovered(err *error, what string) {
	failure := recover()
	if failure == nil {
		return
	}
	stack := debug.Stack()
	fmt.Fprintf(os.Stderr, "1loop: internal error in %s: %v\n%s", what, failure, stack)
	platform.Devf("agent", "internal error in %s: %v\n%s", what, failure, stack)
	*err = fmt.Errorf("1loop hit an internal error in %s and stopped it: %v", what, failure)
}
