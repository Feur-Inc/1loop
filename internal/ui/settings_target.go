// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Remote (#443): everything remote on one page, in three parts.
// Where the agent runs lists every execution target, the one in use
// marked, so the choice the composer's target button makes is also
// explained and reachable here: this computer, its SSH machines, and the
// headless 1loop servers (how to connect one is spelled out step by step,
// since nothing else in the window says it). Devices that control this
// 1loop are the phones paired with this computer and the devices paired
// with the server in use, each with Forget or Remove, which ask first. The
// phone app is the switch that lets phones reach this computer, and its
// pairing code.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/core"
	registry "1loop/internal/settings"
	"1loop/internal/sshmachine"
)

// inUse marks the target the agent works on now, in place of its button.
func inUse() fyne.CanvasObject {
	return container.NewCenter(textLine("In use", 12.5, colAmber))
}

// partTitle heads a list inside one of a page's parts.
func partTitle(text string) fyne.CanvasObject {
	return padded(10, 6, 2, 0, textLine(text, 13, colMuted))
}

// partNote is the line under a part's title.
func partNote(text string) fyne.CanvasObject {
	return padded(0, 10, 2, 0, plainFlow(text, 13, colMuted))
}

// remoteSettings is the Remote page: where the agent runs, the devices
// that control this 1loop, the phone app.
func (ui *desktopUI) remoteSettings() fyne.CanvasObject {
	view := ui.prefs
	page := container.NewVBox(view.pageTitle("Remote", "", nil), view.intro(registry.Remote))
	for _, object := range ui.targetRows() {
		page.Add(object)
	}
	if ui.server != nil { // Install Chromium / Claude Code, while the server lacks them (#406)
		page.Add(ui.server.installRows())
	}
	page.Add(sectionTitle(registry.GroupDevices))
	page.Add(partNote("The phones and browsers that can use this 1loop. Forgetting one stops its access at once: it must be paired again to come back."))
	page.Add(partTitle("Paired with this computer"))
	page.Add(view.mark("phone.pairing", card(ui.pairedPhoneRows()...)))
	if ui.server != nil { // #428
		page.Add(partTitle("Paired with " + serverName(ui.server.client.address.URL)))
		page.Add(ui.serverDevicesCard(ui.server))
	}
	page.Add(sectionTitle(registry.GroupPhone))
	page.Add(card(ui.phoneAppRows()...))
	return page
}

// reshowRemote redraws Settings → Remote when it is on screen.
func (ui *desktopUI) reshowRemote() {
	if ui.prefs != nil && ui.prefs.at("Remote") {
		ui.prefs.show("Remote")
	}
}

// targetRows are Remote's first part: this computer, the SSH machines and
// the remote servers, each with Use, or In use for the one the agent is on.
func (ui *desktopUI) targetRows() []fyne.CanvasObject {
	view := ui.prefs
	var computer fyne.CanvasObject = newRow("Use", nil, styleOutline, func() { ui.useComputer(); view.show("Remote") })
	if ui.server == nil && ui.cfg.MachineID == "" {
		computer = inUse()
	}
	add := newRow("Add machine", iconPlus, styleChip, func() {
		ui.sshEditor(sshmachine.Settings{ID: core.Identifier(), Port: 22, Auth: "key", KeyPath: "~/.ssh/id_ed25519", Projects: "~/1loop-projects"})
	})
	return []fyne.CanvasObject{
		sectionTitle(registry.GroupTargets),
		partNote("Where the agent runs commands, edits files and keeps its projects. The target button under the conversation switches between them too."),
		card(view.mark("general.target", settingRow("This computer", "The agent works here, with your user's rights. Computer Use is available only on this target.", computer))),
		container.NewBorder(nil, nil, nil, padded(6, 2, 0, 0, add), partTitle("SSH machines")),
		view.mark("general.ssh", card(ui.sshRows()...)),
		partTitle("Remote servers"),
		view.mark("general.remote_server", card(ui.serverRows()...)),
	}
}

// sshRows are the SSH machines, each with Use (or In use) and Edit.
func (ui *desktopUI) sshRows() []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, machine := range ui.sshMachines {
		machine := machine
		edit := newRow("Edit", iconChevronRight, styleChip, func() { ui.sshEditor(machine) })
		edit.iconAfter = true
		var use fyne.CanvasObject = newRow("Use", nil, styleOutline, func() { ui.useMachine(machine.ID); ui.prefs.show("Remote") })
		if ui.server == nil && ui.cfg.MachineID == machine.ID {
			use = inUse()
		}
		rows = append(rows, settingRow(machine.Label(), machine.User+"@"+machine.Address()+" · projects in "+machine.Projects,
			container.New(layout.NewCustomPaddedHBoxLayout(10), use, edit)))
	}
	if len(rows) == 0 {
		rows = append(rows, settingRow("No SSH machine yet", "Add a computer the agent reaches over SSH: its projects are folders there, and it runs commands and a terminal there, while this computer keeps the models, Browser Use and Computer Use.", nil))
	}
	return rows
}

// serverRows are the headless 1loop servers paired, if any, and how to
// connect one.
func (ui *desktopUI) serverRows() []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, saved := range loadServerAddresses() {
		connected := ui.server != nil && ui.server.client.address.URL == saved.URL
		var control fyne.CanvasObject = newRow("Use", nil, styleOutline, func() {
			client, err := newRemoteClient(saved)
			if err != nil {
				ui.notice("Remote server", err.Error())
				return
			}
			ui.useServer(client)
		})
		detail := saved.URL + " · paired"
		if connected {
			control = container.New(layout.NewCustomPaddedHBoxLayout(10), inUse(), newRow("Disconnect", nil, styleOutline, func() {
				ui.useComputer()
				ui.prefs.show("Remote")
			}))
			detail = saved.URL + " · connected"
		}
		forget := newRow("Forget", iconTrash, styleChip, func() { ui.forgetServer(saved) })
		control = container.New(layout.NewCustomPaddedHBoxLayout(10), control, forget)
		rows = append(rows, settingRow(serverName(saved.URL), detail, control))
	}
	// One line and its button: the steps are in the guide it opens (#443).
	rows = append(rows, settingRow("Connect a server", "Run 1loop on another machine, and work there from here.",
		newRow("Connect a server…", iconPlus, styleOutline, ui.connectServer)))
	return rows
}
