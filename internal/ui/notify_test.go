// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

// No notification while the window has the focus, nor when they are off.
func TestNotifies(t *testing.T) {
	for _, test := range []struct {
		foreground, off, want bool
	}{
		{false, false, true},
		{true, false, false},
		{false, true, false},
		{true, true, false},
	} {
		ui, _ := twoWindows(t)
		ui.inForeground, ui.notificationsOff = test.foreground, test.off
		if got := ui.notifies(); got != test.want {
			t.Errorf("foreground %v, off %v: notifies = %v, want %v", test.foreground, test.off, got, test.want)
		}
	}
}
