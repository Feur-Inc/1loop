package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications, unless the user is looking at it.

import "fyne.io/fyne/v2"

func (ui *desktopUI) notifyUser(chat *conversation, text string) {
	if ui.inForeground && chat == ui.current {
		return
	}
	title := chat.Title
	if title == "" {
		title = "1loop"
	}
	fyne.CurrentApp().SendNotification(fyne.NewNotification(title, text))
}
