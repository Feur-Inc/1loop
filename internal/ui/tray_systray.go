// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import "fyne.io/systray"

// The systray calls Fyne does not wrap. systray reaches the tray host over
// D-Bus, which does not build for the browser (tray_js.go).

func traySetOnTapped(tapped func()) { systray.SetOnTapped(tapped) }

func traySetTooltip(text string) { systray.SetTooltip(text) }
