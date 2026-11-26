// SPDX-License-Identifier: MPL-2.0

package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications while 1loop is in the background, minimised or
// hidden in the tray; never while one of its windows has the focus (#439),
// nor when Settings turns them off. Clicking one opens its chat. In
// the Flatpak they go through the notification portal, which needs no D-Bus
// permission (Fyne itself would call org.freedesktop.Notifications).

import (
	"fyne.io/fyne/v2"
)

// notifies reports whether notifications go out now.
func (app *desktopApp) notifies() bool {
	return !app.notificationsOff && !app.inForeground
}

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	if !ui.notifies() {
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
