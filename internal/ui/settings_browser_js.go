// SPDX-License-Identifier: MPL-2.0

package ui

// Settings in the browser client (issue #267): the page has no computer of
// its own, so each section is the server's, through its API, or says where
// on the server it is set. The desktop's pages would read files that are
// not there ("open extensions.json: not implemented on js") or reach sites
// a page may not (the release page, for updates).

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/update"
)

// SSH machines and the phone remote are this computer's: a page has neither.
func settingsSectionShown(name string) bool {
	return name != "SSH machines" && name != "Phone remote"
}

func (ui *desktopUI) browserSettings(section string) fyne.CanvasObject {
	remote := ui.server
	if remote == nil {
		return container.NewVBox(ui.prefs.pageTitle(section, "", nil),
			card(settingRow("Not connected", "Settings are the server's: they show once this page is connected to it.", nil)))
	}
	switch section {
	case "Instructions":
		return remote.instructionSettings()
	case "Connections":
		return serverSideSettings(ui, section, "The server's accounts and models",
			"Chats run on the server, with its connections. They are set where the server runs: its configuration file (-config), or -provider, -base-url, -model and -api-key-env when it starts.")
	case "Extensions":
		return serverSideSettings(ui, section, "The server's skills, plugins and MCP servers",
			"The agent runs on the server, with the extensions installed there, in the data folder of its user.")
	case "Connectors":
		return serverSideSettings(ui, section, "The server's connectors",
			"Connectors such as GitHub sign in on the server, where the agent uses them.")
	case "Updates":
		return remote.updateSettings()
	default:
		return remote.generalSettings()
	}
}

func serverSideSettings(ui *desktopUI, section, title, detail string) fyne.CanvasObject {
	return container.NewVBox(ui.prefs.pageTitle(section, "", nil), card(settingRow(title, detail, nil)))
}

// generalSettings is what of General a server takes: tool permissions and
// hooks. The workspace is chosen in the sidebar's projects.
func (remote *remoteDesktop) generalSettings() fyne.CanvasObject {
	ui := remote.parent
	mode := newPicker(ui.window, "Tool permissions", styleField, nil)
	mode.Options = []string{"Approval required", "YOLO mode"}
	modes := []string{"approval", "yolo"}
	if remote.autoMode {
		mode.Options, modes = append(mode.Options, "Auto mode"), append(modes, "auto")
	}
	mode.SetSelected(mode.Options[0])
	for i, name := range modes {
		if remote.approvalMode == name {
			mode.SetSelected(mode.Options[i])
		}
	}
	mode.onChanged = func(label string) {
		for i, option := range mode.Options {
			if label == option {
				remote.approvalMode = modes[i]
				remote.action("/api/approval-mode", map[string]string{"mode": modes[i]})
			}
		}
		ui.updateApprovalControl()
	}
	return container.NewVBox(
		ui.prefs.pageTitle("General", "", nil),
		sectionTitle("Permissions"),
		card(settingRow("Tool permissions",
			"Approval asks before each shell command or file edit on the server. YOLO runs them at once, with the server user's rights.",
			sized(230, mode.row))),
		sectionTitle("Hooks"),
		card(settingRow("Hook commands", "The server's commands around the agent's work, in Claude Code's format.",
			newRow("Edit", nil, styleOutline, remote.editHooks))),
	)
}

// instructionSettings edits the server's preferences and memory switch.
func (remote *remoteDesktop) instructionSettings() fyne.CanvasObject {
	ui := remote.parent
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(14)
	entry.SetPlaceHolder("Describe your preferences: language, tone, coding conventions, level of detail…")
	entry.Disable() // until the server's text is in
	memory := container.NewStack()
	page := container.NewVBox(
		ui.prefs.pageTitle("Instructions", "", nil),
		card(settingStack("Your preferences",
			"Added to the server agent's system prompt in every chat. Saved as you type; they apply from the next turn.",
			entry),
			settingRow("Memory", "The agent adds what it learns about you to these preferences, each change shown to you for approval.", memory)),
	)
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
		defer cancel()
		var state struct {
			Settings struct {
				UserPreferences string
				Memory          bool
			}
		}
		err := remote.client.json(ctx, "GET", "/api/state", nil, &state)
		fyne.Do(func() {
			if err != nil {
				entry.SetPlaceHolder(err.Error())
				return
			}
			entry.SetText(state.Settings.UserPreferences)
			entry.Enable()
			var save *time.Timer
			entry.OnChanged = func(text string) {
				if save != nil {
					save.Stop()
				}
				save = time.AfterFunc(700*time.Millisecond, func() {
					remote.action("/api/preferences", map[string]string{"userPreferences": text})
				})
			}
			memory.Objects = []fyne.CanvasObject{newToggle(state.Settings.Memory, func(on bool) {
				remote.action("/api/memory", map[string]bool{"enabled": on})
			})}
			memory.Refresh()
		})
	}()
	return page
}

// updateSettings is the server's version: the page comes from the server,
// so it updates with it.
func (remote *remoteDesktop) updateSettings() fyne.CanvasObject {
	ui := remote.parent
	status := widget.NewLabel("Checking the server's version…")
	status.Wrapping = fyne.TextWrapWord
	notes := newMarkdownView("")
	install := newRow("Update the server", nil, stylePrimary, func() {
		remote.updateServer()
		status.SetText("The server is downloading its update; this page reconnects when it restarts.")
	})
	install.Hide()
	check := newRow("Check now", nil, styleOutline, nil)
	check.onTap = func() {
		status.SetText("Checking the server's version…")
		go func() {
			ctx, cancel := context.WithTimeout(remote.ctx, 40*time.Second)
			defer cancel()
			var state update.Status
			err := remote.client.json(ctx, "GET", "/api/update", nil, &state)
			fyne.Do(func() {
				text := "The server runs 1loop " + state.Current + "."
				switch {
				case err != nil:
					text = "The server could not say: " + err.Error()
				case state.Available:
					text += " Version " + state.Latest + " is available."
				case state.Latest != "":
					text += " It is up to date."
				}
				if err == nil && state.Problem != "" {
					text += "\n" + state.Problem
				}
				status.SetText(text)
				notes.ParseMarkdown(state.Notes)
				if err == nil && state.Available && state.CanUpdate {
					install.Show()
				} else {
					install.Hide()
				}
			})
		}()
	}
	check.onTap()
	return container.NewVBox(
		ui.prefs.pageTitle("Updates", "", nil),
		card(settingStack("Server updates",
			"This page is served by your 1loop server, so it is always the server's version. The server checks the release page and updates itself; downloads are checked against the release's signature.",
			container.NewVBox(status, container.NewHBox(check, install)))),
		notes.box,
	)
}
