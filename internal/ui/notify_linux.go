// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os"
	"strconv"
	"sync"

	"fyne.io/fyne/v2"
	"github.com/godbus/dbus/v5"

	"1loop/internal/platform"
)

// notifications is the bus connection notifications are sent and clicked
// on. It is 1loop's own: the shared session connection hands every signal
// to every listener, and fyne.io/systray stops keeping its icon registered
// at the first signal it does not expect.
var notifications struct {
	sync.Mutex
	conn   *dbus.Conn
	failed bool
	clicks map[string]func() // by notification id
}

// sendClickableNotification shows a notification whose click runs click on
// the interface's thread. False when there is no session bus.
func sendClickableNotification(title, body string, click func()) bool {
	conn := notificationBus()
	if conn == nil {
		return false
	}
	flatpak := os.Getenv("ONELOOP_FLATPAK") != ""
	go func() {
		var id string
		if flatpak {
			// The portal shows the application's own icon.
			id = strconv.FormatUint(portalNotification.Add(1), 10)
			onNotificationClick(id, click)
			err := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").Call("org.freedesktop.portal.Notification.AddNotification", 0, id, map[string]dbus.Variant{
				"title":          dbus.MakeVariant(title),
				"body":           dbus.MakeVariant(body),
				"default-action": dbus.MakeVariant("app.open-chat"),
			}).Err
			if err != nil {
				platform.Devf("app", "notification portal: %v", err)
			}
			return
		}
		var number uint32
		err := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").Call("org.freedesktop.Notifications.Notify", 0,
			"1loop", uint32(0), "1loop", title, body, []string{"default", "Open"},
			map[string]dbus.Variant{"desktop-entry": dbus.MakeVariant("1loop")}, int32(-1)).Store(&number)
		if err != nil {
			platform.Devf("app", "notification: %v", err)
			return
		}
		onNotificationClick(strconv.FormatUint(uint64(number), 10), click)
	}()
	return true
}

func onNotificationClick(id string, click func()) {
	notifications.Lock()
	notifications.clicks[id] = click
	notifications.Unlock()
}

// notificationBus connects once, and listens for clicks on 1loop's
// notifications from then on.
func notificationBus() *dbus.Conn {
	notifications.Lock()
	defer notifications.Unlock()
	if notifications.conn != nil || notifications.failed {
		return notifications.conn
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		platform.Devf("app", "notifications: no session bus: %v", err)
		notifications.failed = true
		return nil
	}
	for _, match := range [][]dbus.MatchOption{
		{dbus.WithMatchInterface("org.freedesktop.Notifications"), dbus.WithMatchMember("ActionInvoked")},
		{dbus.WithMatchInterface("org.freedesktop.Notifications"), dbus.WithMatchMember("NotificationClosed")},
		{dbus.WithMatchInterface("org.freedesktop.portal.Notification"), dbus.WithMatchMember("ActionInvoked")},
	} {
		if err := conn.AddMatchSignal(match...); err != nil {
			platform.Devf("app", "notifications: %v", err)
		}
	}
	notifications.conn = conn
	notifications.clicks = map[string]func(){}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go func() {
		for signal := range signals {
			id, clicked := notificationSignal(signal)
			if id == "" {
				continue
			}
			notifications.Lock()
			click := notifications.clicks[id]
			delete(notifications.clicks, id)
			notifications.Unlock()
			if clicked && click != nil {
				fyne.Do(click)
			}
		}
	}()
	return conn
}

// notificationSignal reads which notification a signal is about, and
// whether it was clicked (rather than closed).
func notificationSignal(signal *dbus.Signal) (id string, clicked bool) {
	if len(signal.Body) < 2 {
		return "", false
	}
	switch signal.Name {
	case "org.freedesktop.Notifications.ActionInvoked", "org.freedesktop.Notifications.NotificationClosed":
		number, ok := signal.Body[0].(uint32)
		if !ok {
			return "", false
		}
		return strconv.FormatUint(uint64(number), 10), signal.Name == "org.freedesktop.Notifications.ActionInvoked"
	case "org.freedesktop.portal.Notification.ActionInvoked":
		id, _ := signal.Body[0].(string)
		return id, true
	}
	return "", false
}
