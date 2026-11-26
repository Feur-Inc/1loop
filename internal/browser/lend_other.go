// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package browser

import "os/exec"

func lentProcess(*exec.Cmd) {}
