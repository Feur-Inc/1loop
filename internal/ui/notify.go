// SPDX-License-Identifier: MPL-2.0

package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications while 1loop is not in front (the window in the
// background, minimised or hidden in the tray). Never while the window has
// the focus, whatever chat it shows: the sidebar already says how the chats
// are doing. They can be turned off altogether in Settings (General →
// Notifications). Clicking one opens its chat. In the Flatpak they go through
// the notification portal, which needs no D-Bus permission (Fyne itself would
// call org.freedesktop.Notifications).

import (
	"fyne.io/fyne/v2"
)

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	if !ui.notificationsWanted() {
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

// notificationsWanted reports whether news from a chat goes out as a desktop
// notification: the setting is on, and 1loop is not in front (its window in
// the background, minimised or hidden in the tray). While the user works in
// the window the sidebar already shows how the chats are doing, whichever
// chat the window shows (#439).
func (ui *desktopUI) notificationsWanted() bool {
	return !ui.notificationsOff && !ui.inForeground
}
