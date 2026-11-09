// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import "fyne.io/fyne/v2"

// settingsSectionShown and browserSettings change Settings in the browser
// only (settings_browser_js.go).
func settingsSectionShown(string) bool { return true }

func (ui *desktopUI) browserSettings(string) fyne.CanvasObject { return nil }
