// SPDX-License-Identifier: MPL-2.0

//go:build linux && wayland && cgo

package ui

import "fyne.io/fyne/v2"

// setPrimarySelection: GLFW 3.3 does not speak Wayland's primary selection
// protocol (zwp_primary_selection_device_manager_v1), so the Wayland build
// has none yet.
func setPrimarySelection(fyne.Window, string) {}
