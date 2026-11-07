// SPDX-License-Identifier: MPL-2.0

package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications, unless the user is looking at it. In the
// Flatpak they go through the notification portal, which needs no D-Bus
// permission (Fyne itself would call org.freedesktop.Notifications).

import (
	"os"

	"fyne.io/fyne/v2"
)

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	if ui.inForeground && chat == ui.current {
		return
	}
	title := chat.Title
	if title == "" {
		title = "1loop"
	}
	if os.Getenv("ONELOOP_FLATPAK") != "" {
		// The portal shows the application's own icon.
		go portalNotify(title, text)
		return
	}
	fyne.CurrentApp().SendNotification(fyne.NewNotification(title, text))
}
