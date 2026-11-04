//go:build !windows && (!linux || !cgo)

package ui

// Without cgo 1loop cannot speak to the display itself.
func clipboardReady() {}

func imageClipboardData() ([]byte, error) { return externalImageClipboardData() }
