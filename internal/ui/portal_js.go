// SPDX-License-Identifier: MPL-2.0

package ui

// No desktop portal in the browser (portal.go): files come through Fyne's
// own dialog, which has nothing to browse there, and notifications through
// Fyne.

import "fyne.io/fyne/v2"

func portalHandle(fyne.Window) string { return "" }

func portalPick(string, pickRequest) ([]string, bool) { return nil, false }

func portalNotify(title, text string) {
	fyne.CurrentApp().SendNotification(fyne.NewNotification(title, text))
}
