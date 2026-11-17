// SPDX-License-Identifier: MPL-2.0

package ui

// Add project is one dialog, New project (#408): where the project lives
// (#381: this computer, an SSH machine of Settings → General, or a paired
// server), its name, the folders of this computer it is made of (each
// with its branch, and a Worktree box to work in a new worktree of it),
// and its environment variables. The project is kept under its target and
// its chats run there. A folder of this computer is never sent to a
// server, nor a server's opened here: the target is chosen, not implied
// by the one the window happens to work on.

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// addTargets are the targets Add project offers, in the sidebar's order:
// this computer, each SSH machine of Settings, then the paired servers.
// The browser client runs nothing itself, so it offers only its servers.
func (ui *desktopUI) addTargets() []projectTarget {
	var targets []projectTarget
	if !browserClient {
		targets = append(targets, localTarget)
	}
	for _, machine := range ui.sshMachines {
		targets = append(targets, sshTarget(machine.ID))
	}
	for _, address := range ui.serverAddresses() {
		targets = append(targets, serverTarget(address))
	}
	return targets
}

// serverAddresses are the addresses of the servers the window follows,
// in the order they were paired (#383), then the one it works on if it
// is not among them: the order the sidebar lists them in (serverGroups).
func (ui *desktopUI) serverAddresses() []string {
	var addresses []string
	for _, link := range ui.links {
		addresses = append(addresses, link.address.URL)
	}
	if remote := ui.server; remote != nil && ui.linkTo(remote.client.address.URL) == nil {
		addresses = append(addresses, remote.client.address.URL)
	}
	return addresses
}

// targetName names a target as Add project offers it.
func (ui *desktopUI) targetName(target projectTarget) (name, detail string, icon fyne.Resource) {
	switch target.kind {
	case targetSSH:
		settings, _ := ui.machineSettings(target.id)
		return settings.Label(), "SSH · " + settings.User + "@" + settings.Host, iconTerminal
	case targetServer:
		return serverName(target.id), target.id, iconBrowser
	}
	return "This computer", "A folder of this computer", theme.ComputerIcon()
}

// activeTarget is the target the window works on.
func (ui *desktopUI) activeTarget() projectTarget {
	switch {
	case ui.server != nil:
		return serverTarget(ui.server.client.address.URL)
	case ui.cfg.MachineID != "":
		return sshTarget(ui.cfg.MachineID)
	}
	return localTarget
}

// addProject opens the New project dialog on the target the window works
// on, when it is one Add project offers, else on the first.
func (ui *desktopUI) addProject() {
	targets := ui.addTargets()
	if len(targets) == 0 {
		return
	}
	target := targets[0]
	if active := ui.activeTarget(); slices.Contains(targets, active) {
		target = active
	}
	ui.addProjectOn(target)
}

// keepProjectOn keeps a new project under its target. A server keeps its
// own list: the project is listed once it lists it.
func (ui *desktopUI) keepProjectOn(target projectTarget, folder string) {
	switch target.kind {
	case targetLocal:
		ui.ensureProject(folder)
	case targetSSH:
		ui.ensureMachineProject(target.id, folder)
	}
}

// projectAdded keeps a project just added under its target and opens it
// there.
func (ui *desktopUI) projectAdded(target projectTarget, folder string) {
	ui.keepProjectOn(target, folder)
	if ui.activeTarget() == target && target.kind != targetServer && folder == ui.cfg.Workspace {
		ui.save()
		ui.renderChats()
		return
	}
	ui.openProjectOn(target, folder)
}
