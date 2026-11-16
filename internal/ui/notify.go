// SPDX-License-Identifier: MPL-2.0

package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications, only while the window is in the background,
// minimised or hidden in the tray, and unless they are turned off in Settings. Clicking one opens its chat. In
// the Flatpak they go through the notification portal, which needs no D-Bus
// permission (Fyne itself would call org.freedesktop.Notifications).

import (
	"fyne.io/fyne/v2"
)

// notifies says whether a notification may be sent now.
func (ui *desktopUI) notifies() bool { return !ui.notificationsOff && !ui.inForeground }

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	// None when the user turned them off, nor while the window has the
	// focus: the sidebar already shows every chat's state.
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
