// SPDX-License-Identifier: MPL-2.0

package ui

// Where the Loopy the app shows lives: this computer, or a paired server.
// The views (loopy_view.go) reach it the same way in both cases.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/loopy"
	"1loop/internal/sse"
)

// loopyCaller is a server's API for Loopy.
type loopyCaller struct{ client *remoteClient }

func (caller loopyCaller) JSON(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return caller.client.json(ctx, method, path, body, out)
}

func (caller loopyCaller) Bytes(ctx context.Context, path string) ([]byte, error) {
	response, err := caller.client.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return io.ReadAll(io.LimitReader(response.Body, 64<<20))
}

func (caller loopyCaller) Upload(ctx context.Context, path string, data []byte, out any) error {
	return caller.client.json(ctx, "POST", path, bytes.NewReader(data), out)
}

// loopyServer is the address of the server the app's Loopy lives on, ""
// for this computer.
func (ui *desktopUI) loopyServer() string {
	if ui.home != "" { // the browser client
		if ui.loopyAt == "" {
			return ui.home
		}
	}
	return ui.loopyAt
}

// loopyClient reaches the app's Loopy. Never on the interface's thread.
func (ui *desktopUI) loopyClient(at string) (loopy.Client, error) {
	if at == "" {
		return ui.localLoopy()
	}
	client, err := ui.loopyRemote(at)
	if err != nil {
		return nil, err
	}
	return loopy.Remote{Caller: loopyCaller{client}}, nil
}

func (ui *desktopUI) loopyRemote(at string) (*remoteClient, error) {
	for _, address := range loadServerAddresses() {
		if address.URL == at && address.Token != "" {
			return newRemoteClient(address)
		}
	}
	if ui.home == at && ui.server != nil {
		return ui.server.client, nil
	}
	return nil, errors.New("The server your Loopy lives on is no longer paired: choose where it lives in Settings → Loopy")
}

// followLoopy listens to the server the app's Loopy lives on, until ctx is
// done.
func (ui *desktopUI) followLoopy(ctx context.Context, at string) {
	for ctx.Err() == nil {
		if client, err := ui.loopyRemote(at); err == nil {
			if response, err := client.request(ctx, "GET", "/api/events", nil); err == nil {
				fyne.Do(func() { ui.loopyChanged(at, loopy.Change{All: true, State: "idle", Version: -1}) }) // what happened meanwhile
				for event, err := range sse.Events(response.Body, 32<<20) {
					if err != nil {
						break
					}
					switch event.Type {
					case "loopy":
						var change loopy.Change
						if json.Unmarshal([]byte(event.Data), &change) == nil {
							fyne.Do(func() { ui.loopyChanged(at, change) })
						}
					case "loopy_notice":
						var notice struct{ Title, Text string }
						if json.Unmarshal([]byte(event.Data), &notice) == nil {
							fyne.Do(func() { ui.loopyNotice(notice.Title, notice.Text) })
						}
					}
				}
				response.Body.Close()
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// setLoopyAt changes where the app's Loopy lives.
func (ui *desktopUI) setLoopyAt(at string) {
	ui.loopyAt = at
	ui.save()
	ui.linkLoopy()
	for _, view := range ui.windows {
		if view.loopy != nil {
			view.loopy.reload()
		}
	}
}

// linkLoopy follows the server Loopy lives on, if it does.
func (ui *desktopUI) linkLoopy() {
	if ui.loopyUnlink != nil {
		ui.loopyUnlink()
		ui.loopyUnlink = nil
	}
	at := ui.loopyServer()
	if at == "" {
		return
	}
	ctx, cancel := context.WithCancel(ui.ctx)
	ui.loopyUnlink = cancel
	go ui.followLoopy(ctx, at)
}

// loopyChanged shows what changed in the Loopy at at.
func (ui *desktopUI) loopyChanged(at string, change loopy.Change) {
	if at != ui.loopyServer() {
		return
	}
	for _, view := range ui.windows {
		if view.loopy != nil {
			view.loopy.apply(change)
		}
	}
}

// loopyNotice is a notification of Loopy's, unless the user is looking at it.
func (ui *desktopUI) loopyNotice(title, text string) {
	if ui.notificationsOffNow() {
		return
	}
	for _, view := range ui.windows {
		if view.inLoopy && ui.inForeground {
			return
		}
	}
	open := func() { ui.windows[0].openLoopy() }
	if sendClickableNotification(title, text, open) {
		return
	}
	fyne.CurrentApp().SendNotification(fyne.NewNotification(title, text))
}

// notificationsOffNow says whether the user turned notifications off.
func (app *desktopApp) notificationsOffNow() bool { return false }
