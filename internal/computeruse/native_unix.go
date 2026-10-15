//go:build !windows

package computeruse

import (
	"context"

	"1loop/assets"
)

func startNativeComputer(ctx context.Context) (*computerProcess, error) {
	return startComputerProcess(ctx, "/usr/bin/python3", "-u", "-c", assets.ComputerUsePortal)
}
func ComputerGuideText() string { return computerGuide }
