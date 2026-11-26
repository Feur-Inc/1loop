// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

// Notifications go out only while no window has the focus, whichever chat is
// on screen, and never once Settings turned them off (#439).
func TestNotifiesOnlyInTheBackground(t *testing.T) {
	main, other := twoWindows(t)
	for _, test := range []struct{ foreground, off, want bool }{
		{true, false, false},
		{false, false, true},
		{false, true, false},
		{true, true, false},
	} {
		main.inForeground, main.notificationsOff = test.foreground, test.off
		if main.notifies() != test.want || other.notifies() != test.want {
			t.Errorf("foreground %v, off %v: main %v, other window %v, want %v", test.foreground, test.off, main.notifies(), other.notifies(), test.want)
		}
	}
}
