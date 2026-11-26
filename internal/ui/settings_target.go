// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → General → Where the agent runs: every execution target on one
// part of the page, so the choice the composer's target button makes is
// also explained here: this computer, its SSH machines, and the headless
// 1loop servers (how to connect one is spelled out step by step, since
// nothing else in the window says it). Each server's own settings are its
// group under Servers (settings_servers.go).

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/core"
	"1loop/internal/sshmachine"
)

// targetRows are General's targets: this computer, the SSH machines and
// the paired servers. They read the same whatever project is open: the
// target button and the sidebar's projects switch between them (#639).
func (ui *desktopUI) targetRows() []fyne.CanvasObject {
	view := ui.prefs
	add := newRow("Add machine", iconPlus, styleChip, func() {
		ui.sshEditor(sshmachine.Settings{ID: core.Identifier(), Port: 22, Auth: "key", KeyPath: "~/.ssh/id_ed25519", Projects: "~/1loop-projects"})
	})
	return []fyne.CanvasObject{
		sectionTitle("Where the agent runs"),
		padded(0, 10, 2, 0, plainFlow("Switch with the target button under the conversation, or open a project in the sidebar.", 13, colMuted)),
		card(view.mark("general.target", settingRow("This computer", "The agent works here, with your user's rights. Computer Use works only here.", nil))),
		container.NewBorder(nil, nil, nil, padded(14, 4, 0, 0, add), sectionTitle("SSH machines")),
		view.mark("general.ssh", card(ui.sshRows()...)),
		sectionTitle("Remote servers"),
		view.mark("general.remote_server", card(ui.serverRows()...)),
	}
}

// sshRows are the SSH machines, each with Edit.
func (ui *desktopUI) sshRows() []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, machine := range ui.sshMachines {
		machine := machine
		edit := newRow("Edit", iconChevronRight, styleChip, func() { ui.sshEditor(machine) })
		edit.iconAfter = true
		rows = append(rows, settingRow(machine.Label(), machine.User+"@"+machine.Address()+" · projects in "+machine.Projects, edit))
	}
	if len(rows) == 0 {
		rows = append(rows, settingRow("No SSH machine yet", "Add a computer the agent reaches over SSH: its projects are folders there, and it runs commands and a terminal there, while this computer keeps the models, Browser Use and Computer Use.", nil))
	}
	return rows
}

// serverRows are the headless 1loop servers paired, each with its settings
// (under Servers, in the sidebar) and Forget, and how to connect one.
func (ui *desktopUI) serverRows() []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, saved := range loadServerAddresses() {
		open := newRow("Settings", iconChevronRight, styleChip, ui.prefs.link(serverPageID(saved.URL, "Connections")))
		open.iconAfter = true
		forget := newRow("Forget", iconTrash, styleChip, func() { ui.forgetServer(saved) })
		rows = append(rows, settingRow(ui.serverLabel(saved.URL), saved.URL, container.New(layout.NewCustomPaddedHBoxLayout(10), open, forget)))
	}
	steps := container.New(layout.NewCustomPaddedVBoxLayout(4))
	for i, step := range []string{
		"Start the server on its machine (1loop-server, or its Docker image).",
		"Enter its HTTPS address. For a self-signed certificate, also paste the SHA-256 fingerprint the console prints.",
		"Approve this computer in 1loop on a device already paired with the server, or type the pairing command in the server's console.",
	} {
		number := textLine(string(rune('1'+i))+".", 12.5, colAmber)
		steps.Add(container.NewBorder(nil, nil, padded(0, 0, 0, 8, number), nil, plainFlow(step, 12.5, colMuted)))
	}
	rows = append(rows, settingStack("Connect a server",
		"A headless 1loop server runs the agent, its files, browser and terminals on another machine (see docs/SERVER.md).",
		container.NewVBox(steps, container.NewHBox(newRow("Connect a server…", iconPlus, styleOutline, ui.connectServer)))))
	return rows
}
