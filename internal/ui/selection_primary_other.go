// SPDX-License-Identifier: MPL-2.0

//go:build !linux || !cgo

package ui

import "fyne.io/fyne/v2"

// setPrimarySelection does nothing: only X11 and Wayland have a primary
// selection (selection_primary_x11.go, selection_primary_wayland.go).
func setPrimarySelection(fyne.Window, string) {}
