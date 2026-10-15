//go:build !windows && !server

package main

import "context"

func startNativeComputer(ctx context.Context) (*computerProcess, error) {
	return startComputerProcess(ctx, "/usr/bin/python3", "-u", "-c", computerPortalSource)
}
func computerGuideText() string { return computerGuide }
