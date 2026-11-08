// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client (cmd/1loop-web, issue #267): the desktop's remote mode
// alone, built for GOOS=js and served by 1loop-server at /app/. It has no
// computer of its own to run agents on, so it skips everything local at
// startup and drives the server the page came from, pairing from its
// console as the desktop does. The browser already checked the server's
// certificate, so there is no fingerprint to pin: requests go through
// fetch, which never sees Go's TLS settings.

import (
	"context"
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
	ui := &desktopUI{backend: backend, ctx: ctx, shutdown: cancel, keys: make(map[string]string),
		cfg: agent.Config{Provider: "compatible"}}
	ui.window = a.NewWindow("1loop")
	ui.window.SetPadded(false)
	ui.window.SetContent(ui.build())
	ui.prompt.switchDraft(ui.draftKey())
	ui.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { ui.copySelection() })
	ui.prompt.paste = nil // pictures come with the page's paste event (web_attach_js.go)
	ui.listenBrowserFiles()
	ui.updateHeader()
	ui.inForeground = true
	a.Lifecycle().SetOnStarted(func() {
		ui.startFrames()
		ui.connectOrigin()
	})
	ui.window.ShowAndRun()
}

// connectOrigin uses the token saved for this server, or pairs.
func (ui *desktopUI) connectOrigin() {
	origin := js.Global().Get("location").Get("origin").String()
	saved := loadServerAddress()
	target := serverAddress{URL: origin}
	if saved.URL == origin {
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
			Pairing, Code  string
			Console, Phone bool
		}
		err := client.json(ctx, "POST", "/api/pair", map[string]string{"device": "1loop web"}, &reply)
		fyne.Do(func() {
			if err != nil {
				status.SetText(err.Error())
				return
			}
			sheet.dismiss()
			ui.confirmServerPair(client, reply.Pairing, reply.Code, reply.Phone)
		})
	}()
}
