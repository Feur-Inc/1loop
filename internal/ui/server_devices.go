// SPDX-License-Identifier: MPL-2.0

package ui

// Unpairing (#428): Forget on a paired server, in Settings → Remote,
// unpairs this device there (its token stops working) before forgetting the
// server here, and each server's Devices page (Settings → Servers) lists
// the devices paired with it, each with Remove. Both ask first. The desktop and the browser client share
// this; the server's side is internal/server/security.go.

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// serverDevice is a device paired with a server, as GET /api/devices lists
// it; Current is the one asking.
type serverDevice struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Added   time.Time `json:"added"`
	Current bool      `json:"current"`
}

// serverDevices lists the devices paired with a server.
func serverDevices(ctx context.Context, client *remoteClient) ([]serverDevice, error) {
	var listed struct{ Devices []serverDevice }
	err := client.json(ctx, "GET", "/api/devices", nil, &listed)
	return listed.Devices, err
}

// revokeServerDevice unpairs a device from a server.
func revokeServerDevice(ctx context.Context, client *remoteClient, id string) error {
	return client.json(ctx, "POST", "/api/devices/"+url.PathEscape(id)+"/revoke", map[string]any{}, nil)
}

// unpairSelf unpairs this device from a server: it finds itself among the
// paired devices and revokes its own token.
func unpairSelf(ctx context.Context, saved serverAddress) error {
	client, err := newRemoteClient(saved)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	devices, err := serverDevices(ctx, client)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if device.Current {
			return revokeServerDevice(ctx, client, device.ID)
		}
	}
	return nil // already unpaired there
}

// forgetServer asks, then unpairs this device from a server and forgets it.
// A server that does not answer is forgotten here all the same, and the
// user told how to finish on its side.
func (ui *desktopUI) forgetServer(saved serverAddress) {
	name := ui.serverLabel(saved.URL)
	ui.confirm("Forget "+name+"?",
		"This device is unpaired from "+name+": its access stops at once. It must be paired again to use it.",
		"Forget", func() {
			go func() {
				err := unpairSelf(ui.ctx, saved)
				fyne.Do(func() {
					ui.dropServer(saved.URL)
					if err != nil {
						ui.notice("Remote server", fmt.Sprintf("%s is forgotten here; it could not be told (%v). Unpair it there too.", name, err))
					}
				})
			}()
		})
}

// dropServer forgets a server this device is no longer paired with. The
// browser client pairs with its page's own server again at once.
func (ui *desktopUI) dropServer(address string) {
	switch {
	case ui.home != "" && address == ui.home:
		forgetServerAddress(address)
		ui.linkServers()
		ui.pairOriginAgain()
	case ui.home != "":
		ui.unlinkServer(address)
	default:
		if ui.server != nil && ui.server.client.address.URL == address {
			ui.useComputer()
		}
		forgetServerAddress(address)
		ui.linkServers()
	}
	ui.reshowRemoteFor(address)
}

// reshowRemoteFor redraws Settings → Remote when it is on screen, and leaves
// the pages of a server just forgotten for it.
func (ui *desktopUI) reshowRemoteFor(forgotten string) {
	if view := ui.prefs; view != nil && (view.at("Remote") || view.open && forgotten != "" && view.server == forgotten) {
		view.show("Remote")
	}
}

// serverDevicesCard lists the devices paired with a server, each
// with Remove; this device's own Remove forgets the server. A removal
// reloads the list in place, where the page stays scrolled.
func (ui *desktopUI) serverDevicesCard(remote *remoteDesktop) fyne.CanvasObject {
	holder := container.NewStack(card(settingRow("Loading the paired devices…", "", nil)))
	var load func()
	load = func() {
		go func() {
			ctx, cancel := context.WithTimeout(remote.ctx, 10*time.Second)
			defer cancel()
			devices, err := serverDevices(ctx, remote.client)
			fyne.Do(func() {
				if remote.ctx.Err() != nil {
					return
				}
				var rows []fyne.CanvasObject
				if err != nil {
					detail := err.Error()
					if olderServer(err) {
						detail = olderText(remote)
					}
					rows = append(rows, settingRow("The paired devices cannot be listed", detail, nil))
				}
				for _, device := range devices {
					rows = append(rows, ui.serverDeviceRow(remote, device, load))
				}
				if len(rows) == 0 {
					rows = append(rows, settingRow("No paired device", "", nil))
				}
				holder.Objects = []fyne.CanvasObject{card(rows...)}
				holder.Refresh()
			})
		}()
	}
	load()
	return holder
}

// serverDeviceRow is one paired device with Remove, which asks first;
// removed is called once the server unpaired it.
func (ui *desktopUI) serverDeviceRow(remote *remoteDesktop, device serverDevice, removed func()) fyne.CanvasObject {
	title, detail := device.Name, "Paired "+device.Added.Local().Format("2 Jan 2006 15:04")
	if device.Current {
		title += " · this device"
	}
	server := remote.label()
	remove := newRow("Remove", iconTrash, styleChip, func() {
		if device.Current {
			ui.forgetServer(remote.client.address)
			return
		}
		ui.confirm("Remove "+device.Name+"?",
			device.Name+" is unpaired from "+server+": its access stops at once.",
			"Remove", func() {
				go func() {
					ctx, cancel := context.WithTimeout(remote.ctx, 10*time.Second)
					defer cancel()
					err := revokeServerDevice(ctx, remote.client, device.ID)
					fyne.Do(func() {
						if err != nil {
							ui.notice("Remote server", "Unable to remove "+device.Name+": "+err.Error())
						}
						removed()
					})
				}()
			})
	})
	return settingRow(title, detail, remove)
}
