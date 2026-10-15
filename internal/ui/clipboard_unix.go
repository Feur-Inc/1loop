//go:build !windows

package ui

func imageClipboardData() ([]byte, error) { return linuxImageClipboardData() }
