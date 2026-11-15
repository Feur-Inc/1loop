// SPDX-License-Identifier: MPL-2.0

//go:build !windows && !(linux && cgo)

package ui

import (
	"os"

	"fyne.io/fyne/v2"
)

// Elsewhere (macOS, the web client) the system draws: no other way to try.
func softwareRendererStart()         {}
func windowMissing(fyne.Window) bool { return false }
func rendererReady()                 { forgetTried() }

func renderFailed(failure error, release func()) {
	release()
	noteTried("OpenGL", failure)
	os.Stderr.WriteString(renderFailureTitle + "\n\n" + renderFailureText(triedSoFar(), []string{"Install or update the graphics driver, then start 1loop again."}) + "\n")
	os.Exit(1)
}
