// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package ui

// Elsewhere the Save As dialog is the portal's, zenity's, kdialog's or
// AppleScript's (filepicker.go).
func windowsSavePick(pickRequest) ([]string, error) { return nil, errNoNativePicker }
