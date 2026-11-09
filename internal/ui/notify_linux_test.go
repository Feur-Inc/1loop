// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestNotificationSignal(t *testing.T) {
	for _, c := range []struct {
		signal  dbus.Signal
		id      string
		clicked bool
	}{
		{dbus.Signal{Name: "org.freedesktop.Notifications.ActionInvoked", Body: []any{uint32(7), "default"}}, "7", true},
		{dbus.Signal{Name: "org.freedesktop.Notifications.NotificationClosed", Body: []any{uint32(7), uint32(2)}}, "7", false},
		{dbus.Signal{Name: "org.freedesktop.portal.Notification.ActionInvoked", Body: []any{"3", "open-chat", []dbus.Variant{}}}, "3", true},
		{dbus.Signal{Name: "org.freedesktop.DBus.NameOwnerChanged", Body: []any{"a", "b", "c"}}, "", false},
		{dbus.Signal{Name: "org.freedesktop.Notifications.ActionInvoked", Body: []any{uint32(7)}}, "", false},
	} {
		if id, clicked := notificationSignal(&c.signal); id != c.id || clicked != c.clicked {
			t.Errorf("%s %v: got %q %v, want %q %v", c.signal.Name, c.signal.Body, id, clicked, c.id, c.clicked)
		}
	}
}
