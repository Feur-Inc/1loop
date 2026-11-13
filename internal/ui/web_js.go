// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client (cmd/1loop-web, issue #267): the desktop's remote mode
// alone, built for GOOS=js and served by 1loop-server at /app/. It has no
// computer of its own to run agents on, so it skips everything local at
// startup and drives the server the page came from, pairing as the desktop
// does or with the server's setup link. The browser already checked the
// server's certificate, so there is no fingerprint to pin: requests go
// through fetch, which never sees Go's TLS settings.

import (
	"context"
	"strings"
	"syscall/js"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// WebMain runs the browser client.
func WebMain() {
	useEnglishUI()
	a := fyneapp.NewWithID("io.1loop.web")
	a.Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ctx, cancel := context.WithCancel(context.Background())
	backend := &agent.App{Approvals: make(map[string]chan bool)}
	ui := &desktopUI{desktopApp: &desktopApp{backend: backend, ctx: ctx, shutdown: cancel, keys: make(map[string]string),
		home: js.Global().Get("location").Get("origin").String(), serverProjects: loadWebProjects()},
		cfg: agent.Config{Provider: "compatible"}}
	ui.windows = []*desktopUI{ui}
	ui.window = a.NewWindow("1loop")
	ui.window.SetPadded(false)
	ui.followTheme()
	ui.window.SetContent(ui.build())
	ui.prompt.switchDraft(ui.draftKey())
	ui.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { ui.copySelection() })
	ui.prompt.paste = nil // pictures come with the page's paste event (web_attach_js.go)
	ui.listenBrowserFiles()
	ui.updateHeader()
	ui.inForeground = true
	a.Lifecycle().SetOnStarted(func() {
		ui.startFrames()
		ui.watchTextInput()
		ui.linkServers() // listed at once, those that do not answer marked offline
		ui.connectOrigin()
	})
	ui.window.ShowAndRun()
}

// pairOriginAgain pairs with the page's own server again once this browser
// forgot it (server_devices.go).
func (ui *desktopUI) pairOriginAgain() { ui.connectOrigin() }

// connectOrigin uses the token saved for this server, or pairs: at once
// with the server's one-time setup link (#setup=… in the address).
func (ui *desktopUI) connectOrigin() {
	location := js.Global().Get("location")
	origin := location.Get("origin").String()
	setup := ""
	if hash := location.Get("hash").String(); strings.HasPrefix(hash, "#setup=") {
		setup = strings.TrimPrefix(hash, "#setup=")
		// Out of the address bar and the history: it is used up.
		js.Global().Get("history").Call("replaceState", nil, "", location.Get("pathname").String())
	}
	target := serverAddress{URL: origin}
	if saved, ok := pairedServer(origin); ok { // among the servers it linked, not only the last
		target.Token = saved.Token
	}
	client, err := newRemoteClient(target)
	if err != nil {
		ui.notice("Remote server", err.Error())
		return
	}
	status := widget.NewLabel("Connecting to " + origin + "…")
	sheet := ui.modal("1loop", status, 520, nil)
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 10*time.Second)
		defer cancel()
		if target.Token != "" {
			var state map[string]any
			if err := client.json(ctx, "GET", "/api/state", nil, &state); err == nil {
				fyne.Do(func() { sheet.dismiss(); ui.useServer(client) })
				return
			}
			client.address.Token = ""
		}
		var reply struct {
			Pairing, Code            string
			Console, Phone, Approved bool
		}
		err := client.json(ctx, "POST", "/api/pair", map[string]string{"device": "1loop web", "setup": setup}, &reply)
		fyne.Do(func() {
			if err != nil {
				status.SetText(err.Error())
				return
			}
			sheet.dismiss()
			ui.confirmServerPair(client, reply.Pairing, reply.Code, reply.Phone, reply.Approved, ui.useNewServer)
		})
	}()
}
