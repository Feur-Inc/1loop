// SPDX-License-Identifier: MPL-2.0

package ui

// Install Chromium and Install Claude Code on a server without them (the
// slim Docker image, issue #406), in Settings → General of the desktop
// connected to it and of the browser client. The server downloads them
// into its data folder (internal/server/install.go); this page starts the
// install and follows it in /api/state.

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// serverInstall is /api/state's part about what the server can install.
type serverInstall struct {
	Browser    *bool // nil: a server from before #406, which always has it
	ClaudeCode *bool
	Installs   map[string]struct {
		Running     bool
		Text, Error string
	}
}

// installRows are the server's Chromium and Claude Code, each with its
// Install button while it lacks them, in a card under a title. They stay
// hidden until the server says it lacks one.
func (remote *remoteDesktop) installRows() fyne.CanvasObject {
	type item struct {
		what, title, detail, button string
		has                         func(serverInstall) *bool
		status                      *flowText
		install                     *tapRow
		row                         fyne.CanvasObject
	}
	items := []*item{
		{what: "chromium", title: "Chromium", button: "Install Chromium",
			detail: "For Browser Use: the agent's browser tabs run on the server. The server downloads Chromium (Chrome for Testing, about 200 MB, checked against Google's checksum) into its data folder, with the system libraries it needs from Debian's signed packages. No administrator rights; it stays across restarts and image updates.",
			has:    func(state serverInstall) *bool { return state.Browser }},
		{what: "claude-code", title: "Claude Code", button: "Install Claude Code",
			detail: "For Claude Code connections. The server downloads Anthropic's claude command into its data folder, checked against Anthropic's release signature and checksum. Then sign in from Models & accounts.",
			has:    func(state serverInstall) *bool { return state.ClaudeCode }},
	}
	box := container.NewVBox()
	box.Hide()
	var refresh func()
	for _, entry := range items {
		entry.status = plainFlow("", 12.5, colMuted)
		entry.install = newRow(entry.button, nil, stylePrimary, nil)
		entry.install.onTap = func() {
			entry.install.Disable()
			setFlow(entry.status, "Starting…")
			go func() {
				ctx, cancel := context.WithTimeout(remote.ctx, 30*time.Second)
				defer cancel()
				err := remote.client.json(ctx, "POST", "/api/install/"+entry.what, nil, nil)
				fyne.Do(func() {
					if err != nil {
						setFlow(entry.status, err.Error())
						entry.install.Enable()
						return
					}
					refresh()
				})
			}()
		}
		entry.row = settingStack(entry.title, entry.detail, container.NewVBox(entry.status, container.NewHBox(entry.install)))
	}
	following := false
	refresh = func() {
		go func() {
			ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
			defer cancel()
			var state serverInstall
			err := remote.client.json(ctx, "GET", "/api/state", nil, &state)
			fyne.Do(func() {
				if err != nil {
					return
				}
				box.Objects = nil
				running := false
				var rows []fyne.CanvasObject
				for _, entry := range items {
					has := entry.has(state)
					job := state.Installs[entry.what]
					switch {
					case job.Running:
						running = true
						entry.install.Disable()
						entry.install.SetText("Installing…")
						setFlow(entry.status, job.Text)
					case has == nil || *has:
						if job.Error == "" && hasKey(state.Installs, entry.what) { // installed from here: say so once
							setFlow(entry.status, entry.title+" is installed on the server.")
							entry.install.Hide()
							rows = append(rows, entry.row)
						}
						continue
					default:
						entry.install.Enable()
						entry.install.SetText(entry.button)
						entry.install.Show()
						text := "Not installed on the server."
						if job.Error != "" {
							text = "The install failed: " + job.Error
							entry.install.SetText("Try again")
						}
						setFlow(entry.status, text)
					}
					rows = append(rows, entry.row)
				}
				if len(rows) == 0 {
					box.Hide()
					return
				}
				box.Objects = []fyne.CanvasObject{sectionTitle("On the server"), card(rows...)}
				box.Show()
				box.Refresh()
				if running && !following {
					following = true
					time.AfterFunc(time.Second, func() {
						fyne.Do(func() {
							following = false
							refresh()
						})
					})
				}
			})
		}()
	}
	refresh()
	return box
}

func hasKey[V any](values map[string]V, key string) bool {
	_, ok := values[key]
	return ok
}
