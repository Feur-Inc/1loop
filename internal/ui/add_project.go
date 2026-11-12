// SPDX-License-Identifier: MPL-2.0

package ui

// Add project asks which target the project lives on (#381): this
// computer, an SSH machine of Settings → General, or the paired server.
// The folder is then picked on that target, the project is kept under it,
// and its chats run there. A folder of this computer is never sent to a
// server, nor a server's opened here: the target is chosen, not implied
// by the one the window happens to work on.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// addTargets are the targets Add project offers, in the sidebar's order:
// this computer, each SSH machine of Settings, then the paired server.
// The browser client runs nothing itself, so it offers only its server.
func (ui *desktopUI) addTargets() []projectTarget {
	var targets []projectTarget
	if !browserClient {
		targets = append(targets, localTarget)
	}
	for _, machine := range ui.sshMachines {
		targets = append(targets, sshTarget(machine.ID))
	}
	if address := ui.pairedServer(); address != "" {
		targets = append(targets, serverTarget(address))
	}
	return targets
}

// pairedServer is the address of the server the window works on or
// follows, "" when none is paired.
func (ui *desktopUI) pairedServer() string {
	switch {
	case ui.server != nil:
		return ui.server.client.address.URL
	case ui.link != nil:
		return ui.link.address.URL
	}
	return ""
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

// addProject asks which target the new project lives on, then picks its
// folder there. With a single target there is nothing to ask.
func (ui *desktopUI) addProject() {
	targets := ui.addTargets()
	if len(targets) == 1 {
		ui.addProjectOn(targets[0])
		return
	}
	var dialog *sheet
	list := container.New(layout.NewCustomPaddedVBoxLayout(2))
	for _, target := range targets {
		target := target
		name, detail, icon := ui.targetName(target)
		row := newRow(name, icon, styleMenu, func() {
			dialog.Hide()
			ui.addProjectOn(target)
		})
		row.detail = detail
		list.Add(row)
	}
	body := container.NewVBox(note("Where does the project live? Its chats run there."), list)
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Add project", body, 420, nil, cancel)
}

// addProjectOn picks a project's folder on a target: with this computer's
// folder picker, or by name in the projects folder of an SSH machine or
// of the server.
func (ui *desktopUI) addProjectOn(target projectTarget) {
	switch target.kind {
	case targetSSH:
		ui.addMachineProject(target.id)
	case targetServer:
		ui.addServerProject(target)
	default:
		ui.addLocalProject()
	}
}

// addLocalProject picks a folder of this computer, even while a server or
// an SSH machine is in use: the project is this computer's.
func (ui *desktopUI) addLocalProject() {
	start := ui.lastLocalWorkspace()
	if ui.server == nil && ui.cfg.MachineID == "" {
		start = ui.cfg.Workspace
	}
	ui.pick(pickRequest{title: "Add a project folder", start: filepath.Dir(start), folder: true}, func(paths []string) {
		root, err := agent.ResolveWorkspace(paths[0])
		if err != nil {
			ui.notice("Add project", err.Error())
			return
		}
		ui.projectAdded(localTarget, root)
	})
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

// addServerProject makes a project on the paired server: a folder of its
// projects folder, made there unless it exists.
func (ui *desktopUI) addServerProject(target projectTarget) {
	client := (*remoteClient)(nil)
	if remote := ui.server; remote != nil && remote.client.address.URL == target.id {
		client = remote.client
	} else {
		saved := loadServerAddress()
		if saved.URL != target.id || saved.Token == "" {
			ui.notice("Add project", "This server is no longer paired. Connect it again with the target menu under the conversation.")
			return
		}
		var err error
		if client, err = newRemoteClient(saved); err != nil {
			ui.notice("Add project", err.Error())
			return
		}
	}
	name := serverName(target.id)
	ui.projectNameDialog("New project on "+name, "A folder in the projects folder of "+name+". An existing folder of that name is opened as it is.",
		func(ctx context.Context, folder string) (string, error) {
			var reply struct{ Workspace string }
			err := client.json(ctx, "POST", "/api/projects", map[string]any{"path": folder}, &reply)
			if err != nil { // not there yet: made
				err = client.json(ctx, "POST", "/api/projects", map[string]any{"path": folder, "create": true}, &reply)
			}
			if err == nil && reply.Workspace == "" {
				err = errors.New("The server did not say where the project is")
			}
			return reply.Workspace, err
		},
		func(project string) {
			ui.setReach(target, reachOnline, "")
			if link := ui.link; link != nil { // its listing is read again, at once
				select {
				case link.wake <- struct{}{}:
				default:
				}
			}
			if remote := ui.server; remote != nil && remote.client.address.URL == target.id {
				remote.notify()
			}
			ui.projectAdded(target, project)
		})
}

// projectNameDialog asks a new project's folder name, then makes it on a
// target with makeFolder (off the UI thread), and calls done with its folder
// there once it is.
func (ui *desktopUI) projectNameDialog(title, text string, makeFolder func(ctx context.Context, name string) (string, error), done func(string)) {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Project name")
	problem := plainFlow("", 12.5, colDanger)
	var dialog *sheet
	var create *tapRow
	apply := func() {
		name := strings.TrimSpace(entry.Text)
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") {
			setFlow(problem, "Enter a folder name, without / or \\.")
			return
		}
		create.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, time.Minute)
			defer cancel()
			project, err := makeFolder(ctx, name)
			fyne.Do(func() {
				create.Enable()
				if err != nil {
					setFlow(problem, err.Error())
					return
				}
				dialog.Hide()
				done(project)
			})
		}()
	}
	entry.OnSubmitted = func(string) { apply() }
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	create = newRow("Create", nil, stylePrimary, apply)
	dialog = ui.modal(title, container.NewVBox(note(text), entry, problem), 480, nil, cancel, create)
	ui.window.Canvas().Focus(entry)
}
