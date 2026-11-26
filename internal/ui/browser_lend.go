// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// Lending this computer's browser to a paired server (issue #406): a
// server with no Chromium may run Browser Use in a headless Chromium here
// (internal/browser/lend.go). It is off unless the user turns it on for
// that server, on its Installs page in Settings. It lasts as long
// as the server's link (target_link.go). While the server's agent uses the
// browser, a bar above the composer says so, with Stop.

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/browser"
)

// lendSession lends the browser to one server.
type lendSession struct {
	address  serverAddress
	cancel   context.CancelFunc
	refs     int  // the links that want it
	attached bool // the server took the offer
	inUse    bool // its agent uses the browser now
	problem  string
}

// lendings are the servers the browser is lent to, by address, and
// lendViews the windows that show it. Both are the UI thread's.
var (
	lendings  = map[string]*lendSession{}
	lendViews = map[*desktopUI]bool{}
)

// lendBrowser lends the browser to a server until ctx ends, if the user
// lets that server use it.
func lendBrowser(ctx context.Context, address serverAddress, client *remoteClient) {
	if !address.LendBrowser {
		return
	}
	session := lendings[address.URL]
	if session != nil && session.address != address { // paired again since
		session.cancel()
		session = nil
	}
	if session == nil {
		lent, cancel := context.WithCancel(context.Background())
		session = &lendSession{address: address, cancel: cancel}
		lendings[address.URL] = session
		var settings *tls.Config
		if transport, ok := client.http.Transport.(*http.Transport); ok {
			settings = transport.TLSClientConfig
		}
		go session.run(lent, browser.Lending{URL: address.URL, Token: address.Token, TLS: settings})
	}
	session.refs++
	context.AfterFunc(ctx, func() {
		fyne.Do(func() {
			if session.refs--; session.refs > 0 {
				return
			}
			session.cancel()
			if lendings[address.URL] == session {
				delete(lendings, address.URL)
			}
			showLending()
		})
	})
}

// run keeps the offer open, connecting again when the connection ends.
func (session *lendSession) run(ctx context.Context, lending browser.Lending) {
	lending.OnAttach = func() { session.update(func() { session.attached, session.problem = true, "" }) }
	lending.OnUse = func(on bool) { session.update(func() { session.inUse = on }) }
	wait := 2 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := lending.Run(ctx)
		if ctx.Err() != nil {
			break
		}
		next := wait
		switch {
		case errors.Is(err, browser.ErrLendUnsupported):
			next = 10 * time.Minute
		case time.Since(started) > time.Minute: // it was up: try again soon
			wait, next = 2*time.Second, 2*time.Second
		default:
			wait = min(2*wait, time.Minute)
		}
		session.update(func() { session.attached, session.inUse, session.problem = false, false, err.Error() })
		select {
		case <-ctx.Done():
		case <-time.After(next):
		}
	}
	session.update(func() { session.attached, session.inUse = false, false })
}

func (session *lendSession) update(change func()) {
	fyne.Do(func() {
		change()
		showLending()
	})
}

// showLending brings every window's bar and status up to date.
func showLending() {
	for ui := range lendViews {
		ui.renderLending()
	}
}

// lendersInUse are the hosts of the servers whose agent uses the browser now.
func lendersInUse() []string {
	var hosts []string
	for _, session := range lendings {
		if session.inUse {
			hosts = append(hosts, serverName(session.address.URL))
		}
	}
	slices.Sort(hosts)
	return hosts
}

// lendBarText is what the bar says while servers use the browser.
func lendBarText(hosts []string) string {
	return "The server's agent is using this computer's browser (" + strings.Join(hosts, ", ") + ")"
}

// lendStatusText is the state of lending the browser to a server.
func lendStatusText(address string) string {
	session := lendings[address]
	switch {
	case session == nil:
		if saved, _ := pairedServer(address); saved.LendBrowser {
			return "Connecting…"
		}
		return "Off: the server's agent cannot use this computer's browser."
	case session.inUse:
		return "The server's agent is using this computer's browser now."
	case session.attached:
		return "Ready: the server's agent uses it when it browses, while the server has no browser of its own."
	case session.problem != "":
		return "Not connected: " + session.problem
	}
	return "Connecting…"
}

// buildLendBar is the bar above the composer while a server's agent uses
// the browser.
func (ui *desktopUI) buildLendBar() fyne.CanvasObject {
	ui.lendText = plainFlow("", 13, colText)
	stop := newRow("Stop", nil, styleOutline, ui.stopLending)
	bar := surface(container.NewBorder(nil, nil, nil, container.NewCenter(stop), container.NewVBox(ui.lendText)), colRaised, colAmber, 10, 8)
	ui.lendBar = container.NewStack(padded(0, 8, 4, 4, bar))
	ui.lendBar.Hide()
	lendViews[ui] = true
	ui.renderLending()
	return ui.lendBar
}

func (ui *desktopUI) renderLending() {
	if ui.lendBar != nil {
		if hosts := lendersInUse(); len(hosts) > 0 {
			setFlow(ui.lendText, lendBarText(hosts))
			ui.lendBar.Show()
		} else {
			ui.lendBar.Hide()
		}
	}
	if ui.lendStatus != nil && ui.lendAddress != "" {
		setFlow(ui.lendStatus, lendStatusText(ui.lendAddress))
	}
}

// stopLending is the bar's Stop: the servers using the browser stop being
// let, their browsers close, and the setting stays off until turned on again.
func (ui *desktopUI) stopLending() {
	for address, session := range lendings {
		if !session.inUse {
			continue
		}
		saveLendBrowser(address, false)
		session.cancel()
		delete(lendings, address)
	}
	ui.linkServers() // the links follow the setting now
	showLending()
}

// lendRows are the switch, on a paired server's Installs page in
// Settings, that lends it this computer's browser.
func (ui *desktopUI) lendRows(address string) fyne.CanvasObject {
	saved, _ := pairedServer(address)
	ui.lendStatus, ui.lendAddress = plainFlow("", 12.5, colMuted), address
	toggle := newToggle(saved.LendBrowser, func(on bool) {
		if err := saveLendBrowser(address, on); err != nil {
			setFlow(ui.lendStatus, err.Error())
			return
		}
		ui.linkServers() // the server's link starts or stops lending
		showLending()
	})
	row := settingRow("Let this server use this computer's browser",
		"For Browser Use on a server without Chromium. Its agent browses in a headless Chromium on this computer, started for it, with a profile of its own: not your browser's cookies or logins. Its pages load from this computer's network and address. It cannot open this computer's files. A bar shows while the agent uses it, with Stop.",
		toggle)
	ui.renderLending()
	return container.NewVBox(sectionTitle("This computer's browser"), card(row, padded(0, 13, 16, 16, ui.lendStatus)))
}
