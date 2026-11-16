// SPDX-License-Identifier: MPL-2.0

package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications, but only while the 1loop window is in the
// background, minimised or hidden in the tray (the sidebar already shows the
// chats' states when it has the focus), and unless the user turned them off
// in Settings. Clicking one opens its chat. In
// the Flatpak they go through the notification portal, which needs no D-Bus
// permission (Fyne itself would call org.freedesktop.Notifications).

import (
	"fyne.io/fyne/v2"
)

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	if ui.notificationsOff || ui.inForeground { // off in Settings, or the window has the focus
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
