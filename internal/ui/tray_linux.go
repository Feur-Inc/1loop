// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"

	"1loop/internal/platform"
)

const trayWatcher = "org.kde.StatusNotifierWatcher"

// trayHostPresent asks the session bus, as the window closes, whether a
// tray host would show 1loop's icon. Bars and panels come and go, so it is
// asked each time rather than once at start.
func trayHostPresent() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		platform.Devf("ui", "tray: no session bus: %v", err)
		return false
	}
	// The window waits for the answer: a bus that does not answer at once
	// counts as no tray.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var watcher bool
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, trayWatcher).Store(&watcher); err != nil {
		platform.Devf("ui", "tray: %v", err)
		return false
	}
	var host dbus.Variant
	err = conn.Object(trayWatcher, "/StatusNotifierWatcher").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0,
		trayWatcher, "IsStatusNotifierHostRegistered").Store(&host)
	registered, known := host.Value().(bool)
	present := trayHostDecision(watcher, registered, err == nil && known)
	platform.Devf("ui", "tray: watcher %v, host registered %v (known %v): hide %v", watcher, registered, err == nil && known, present)
	return present
}
