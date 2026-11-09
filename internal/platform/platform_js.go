// SPDX-License-Identifier: MPL-2.0

package platform

// The browser client (GOOS=js) starts no programs: it drives a server,
// which runs them. These keep the shared packages building.

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

var errBrowser = errors.New("not available in the browser")

func ShellDescription() string { return "none (browser)" }
func ShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "bash", "-c", command) // fails to start
}
func PrepareProcess(*exec.Cmd)         {}
func PrepareComputerProcess(*exec.Cmd) {}
func KillProcessTree(*exec.Cmd) error  { return errBrowser }
func InterruptProcess(*exec.Cmd) error { return errBrowser }
func RestartProcess(string) error      { return errBrowser }
func CpuTime() time.Duration           { return 0 }
func watchDevSignals()                 {}
func UnifiedDiff(context.Context, string, string, string) (string, error) {
	return "", errBrowser
}
