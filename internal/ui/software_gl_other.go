// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package ui

import "fyne.io/fyne/v2"

// Only Windows falls back to Mesa drawing on the CPU (software_gl_windows.go).
func softwareRendererStart()                    {}
func windowMissing(fyne.Window) bool            { return false }
func startSoftwareRenderer(release func()) bool { return false }
