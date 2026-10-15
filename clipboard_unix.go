//go:build !windows && !server

package main

func imageClipboardData() ([]byte, error) { return linuxImageClipboardData() }
