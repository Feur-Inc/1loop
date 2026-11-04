package ui

// Notifications: a chat that finishes or waits for the user says so through
// the desktop's notifications, unless the user is looking at it. In the
// Flatpak they go through the notification portal, which needs no D-Bus
// permission (Fyne itself would call org.freedesktop.Notifications).

import (
	"os"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"github.com/rymdport/portal/notification"

	"1loop/internal/platform"
)

// portalNotification numbers the portal's notifications: one id each, so a
// new one does not replace the last.
var portalNotification atomic.Uint64

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
		go func() {
			if err := notification.Add(uint(portalNotification.Add(1)), notification.Content{Title: title, Body: text}); err != nil {
				platform.Devf("app", "notification portal: %v", err)
			}
		}()
		return
	}
	fyne.CurrentApp().SendNotification(fyne.NewNotification(title, text))
}
