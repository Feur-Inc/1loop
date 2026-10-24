//go:build !windows

package computeruse

import (
	"context"
	"os"

	"1loop/assets"
)

// computerPython runs the portal helper: the system's own Python, where the
// distribution installs python3-gi and its GStreamer bindings (a virtualenv
// or pyenv python3 found first on PATH usually lacks them). Packages that
// bring their own (Nix) or reach the host's (Flatpak) set
// ONELOOP_COMPUTER_PYTHON.
func computerPython() string {
	if python := os.Getenv("ONELOOP_COMPUTER_PYTHON"); python != "" {
		return python
	}
	return "/usr/bin/python3"
}

func startNativeComputer(ctx context.Context) (*computerProcess, error) {
	return startComputerProcess(ctx, computerPython(), "-u", "-c", assets.ComputerUsePortal)
}
func ComputerGuideText() string { return computerGuide }
