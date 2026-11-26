// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client's targets (#382): the server its page came from, its
// home, and the other 1loop servers it linked. Each is followed by a link
// as the desktop follows its paired server (target_link.go), and the
// sidebar lists every one's projects and chats together: home's with no
// icon, the others' with the server icon, one that does not answer marked
// offline with the projects it listed last.
//
// The page reaches another server from its own origin: that server lets it
// (internal/server/cors.go), and the browser must trust its certificate,
// which a page cannot pin. A self-signed one is accepted once by opening
// the server's address in a tab (#332).

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	registry "1loop/internal/settings"
)

// browserTargets are the browser client's servers in the sidebar's order:
// home first, then those it linked, each once.
func browserTargets(home serverAddress, linked []serverAddress) []serverAddress {
	var targets []serverAddress
	if home.URL != "" {
		targets = append(targets, home)
	}
	for _, address := range linked {
		if address.URL != "" && !slices.ContainsFunc(targets, func(have serverAddress) bool { return have.URL == address.URL }) {
			targets = append(targets, address)
		}
	}
	return targets
}

// browserServers are the browser client's servers: the page's own first,
// with its token once paired, then the others it linked. All are kept in
// the same pairings as the desktop's (remote_pairings.go).
func (ui *desktopUI) browserServers() []serverAddress {
	home, ok := pairedServer(ui.home)
	if !ok {
		home = serverAddress{URL: ui.home}
	}
	return browserTargets(home, loadServerAddresses())
}

// rememberWebProjects keeps each of the browser client's servers' projects
// in the page's storage, for a visit when it does not answer.
func (ui *desktopUI) rememberWebProjects(address string, projects []string) {
	kept := map[string][]string{}
	for _, server := range ui.browserServers() {
		if listed, ok := ui.serverProjects[server.URL]; ok {
			kept[server.URL] = listed
		}
	}
	kept[address] = slices.Clone(projects)
	if maps.EqualFunc(kept, ui.serverProjects, slices.Equal) {
		return
	}
	ui.serverProjects = kept
	saveWebProjects(kept)
}

// browserTargetMenu is the target chip's menu in the browser client: its
// servers, the one the window works on checked, and linking another.
func (ui *desktopUI) browserTargetMenu() {
	var actions, unlink []menuAction
	for _, address := range ui.browserServers() {
		url := address.URL
		inUse := ui.server != nil && ui.server.client.address.URL == url
		detail := url
		if url == ui.home {
			detail = "This page's server"
		}
		if ui.reaches[serverTarget(url).key()].state == reachOffline {
			detail += " · " + offlineMark
		}
		actions = append(actions, menuAction{label: serverName(url), detail: detail, icon: iconBrowser, checked: inUse, run: func() {
			if !inUse {
				ui.reachServer(serverTarget(url), func(*remoteDesktop) {})
			}
		}})
		if url != ui.home {
			unlink = append(unlink, menuAction{label: "Unlink " + serverName(url), icon: iconTrash, danger: true, run: func() { ui.askUnlink(url) }})
		}
	}
	actions = append(actions, menuAction{label: "Link a server…", icon: iconPlus, run: ui.linkAnotherServer})
	actions = append(actions, unlink...)
	if remote := ui.server; remote != nil {
		actions = append(actions, menuAction{label: "Server hooks…", icon: iconTerminal, run: remote.editHooks})
		if offer := remote.update; offer != nil && offer.Available && offer.CanUpdate {
			actions = append(actions, menuAction{label: "Update the server to " + offer.Latest, icon: iconArrowTurn, run: remote.updateServer})
		}
	}
	showMenu(ui.window, ui.machine, actions)
}

// linkAnotherServer asks first whether the other server runs already, and
// guides its setup when it does not (server_setup.go, #443).
func (ui *desktopUI) linkAnotherServer() {
	ui.serverSetup("Link a server", ui.linkServerAddress)
}

// linkServerAddress pairs the browser client with another 1loop server,
// which the sidebar lists from then on beside the page's own.
func (ui *desktopUI) linkServerAddress() {
	address := widget.NewEntry()
	address.SetPlaceHolder("https://192.168.1.20:47101")
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var sheet *sheet
	link := newRow("Link", nil, stylePrimary, nil)
	link.onTap = func() {
		client, err := newRemoteClient(serverAddress{URL: address.Text})
		if err != nil {
			status.SetText(err.Error())
			return
		}
		url := client.address.URL
		if _, linked := pairedServer(url); url == ui.home || linked {
			status.SetText(serverName(url) + " is linked already.")
			return
		}
		link.Disable()
		status.SetText("Connecting to " + url + "…")
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 10*time.Second)
			defer cancel()
			var reply struct {
				Pairing, Code, Command string
				Console, Phone         bool
			}
			err := client.json(ctx, "POST", "/api/pair", map[string]string{"device": "1loop web"}, &reply)
			fyne.Do(func() {
				link.Enable()
				if err != nil {
					status.SetText(linkProblem(url, err))
					return
				}
				sheet.dismiss()
				ui.confirmServerPair(client, reply.Pairing, reply.Code, reply.Command, reply.Phone, false, ui.linkPairedServer)
			})
		}()
	}
	address.OnSubmitted = func(string) { link.onTap() }
	sheet = ui.modal("Link a server", container.NewVBox(
		note("Its chats show in the sidebar. Approve it on a paired device."),
		widget.NewLabel("HTTPS address"), address,
		note("This browser must trust the certificate: open its address once."),
		status), 620, nil, link)
	ui.window.Canvas().Focus(address)
}

// linkProblem says why a server could not be linked; a page that cannot
// reach it at all is most often refused its certificate.
func linkProblem(url string, err error) string {
	text := err.Error()
	if strings.Contains(text, "fetch") || strings.Contains(text, "NetworkError") {
		text += ". If its certificate is self-signed, open " + url + " in a tab and accept it, then try again."
	}
	return text
}

// linkPairedServer keeps a server the browser client just paired with,
// and follows it.
func (ui *desktopUI) linkPairedServer(client *remoteClient) error {
	if err := saveServerAddress(client.address); err != nil {
		return err
	}
	ui.linkServers()
	return nil
}

// askUnlink asks, then unlinks a server the browser client linked (#443).
func (ui *desktopUI) askUnlink(url string) {
	name := serverName(url)
	ui.confirm("Unlink "+name+"?",
		name+" is forgotten in this browser: its projects and chats leave the sidebar, and linking it again needs a new pairing.",
		"Unlink", func() {
			ui.unlinkServer(url)
			ui.reshowRemote()
		})
}

// browserRemoteSettings is the browser client's Settings → Remote (#443),
// the desktop's parts that a page has: the servers it works on, and the
// devices paired with the one in use. SSH machines and the phone app are a
// computer's.
func (ui *desktopUI) browserRemoteSettings() fyne.CanvasObject {
	view := ui.prefs
	var rows []fyne.CanvasObject
	for _, address := range ui.browserServers() {
		url := address.URL
		detail := url
		if url == ui.home {
			detail = "This page's server"
		}
		if ui.reaches[serverTarget(url).key()].state == reachOffline {
			detail += " · " + offlineMark
		}
		var control fyne.CanvasObject = newRow("Use", nil, styleOutline, func() {
			ui.reachServer(serverTarget(url), func(*remoteDesktop) { ui.reshowRemote() })
		})
		if ui.server != nil && ui.server.client.address.URL == url {
			control = inUse()
		}
		if url != ui.home {
			control = container.New(layout.NewCustomPaddedHBoxLayout(10), control, newRow("Unlink", iconTrash, styleChip, func() { ui.askUnlink(url) }))
		}
		rows = append(rows, settingRow(serverName(url), detail, control))
	}
	rows = append(rows, settingRow("Link a server", "Another 1loop server's projects and chats are listed in the sidebar beside this one's, and its chats run there.",
		newRow("Link a server…", iconPlus, styleOutline, ui.linkAnotherServer)))
	page := container.NewVBox(view.pageTitle("Remote", "", nil), view.intro(registry.Remote),
		sectionTitle(registry.GroupTargets),
		partNote("The 1loop servers this page works on: its own, and those it linked. The target button under the conversation switches between them too."),
		view.mark("general.target", card(rows...)),
		sectionTitle(registry.GroupDevices),
		partNote("The phones and browsers paired with the server in use, this one included. Removing one stops its access at once: it must be paired again to come back."))
	if ui.server != nil {
		page.Add(partTitle("Paired with " + serverName(ui.server.client.address.URL)))
		page.Add(view.mark("phone.pairing", ui.serverDevicesCard(ui.server)))
	}
	return page
}

// unlinkServer forgets a linked server: the window comes back to the
// page's own first if it worked on it.
func (ui *desktopUI) unlinkServer(url string) {
	forgetServerAddress(url)
	if ui.server != nil && ui.server.client.address.URL == url {
		ui.reachServer(serverTarget(ui.home), func(*remoteDesktop) {})
	}
	delete(ui.serverProjects, url)
	saveWebProjects(ui.serverProjects)
	ui.linkServers()
}
