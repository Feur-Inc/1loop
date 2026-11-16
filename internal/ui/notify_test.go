// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

// Notifications go out only while 1loop is in the background, and only when
// the setting is on (#439).
func TestNotificationsWanted(t *testing.T) {
	for _, c := range []struct {
		name         string
		off          bool
		inForeground bool
		want         bool
	}{
		{"in the background", false, false, true},
		{"window focused", false, true, false},
		{"turned off", true, false, false},
		{"turned off and focused", true, true, false},
	} {
		ui := &desktopUI{desktopApp: &desktopApp{}}
		ui.notificationsOff, ui.inForeground = c.off, c.inForeground
		if got := ui.notificationsWanted(); got != c.want {
			t.Errorf("%s: notificationsWanted() = %v, want %v", c.name, got, c.want)
		}
	}
}

// The window in front is the 1loop one, whatever chat it shows: another
// chat finishing does not notify while the user works in 1loop.
func TestNoNotificationForAnotherChatWhileFocused(t *testing.T) {
	main, other := twoWindows(t)
	watched, working := &conversation{ID: "a"}, &conversation{ID: "b"}
	main.current = working
	other.current = watched // a notification about it used to go out
	main.inForeground = true
	if main.notificationsWanted() {
		t.Fatal("a notification is sent while the 1loop window has the focus")
	}
	main.inForeground = false
	if !main.notificationsWanted() {
		t.Fatal("no notification in the background")
	}
}
