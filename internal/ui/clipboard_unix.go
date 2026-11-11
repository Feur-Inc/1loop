// SPDX-License-Identifier: MPL-2.0

//go:build !windows && !linux

package ui

// Elsewhere (macOS, the BSDs) 1loop asks the clipboard tools.
func clipboardReady() {}

func imageClipboardData() ([]byte, error) { return externalImageClipboardData() }
