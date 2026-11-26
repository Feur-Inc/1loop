// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Remote (#443): everything remote on one page, in three parts.
// Where the agent runs lists every execution target (this computer, SSH
// machines, and paired headless 1loop servers, with a step-by-step setup flow
// behind Connect a server); each server's own settings are its group under
// Servers (settings_servers.go). Devices that control this 1loop lists the
// phones paired with this computer, each with Forget, which asks first. The
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

// inUse marks the target the agent works on now, in place of its button
// (used in the browser client's Remote page).
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
// that control this 1loop, the phone app. It reads the same whatever project
// is open; each server's own Installs and Devices live under Servers (#639).
func (ui *desktopUI) remoteSettings() fyne.CanvasObject {
	view := ui.prefs
	page := container.NewVBox(view.pageTitle("Remote", "", nil), view.intro(registry.Remote))
	for _, object := range ui.targetRows() {
		page.Add(object)
	}
	page.Add(sectionTitle(registry.GroupDevices))
	page.Add(partNote("Phones paired with this computer. Forgetting one stops its access at once."))
	page.Add(view.mark("phone.pairing", card(ui.pairedPhoneRows()...)))
	page.Add(sectionTitle(registry.GroupPhone))
	page.Add(card(ui.phoneAppRows()...))
	return page
}

// reshowRemote redraws Settings → Remote when it is on screen.
func (ui *desktopUI) reshowRemote() { ui.reshowRemoteFor("") }

// targetRows are Remote's targets: this computer, the SSH machines and
// the paired servers. They read the same whatever project is open: the
// target button and the sidebar's projects switch between them (#639).
func (ui *desktopUI) targetRows() []fyne.CanvasObject {
	view := ui.prefs
	add := newRow("Add machine", iconPlus, styleChip, func() {
		ui.sshEditor(sshmachine.Settings{ID: core.Identifier(), Port: 22, Auth: "key", KeyPath: "~/.ssh/id_ed25519", Projects: "~/1loop-projects"})
	})
	return []fyne.CanvasObject{
		sectionTitle(registry.GroupTargets),
		partNote("Switch with the target button under a chat."),
		card(view.mark("general.target", settingRow("This computer", "Runs here, with your rights. Computer Use works only here.", nil))),
		container.NewBorder(nil, nil, nil, padded(6, 2, 0, 0, add), partTitle("SSH machines")),
		view.mark("general.ssh", card(ui.sshRows()...)),
		partTitle("Remote servers"),
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
		rows = append(rows, settingRow("No SSH machine yet", "Commands run there; models, browser and screen stay here.", nil))
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
	// One line and its button: the steps are in the guide it opens (#443).
	rows = append(rows, settingRow("Connect a server", "Runs the agent, files and terminals on another machine.",
		newRow("Connect a server…", iconPlus, styleOutline, ui.connectServer)))
	return rows
}
