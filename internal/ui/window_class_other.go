// SPDX-License-Identifier: MPL-2.0

//go:build !linux || !cgo

package ui

// Elsewhere the window's application is the executable itself.
func nameWindows() {}
