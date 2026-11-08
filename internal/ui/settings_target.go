// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Execution target: where the agent runs commands, edits files
// and keeps its projects. Every target is on one page, the one in use
// marked, so the choice the composer's target button makes is also
// explained and reachable here: this computer, its SSH machines, and a
// headless 1loop server (how to connect one is spelled out step by step,
// since nothing else in the window says it).

import (
	"encoding/json"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/sshmachine"
)

// inUse marks the target the agent works on now, in place of its button.
func inUse() fyne.CanvasObject {
	return container.NewCenter(textLine("In use", 12.5, colAmber))
}

func (ui *desktopUI) executionSettings() fyne.CanvasObject {
	settings := ui.prefs
	page := container.NewVBox(settings.pageTitle("Execution target", "", nil),
		padded(0, 10, 2, 0, plainFlow("Where the agent runs commands, edits files and keeps its projects. The target button under the conversation switches between them too.", 13, colMuted)))

	if ui.server == nil {
		change := newRow("Change…", nil, styleOutline, func() {
			ui.pick(pickRequest{title: "Choose the workspace", start: ui.cfg.Workspace, folder: true}, func(paths []string) {
				root, err := agent.ResolveWorkspace(paths[0])
				if err != nil {
					ui.notice("Workspace", err.Error())
					return
				}
				if root != ui.cfg.Workspace {
					ui.selectProject(root) // shows the project's start screen: come back here
					settings.openOn("Execution target")
				}
			})
		})
		page.Add(sectionTitle("Project"))
		page.Add(card(ui.projectFolderRow(change)))
	}

	page.Add(sectionTitle("This computer"))
	var computer fyne.CanvasObject = newRow("Use", nil, styleOutline, func() { ui.useComputer(); settings.show("Execution target") })
	if ui.server == nil && ui.cfg.MachineID == "" {
		computer = inUse()
	}
	page.Add(card(settingRow("This computer", "The agent works here, with your user's rights. Computer Use is available only on this target.", computer)))

	page.Add(container.NewBorder(nil, nil, nil, container.NewCenter(newRow("Add machine", iconPlus, styleChip, func() {
		ui.sshEditor(sshmachine.Settings{ID: core.Identifier(), Port: 22, Auth: "key", KeyPath: "~/.ssh/id_ed25519", Projects: "~/1loop-projects"})
	})), sectionTitle("SSH machines")))
	page.Add(card(ui.sshRows()...))

	page.Add(sectionTitle("Remote server"))
	page.Add(card(ui.serverRows()...))
	return page
}

// sshRows are the SSH machines, each with Use (or In use) and Edit.
func (ui *desktopUI) sshRows() []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, machine := range ui.sshMachines {
		machine := machine
		edit := newRow("Edit", iconChevronRight, styleChip, func() { ui.sshEditor(machine) })
		edit.iconAfter = true
		var use fyne.CanvasObject = newRow("Use", nil, styleOutline, func() { ui.useMachine(machine.ID); ui.prefs.show("Execution target") })
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
				ui.prefs.show("Execution target")
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
