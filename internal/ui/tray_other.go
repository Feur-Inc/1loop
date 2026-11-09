// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package ui

// trayHostPresent: Windows' notification area is always there.
func trayHostPresent() bool { return true }
