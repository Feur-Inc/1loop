package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/extensions"
	"1loop/internal/githubconnector"
)

func (ui *desktopUI) connectorSettings() fyne.CanvasObject {
	state := githubconnector.Default.Status()
	status := "Not connected. GitHub tools are not offered to the model."
	if state.Connected {
		status = "Connected as @" + state.Login
		if state.Enabled {
			status += " · GitHub tools enabled"
		} else {
			status += " · tools disabled"
		}
	}
	body := container.NewVBox(ui.prefs.pageTitle("Connectors", "", nil),
		sectionTitle("GitHub"), note("Connect the GitHub account whose repositories the agent may use. Tools come from GitHub's official MCP server and follow your approval mode. Signing in does not authorize unrelated changes."),
		card(settingRow("GitHub account", status, nil)))
	refresh := func() { ui.prefs.show("Connectors") }
	if state.Error != "" {
		body.Add(note(state.Error))
	}
	if !state.Configured {
		id := widget.NewEntry()
		id.PlaceHolder = "Public GitHub App client ID"
		body.Add(sectionTitle("Publisher setup"))
		body.Add(note("This build has no GitHub App configured yet. The publisher registers one GitHub App with Device flow enabled; users then sign in to that app. Enter its public client ID, never an app secret or private key."))
		body.Add(id)
		body.Add(newRow("Save client ID", nil, stylePrimary, func() {
			if err := githubconnector.Default.Configure(id.Text); err != nil {
				ui.notice("GitHub", err.Error())
				return
			}
			refresh()
		}))
		body.Add(newRow("Register a GitHub App", nil, styleOutline, func() { openLink("https://github.com/settings/apps/new") }))
		return body
	}
	if state.Connected {
		body.Add(card(settingRow("Enable GitHub tools", "Turning this off removes GitHub tools from subsequent model requests and rejects calls made after access is disabled.", newToggle(state.Enabled, func(on bool) {
			if err := githubconnector.Default.Enable(on); err != nil {
				ui.notice("GitHub", err.Error())
			}
			if !on {
				extensions.Servers.Forget(githubconnector.ServerName)
			}
			refresh()
		}))))
		body.Add(newRow("Review access on GitHub", nil, styleOutline, func() { openLink("https://github.com/settings/apps/authorizations") }))
		body.Add(newRow("Manage repository access", nil, styleOutline, func() { openLink(state.InstallURL) }))
		body.Add(newRow("Disconnect", nil, styleOutline, func() {
			if err := githubconnector.Default.Disconnect(); err != nil {
				ui.notice("GitHub", err.Error())
				return
			}
			extensions.Servers.Forget(githubconnector.ServerName)
			refresh()
		}))
		body.Add(note("Disconnect removes 1loop's local authorization. You can also revoke the app in your GitHub settings. Credentials stay on this execution target; they are not sent to the model."))
		return body
	}
	body.Add(newRow("Choose repositories on GitHub", nil, styleOutline, func() { openLink(state.InstallURL) }))
	body.Add(newRow("Connect GitHub…", nil, stylePrimary, ui.connectGitHub))
	body.Add(note("Authorize the account in your browser. Review the app's access and choose which repositories to grant on GitHub. The sign-in code is shown only in this settings window."))
	return body
}

func (ui *desktopUI) connectGitHub() {
	if state := githubconnector.Default.Status(); state.Code != "" || state.Error != "" {
		githubconnector.Default.Cancel()
	}
	message := widget.NewLabel("Preparing GitHub sign-in…")
	message.Wrapping = fyne.TextWrapWord
	code := widget.NewLabel("")
	var dialog *sheet
	open := newRow("Open GitHub", nil, stylePrimary, func() {
		if address := githubconnector.Default.Status().URL; address != "" {
			openLink(address)
		}
	})
	open.Disable()
	copy := newRow("Copy code", nil, styleOutline, func() { ui.window.Clipboard().SetContent(githubconnector.Default.Status().Code) })
	copy.Disable()
	cancel := func() { githubconnector.Default.Cancel() }
	dialog = ui.modal("Connect GitHub", container.NewVBox(message, code), 520, cancel,
		newRow("Cancel", nil, styleOutline, func() { cancel(); dialog.dismiss() }), copy, open)
	go func() {
		err := githubconnector.Default.Start(ui.ctx)
		for {
			state := githubconnector.Default.Status()
			if err != nil || state.Error != "" || state.Connected {
				fyne.Do(func() {
					if !dialog.open {
						return
					}
					dialog.dismiss()
					if err != nil {
						ui.notice("GitHub sign-in", err.Error())
					} else if state.Error != "" {
						ui.notice("GitHub sign-in", state.Error)
					}
					ui.prefs.show("Connectors")
				})
				return
			}
			fyne.Do(func() {
				if !dialog.open {
					cancel()
					return
				}
				message.SetText("Open GitHub in your browser, enter this code and review the requested access. This code expires in a few minutes.")
				code.SetText(state.Code)
				open.Enable()
				copy.Enable()
			})
			if state.Code == "" {
				return
			}
			select {
			case <-ui.ctx.Done():
				cancel()
				return
			case <-time.After(time.Second):
			}
		}
	}()
}
