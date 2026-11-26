// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package platform

import "os/exec"

// OOMFirst does nothing here: oom_score_adj is Linux's (oom_linux.go).
func OOMFirst(*exec.Cmd) {}
