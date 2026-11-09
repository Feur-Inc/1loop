// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → General → Where the agent runs: every execution target on one
// part of the page, the one in use marked, so the choice the composer's
// target button makes is also explained and reachable here: this computer,
// its SSH machines, and a headless 1loop server (how to connect one is
// spelled out step by step, since nothing else in the window says it).

import (
	"encoding/json"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/core"
	"1loop/internal/sshmachine"
)

// inUse marks the target the agent works on now, in place of its button.
func inUse() fyne.CanvasObject {
	return container.NewCenter(textLine("In use", 12.5, colAmber))
}

// targetRows are General's targets: this computer, the SSH machines and
// the remote server, each with Use, or In use for the one the agent is on.
func (ui *desktopUI) targetRows() []fyne.CanvasObject {
	view := ui.prefs
	var computer fyne.CanvasObject = newRow("Use", nil, styleOutline, func() { ui.useComputer(); view.show("General") })
	if ui.server == nil && ui.cfg.MachineID == "" {
		computer = inUse()
	}
	add := newRow("Add machine", iconPlus, styleChip, func() {
		ui.sshEditor(sshmachine.Settings{ID: core.Identifier(), Port: 22, Auth: "key", KeyPath: "~/.ssh/id_ed25519", Projects: "~/1loop-projects"})
	})
	return []fyne.CanvasObject{
		sectionTitle("Where the agent runs"),
		padded(0, 10, 2, 0, plainFlow("Where the agent runs commands, edits files and keeps its projects. The target button under the conversation switches between them too.", 13, colMuted)),
		card(view.mark("general.target", settingRow("This computer", "The agent works here, with your user's rights. Computer Use is available only on this target.", computer))),
		container.NewBorder(nil, nil, nil, padded(14, 4, 0, 0, add), sectionTitle("SSH machines")),
		view.mark("general.ssh", card(ui.sshRows()...)),
		sectionTitle("Remote server"),
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
		var use fyne.CanvasObject = newRow("Use", nil, styleOutline, func() { ui.useMachine(machine.ID); ui.prefs.show("General") })
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

// serverRows are the headless 1loop server last paired, if any, and how to
// connect one.
func (ui *desktopUI) serverRows() []fyne.CanvasObject {
	var saved serverAddress
	if data, err := os.ReadFile(remoteAddressPath()); err == nil {
		json.Unmarshal(data, &saved)
	}
	var rows []fyne.CanvasObject
	if saved.URL != "" && saved.Token != "" {
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
				ui.prefs.show("General")
			}))
			detail = saved.URL + " · connected"
		}
		rows = append(rows, settingRow(serverName(saved.URL), detail, control))
	}
	steps := container.New(layout.NewCustomPaddedVBoxLayout(4))
	for i, step := range []string{
		"Start the server on its machine (1loop-server, or its Docker image) and keep its console open.",
		"Enter its HTTPS address. For a self-signed certificate, also paste the SHA-256 fingerprint the console prints.",
		"Approve this computer: type the pairing command 1loop shows in the server's console.",
	} {
		number := textLine(string(rune('1'+i))+".", 12.5, colAmber)
		steps.Add(container.NewBorder(nil, nil, padded(0, 0, 0, 8, number), nil, plainFlow(step, 12.5, colMuted)))
	}
	rows = append(rows, settingStack("Connect a server",
		"A headless 1loop server runs the agent, its files, browser and terminals on another machine (see docs/SERVER.md).",
		container.NewVBox(steps, container.NewHBox(newRow("Connect a server…", iconPlus, styleOutline, ui.connectServer)))))
	return rows
}
