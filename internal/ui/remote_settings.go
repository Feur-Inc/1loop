// SPDX-License-Identifier: MPL-2.0

package ui

// A server's settings pages that are neither its connections nor its
// extensions: its tool permissions, preferences and hooks, and its updates.
// The desktop shows them under Settings → Servers (settings_servers.go),
// the browser client as its own General, Agent & approvals and Updates
// (settings_browser_js.go).

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/update"
)

// settingsPage is the ID of one of the server's settings pages: under its
// group on the desktop, the page itself in the browser client, whose
// settings are its server's.
func (remote *remoteDesktop) settingsPage(name string) string {
	if remote.parent.home != "" {
		return name
	}
	return serverPageID(remote.client.address.URL, name)
}

// agentSettings is the server's Agent page: what its agent may do without
// asking, what it is told and its hooks.
func (remote *remoteDesktop) agentSettings(head serverHead) fyne.CanvasObject {
	page := container.NewVBox(head(nil), sectionTitle("Permissions"), card(remote.permissionRow()), sectionTitle("Instructions and memory"))
	for _, object := range remote.instructionCards(10) {
		page.Add(object)
	}
	page.Add(sectionTitle("Hooks"))
	page.Add(card(remote.hooksRow()))
	return page
}

// permissionRow is the server's Tool permissions.
func (remote *remoteDesktop) permissionRow() fyne.CanvasObject {
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
	return settingRow("Tool permissions",
		"Asks before commands and edits on the server. YOLO runs them at once.",
		sized(230, mode.row))
}

// hooksRow edits the server's hooks.
func (remote *remoteDesktop) hooksRow() fyne.CanvasObject {
	return settingRow("Hook commands", "",
		newRow("Edit", nil, styleOutline, remote.editHooks))
}

// generalSettings is the browser client's General: the server's tool
// permissions, hooks and installs. The workspace is chosen in the sidebar's
// projects.
func (remote *remoteDesktop) generalSettings() fyne.CanvasObject {
	return container.NewVBox(
		remote.parent.prefs.pageTitle("General", "", nil),
		sectionTitle("Permissions"),
		card(remote.permissionRow()),
		sectionTitle("Hooks"),
		card(remote.hooksRow()),
		remote.installRows(false),
	)
}

// instructionSettings is the browser client's Agent & approvals: the
// server's preferences, memory switch and next-step card switch.
func (remote *remoteDesktop) instructionSettings() fyne.CanvasObject {
	page := container.NewVBox(remote.parent.prefs.pageTitle("Instructions", "", nil))
	for _, object := range remote.instructionCards(14) {
		page.Add(object)
	}
	return page
}

// instructionCards edit the server's preferences, memory switch and
// next-step card switch, once the server said them.
func (remote *remoteDesktop) instructionCards(rows int) []fyne.CanvasObject {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(rows)
	entry.SetPlaceHolder("Language, tone, coding conventions, level of detail…")
	entry.Disable() // until the server's text is in
	memory := container.NewStack()
	wrapUp := container.NewStack()
	cards := []fyne.CanvasObject{
		card(settingStack("Your preferences",
			"Added to every chat's system prompt, from the next turn.",
			entry),
			settingRow("Memory", "The agent proposes what it learns; you approve each change.", memory)),
		card(settingRow("Next-step card", "A recap at the end of each turn, with buttons for your reply.", wrapUp)),
	}
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
		defer cancel()
		var state struct {
			Settings struct {
				UserPreferences string
				Memory          bool
				WrapUp          bool
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
			wrapUp.Objects = []fyne.CanvasObject{newToggle(state.Settings.WrapUp, func(on bool) {
				remote.action("/api/wrap-up", map[string]bool{"enabled": on})
			})}
			wrapUp.Refresh()
		})
	}()
	return cards
}

// updateSettings is the server's version, and its update. In the browser
// client, the page comes from the server, so it updates with it.
func (remote *remoteDesktop) updateSettings(head serverHead) fyne.CanvasObject {
	status := widget.NewLabel("Checking the server's version…")
	status.Wrapping = fyne.TextWrapWord
	notes := newMarkdownView("")
	install := newRow("Update the server", nil, stylePrimary, func() {
		remote.updateServer()
		status.SetText("Downloading its update; this page reconnects when done.")
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
		head(nil),
		card(settingStack("Server updates",
			"Checks for updates and installs them, checked by signature.",
			container.NewVBox(status, container.NewHBox(check, install)))),
		notes.box,
	)
}
