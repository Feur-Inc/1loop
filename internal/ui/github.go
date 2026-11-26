// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/githubconnector"
)

// connectorSettings is the Connectors page of a GitHub connector, under its
// title.
func (ui *desktopUI) connectorSettings(source githubSource, title fyne.CanvasObject) fyne.CanvasObject {
	content := container.NewStack(note("Loading…"))
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		defer cancel()
		state, err := source.status(ctx)
		fyne.Do(func() {
			if err != nil {
				state.Error = err.Error()
			}
			content.Objects = []fyne.CanvasObject{ui.githubSettings(source, state)}
			content.Refresh()
		})
	}()
	return container.NewVBox(title, content)
}

// githubChange runs a change of the connector, then shows its page again.
func (ui *desktopUI) githubChange(source githubSource, run func(context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		defer cancel()
		err := run(ctx)
		fyne.Do(func() {
			if err != nil {
				ui.notice("GitHub", err.Error())
			}
			ui.prefs.show(source.page())
		})
	}()
}

func (ui *desktopUI) githubSettings(source githubSource, state githubconnector.Status) fyne.CanvasObject {
	status := "Not connected. GitHub tools are not offered to the model."
	if state.Connected {
		status = "Connected as @" + state.Login
		if state.Enabled {
			status += " · GitHub tools enabled"
		} else {
			status += " · tools disabled"
		}
	}
	target := source.where()
	body := container.NewVBox(
		sectionTitle("GitHub"), note("Connect the GitHub account the agent may use."),
		card(settingRow("GitHub account", status, nil)))
	if state.Error != "" {
		body.Add(note(state.Error))
	}
	if !state.Configured {
		id := widget.NewEntry()
		id.PlaceHolder = "Public GitHub App client ID"
		body.Add(sectionTitle("Publisher setup"))
		body.Add(note("No GitHub App in this build. Enter its public client ID only."))
		body.Add(id)
		body.Add(newRow("Save client ID", nil, stylePrimary, func() {
			clientID := id.Text
			ui.githubChange(source, func(ctx context.Context) error { return source.configure(ctx, clientID) })
		}))
		body.Add(newRow("Register a GitHub App", nil, styleOutline, func() { openLink("https://github.com/settings/apps/new") }))
		return body
	}
	if state.Connected {
		body.Add(card(settingRow("Enable GitHub tools", "Off removes GitHub tools from the model's requests.", newToggle(state.Enabled, func(on bool) {
			ui.githubChange(source, func(ctx context.Context) error { return source.enable(ctx, on) })
		}))))
		body.Add(newRow("Review access on GitHub", nil, styleOutline, func() { openLink("https://github.com/settings/apps/authorizations") }))
		body.Add(newRow("Manage repository access", nil, styleOutline, func() { openLink(state.InstallURL) }))
		for _, object := range ui.githubAttachmentSettings(source, state, target) {
			body.Add(object)
		}
		body.Add(newRow("Disconnect", nil, styleOutline, func() { ui.githubChange(source, source.disconnect) }))
		body.Add(note("Disconnect removes 1loop's authorization on " + target + ". Keys never reach the model."))
		return body
	}
	body.Add(newRow("Choose repositories on GitHub", nil, styleOutline, func() { openLink(state.InstallURL) }))
	body.Add(newRow("Connect GitHub…", nil, stylePrimary, func() { ui.connectGitHub(source, state) }))
	body.Add(note("Authorize in your browser, and pick the repositories there."))
	return body
}

func (ui *desktopUI) connectGitHub(source githubSource, current githubconnector.Status) {
	message := widget.NewLabel("Preparing GitHub sign-in…")
	message.Wrapping = fyne.TextWrapWord
	code := widget.NewLabel("")
	var dialog *sheet
	var shown githubconnector.Status // the attempt on screen
	open := newRow("Open GitHub", nil, stylePrimary, func() {
		if shown.URL != "" {
			openLink(shown.URL)
		}
	})
	open.Disable()
	copy := newRow("Copy code", nil, styleOutline, func() { fyne.CurrentApp().Clipboard().SetContent(shown.Code) })
	copy.Disable()
	cancel := func() {
		go func() {
			ctx, done := context.WithTimeout(ui.ctx, 15*time.Second)
			defer done()
			source.cancel(ctx)
		}()
	}
	dialog = ui.modal("Connect GitHub", container.NewVBox(message, code), 520, cancel,
		newRow("Cancel", nil, styleOutline, func() { cancel(); dialog.dismiss() }), copy, open)
	go func() {
		ctx, done := context.WithTimeout(ui.ctx, 30*time.Second)
		if current.Code != "" || current.Error != "" {
			source.cancel(ctx)
		}
		err := source.start(ctx)
		done()
		for {
			ctx, done := context.WithTimeout(ui.ctx, 15*time.Second)
			state, problem := source.status(ctx)
			done()
			if problem != nil && err == nil {
				err = problem
			}
			if err != nil || state.Error != "" || state.Connected {
				fyne.Do(func() {
					if !dialog.shown() {
						return
					}
					dialog.dismiss()
					if err != nil {
						ui.notice("GitHub sign-in", err.Error())
					} else if state.Error != "" {
						ui.notice("GitHub sign-in", state.Error)
					}
					ui.prefs.show(source.page())
				})
				return
			}
			fyne.Do(func() {
				if !dialog.shown() {
					cancel()
					return
				}
				shown = state
				message.SetText("Open GitHub, enter this code, and review the access.")
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

// githubAttachmentSettings: GitHub refuses the app's sign-in for images and
// videos, so they need a personal access token or GitHub CLI's sign-in.
func (ui *desktopUI) githubAttachmentSettings(source githubSource, state githubconnector.Status, target string) []fyne.CanvasObject {
	detail := "Not set up: add a token, or sign in to GitHub CLI."
	switch {
	case state.AttachmentLogin != "":
		detail = "Personal access token of @" + state.AttachmentLogin
	case state.GitHubCLI:
		detail = "GitHub CLI is installed: uploads use its sign-in."
	}
	objects := []fyne.CanvasObject{sectionTitle("Images and videos"),
		note("Uploads need a token or GitHub CLI, and your approval."),
		card(settingRow("Uploads as", detail, nil))}
	save := func(token string) {
		ui.githubChange(source, func(ctx context.Context) error { return source.setAttachmentToken(ctx, token) })
	}
	if state.AttachmentLogin != "" {
		return append(objects, newRow("Remove token", nil, styleOutline, func() { save("") }))
	}
	token := widget.NewPasswordEntry()
	token.SetPlaceHolder("Personal access token (classic with repo scope, or fine-grained with write access)")
	return append(objects, token,
		newRow("Save token", nil, stylePrimary, func() {
			if strings.TrimSpace(token.Text) != "" {
				save(token.Text)
			}
		}),
		newRow("Create a token on GitHub", nil, styleOutline, func() {
			openLink("https://github.com/settings/tokens/new?scopes=repo&description=1loop+attachments")
		}),
		note("Kept in a private file on "+target+", never sent to the model."))
}
