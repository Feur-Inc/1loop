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
	"fyne.io/fyne/v2/widget"
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

// withLinkedServer is the linked servers with address among them: in
// place of one at the same address, at the end otherwise.
func withLinkedServer(linked []serverAddress, address serverAddress) []serverAddress {
	linked = slices.Clone(linked)
	for i := range linked {
		if linked[i].URL == address.URL {
			linked[i] = address
			return linked
		}
	}
	return append(linked, address)
}

func withoutLinkedServer(linked []serverAddress, url string) []serverAddress {
	return slices.DeleteFunc(slices.Clone(linked), func(address serverAddress) bool { return address.URL == url })
}

// browserServers are the browser client's servers as saved: home's token
// is the page's own pairing.
func (ui *desktopUI) browserServers() []serverAddress {
	home := loadServerAddress()
	if home.URL != ui.home {
		home = serverAddress{URL: ui.home}
	}
	return browserTargets(home, loadLinkedServers())
}

// pairedServer is the pairing saved for a server's address: the desktop's
// one paired server, or one of the browser client's.
func (ui *desktopUI) pairedServer(url string) serverAddress {
	if ui.home == "" {
		return loadServerAddress()
	}
	for _, address := range ui.browserServers() {
		if address.URL == url {
			return address
		}
	}
	return serverAddress{}
}

// follows is whether link is one the window follows now.
func (ui *desktopUI) follows(link *serverLink) bool {
	return ui.link == link || slices.Contains(ui.webLinks, link)
}

// linkTo is the link that follows a server, nil when none does.
func (ui *desktopUI) linkTo(url string) *serverLink {
	if ui.link != nil && ui.link.address.URL == url {
		return ui.link
	}
	for _, link := range ui.webLinks {
		if link.address.URL == url {
			return link
		}
	}
	return nil
}

// linkWebServers follows each of the browser client's paired servers, and
// stops following one it unlinked.
func (ui *desktopUI) linkWebServers() {
	ui.followServers(ui.browserServers())
}

// followServers makes the browser client's links those of addresses, in
// their order: a link already following one at the same address and token
// goes on, the others start, and those left are stopped.
func (ui *desktopUI) followServers(addresses []serverAddress) {
	links := []*serverLink{}
	for _, address := range addresses {
		if address.Token == "" { // home, not paired yet
			continue
		}
		if link := ui.linkTo(address.URL); link != nil && link != ui.link && link.address == address {
			links = append(links, link)
			continue
		}
		client, err := newRemoteClient(address)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithCancel(ui.ctx)
		link := &serverLink{address: address, cancel: cancel, wake: make(chan struct{}, 1)}
		links = append(links, link)
		go ui.followServer(ctx, link, client)
	}
	for _, old := range ui.webLinks {
		if !slices.Contains(links, old) && old.cancel != nil {
			old.cancel()
		}
	}
	ui.webLinks = links
	if ui.projectList != nil {
		ui.renderChats()
	}
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
			unlink = append(unlink, menuAction{label: "Unlink " + serverName(url), icon: iconTrash, danger: true, run: func() { ui.unlinkServer(url) }})
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

// linkAnotherServer pairs the browser client with another 1loop server,
// which the sidebar lists from then on beside the page's own.
func (ui *desktopUI) linkAnotherServer() {
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
		if url == ui.home || slices.ContainsFunc(loadLinkedServers(), func(have serverAddress) bool { return have.URL == url && have.Token != "" }) {
			status.SetText(serverName(url) + " is linked already.")
			return
		}
		link.Disable()
		status.SetText("Connecting to " + url + "…")
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 10*time.Second)
			defer cancel()
			var reply struct {
				Pairing, Code  string
				Console, Phone bool
			}
			err := client.json(ctx, "POST", "/api/pair", map[string]string{"device": "1loop web"}, &reply)
			fyne.Do(func() {
				link.Enable()
				if err != nil {
					status.SetText(linkProblem(url, err))
					return
				}
				sheet.dismiss()
				ui.confirmServerPair(client, reply.Pairing, reply.Code, reply.Phone, false, ui.linkPairedServer)
			})
		}()
	}
	address.OnSubmitted = func(string) { link.onTap() }
	sheet = ui.modal("Link a server", container.NewVBox(
		note("Another 1loop server's projects and chats are listed in the sidebar beside this one's, and its chats run there. The next step asks you to approve this page from a device already paired with it, or in its console."),
		widget.NewLabel("HTTPS address"), address,
		note("This browser must trust its certificate: if it is self-signed, open its address in a tab once and accept it first."),
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
	if err := saveLinkedServers(withLinkedServer(loadLinkedServers(), client.address)); err != nil {
		return err
	}
	ui.linkWebServers()
	return nil
}

// unlinkServer forgets a linked server: the window comes back to the
// page's own first if it worked on it.
func (ui *desktopUI) unlinkServer(url string) {
	saveLinkedServers(withoutLinkedServer(loadLinkedServers(), url))
	if ui.server != nil && ui.server.client.address.URL == url {
		ui.reachServer(serverTarget(ui.home), func(*remoteDesktop) {})
	}
	delete(ui.serverProjects, url)
	saveWebProjects(ui.serverProjects)
	ui.linkWebServers()
}
