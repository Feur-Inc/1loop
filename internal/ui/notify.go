// SPDX-License-Identifier: MPL-2.0

package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications, unless the user is looking at it, and also
// while the window is hidden in the tray. Clicking one opens its chat. In
// the Flatpak they go through the notification portal, which needs no D-Bus
// permission (Fyne itself would call org.freedesktop.Notifications).

import (
	"fyne.io/fyne/v2"
)

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	if ui.inForeground && ui.showing(chat) { // on screen in one of the windows
		return
	}
	title := chat.Title
	if title == "" {
		title = "1loop"
	}
	open := func() {
		ui.openWhereShown(chat)
	}
	if sendClickableNotification(title, text, open) {
		return
	}
	// Elsewhere (Windows) a click on it opens nothing.
	fyne.CurrentApp().SendNotification(fyne.NewNotification(title, text))
}
