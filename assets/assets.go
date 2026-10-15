// Package assets holds the files built into 1loop: its artwork and the
// Computer Use helpers it runs.
package assets

import "embed"

// Artwork is rasterized from the original DA files; the provider logos keep
// their licence beside them.
//
//go:embed *.png provider-*.svg
var Artwork embed.FS

// ComputerUsePortal drives the Linux desktop through the XDG portals.
//
//go:embed computer_use.py
var ComputerUsePortal string

// ComputerUseWindows drives the Windows desktop through UI Automation.
//
//go:embed computer_use_windows.ps1
var ComputerUseWindows string
