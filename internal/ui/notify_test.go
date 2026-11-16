// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

// Without a Fyne app, a notification that gets as far as sending would
// panic: these cases must stop before.
func TestNotifyUserSkipped(t *testing.T) {
	chat := &conversation{ID: "chat", Title: "Chat"}
	for name, set := range map[string]func(ui *desktopUI){
		"window focused":  func(ui *desktopUI) { ui.inForeground = true },
		"turned off":      func(ui *desktopUI) { ui.notificationsOff = true },
		"focused and off": func(ui *desktopUI) { ui.inForeground, ui.notificationsOff = true, true },
		"focused, another chat open": func(ui *desktopUI) {
			ui.inForeground = true
			ui.current = &conversation{ID: "other"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ui := &desktopUI{desktopApp: &desktopApp{}}
			set(ui)
			ui.notifyUser(chat, "Done.")
		})
	}
}
