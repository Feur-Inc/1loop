// SPDX-License-Identifier: MPL-2.0

package ui

// A project's environment variables (#408): set in Add project or in the
// project's menu, they reach the commands its chats start (the shell
// tool, background tasks, terminals, Claude Code) on its target. This
// computer's and an SSH machine's projects keep them in the state file
// (0600); a server's project keeps them on the server. They are never
// logged, nor put in a prompt.

import (
	"context"
	"fmt"
	"image/color"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
)

// projectEnvKey names a project's variables in the state file: its
// target, then its folder.
func projectEnvKey(target projectTarget, project string) string {
	return target.key() + " " + project
}

// projectEnvOf is a copy of the variables of a project of this computer or
// of an SSH machine.
func (ui *desktopUI) projectEnvOf(target projectTarget, project string) map[string]string {
	return maps.Clone(ui.projectEnv[projectEnvKey(target, project)])
}

// keepProjectEnv keeps a project's variables of this computer or of an SSH
// machine; none forgets them.
func (ui *desktopUI) keepProjectEnv(target projectTarget, project string, env map[string]string) {
	key := projectEnvKey(target, project)
	if len(env) == 0 {
		if _, ok := ui.projectEnv[key]; ok {
			delete(ui.projectEnv, key)
			ui.save()
		}
		return
	}
	if ui.projectEnv == nil {
		ui.projectEnv = map[string]map[string]string{}
	}
	ui.projectEnv[key] = maps.Clone(env)
	ui.save()
}

// moveProjectEnv follows a project of this computer renamed (destination)
// or deleted (""), with the projects under it.
func (ui *desktopUI) moveProjectEnv(path, destination string) {
	prefix := localTarget.key() + " "
	for key, env := range ui.projectEnv {
		project, ok := strings.CutPrefix(key, prefix)
		if !ok || !remoteapi.ProjectWithin(path, project) {
			continue
		}
		delete(ui.projectEnv, key)
		if destination != "" {
			ui.projectEnv[prefix+remoteapi.MovedProject(path, destination, project)] = env
		}
	}
}

// runEnv is the variables of the project a chat of this computer or of an
// SSH machine works in; a worktree's are its project's.
func (ui *desktopUI) runEnv(cfg agent.Config) map[string]string {
	if cfg.MachineID != "" {
		return ui.projectEnvOf(sshTarget(cfg.MachineID), cfg.Workspace)
	}
	project := cfg.Workspace
	if owner := ui.worktrees[project]; owner != "" {
		project = owner
	}
	return ui.projectEnvOf(localTarget, project)
}

// folderEnv is the variables for a terminal of the panel opened in dir.
func (ui *desktopUI) folderEnv(dir, machineID string) map[string]string {
	if ui.server != nil && machineID == "" { // the server's terminals start there
		return nil
	}
	return ui.runEnv(agent.Config{Workspace: dir, MachineID: machineID})
}

// envSummary says how many variables a project has, and their names.
func envSummary(env map[string]string) string {
	if len(env) == 0 {
		return "None"
	}
	names := slices.Sorted(maps.Keys(env))
	if len(names) == 1 {
		return "1 variable: " + names[0]
	}
	return fmt.Sprintf("%d variables: %s", len(names), strings.Join(names, ", "))
}

// envRow is one variable of the editor.
type envRow struct {
	name, value *widget.Entry
	row         fyne.CanvasObject
}

// envEditor edits a project's variables in a sheet over the window: one
// row per variable, its value hidden until revealed. done receives them
// when saved.
func (ui *desktopUI) envEditor(title string, env map[string]string, done func(map[string]string)) *sheet {
	var rows []*envRow
	list := container.New(layout.NewCustomPaddedVBoxLayout(6))
	problem := plainFlow("", 12.5, colDanger)
	var add func(name, value string)
	add = func(name, value string) {
		row := &envRow{name: widget.NewEntry(), value: widget.NewPasswordEntry()}
		row.name.SetPlaceHolder("NAME")
		row.name.SetText(name)
		row.value.SetPlaceHolder("Value")
		row.value.SetText(value)
		remove := newIconButton(iconTrash, color.NRGBA{}, colMuted, nil)
		remove.onTap = func() {
			rows = slices.DeleteFunc(rows, func(other *envRow) bool { return other == row })
			list.Remove(row.row)
			list.Refresh()
		}
		row.row = container.NewBorder(nil, nil, nil, container.NewCenter(remove),
			container.NewGridWithColumns(2, row.name, row.value))
		rows = append(rows, row)
		list.Add(row.row)
	}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		add(name, env[name])
	}
	if len(rows) == 0 {
		add("", "")
	}
	addButton := newRow("Add variable", iconPlus, styleChip, func() {
		add("", "")
		ui.window.Canvas().Focus(rows[len(rows)-1].name)
	})
	var dialog *sheet
	save := func() {
		values := map[string]string{}
		for _, row := range rows {
			name := strings.TrimSpace(row.name.Text)
			if name == "" && row.value.Text == "" {
				continue
			}
			if !platform.ValidEnvName(name) {
				setFlow(problem, fmt.Sprintf("“%s” is not a variable name: letters, digits and _, not starting with a digit.", name))
				return
			}
			if _, twice := values[name]; twice {
				setFlow(problem, name+" is set twice.")
				return
			}
			values[name] = row.value.Text
		}
		dialog.Hide()
		done(values)
	}
	body := container.New(layout.NewCustomPaddedVBoxLayout(8),
		note("Set for the shell commands, background tasks and terminals of this project's chats, where the project lives. They are not shown to the model, which reads one only by running a command that prints it."),
		list, container.NewHBox(addButton), problem)
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal(title, body, 560, nil, cancel, newRow("Save", nil, stylePrimary, save))
	if len(rows) > 0 && rows[0].name.Text == "" {
		ui.window.Canvas().Focus(rows[0].name)
	}
	return dialog
}

// projectEnvAction is the project menu's "Environment variables…": the
// editor, for a project of any target.
func (ui *desktopUI) projectEnvAction(target projectTarget, project string) menuAction {
	return menuAction{label: "Environment variables…", icon: iconTerminal, run: func() { ui.editProjectEnv(target, project) }}
}

// editProjectEnv opens a project's variables in the editor, read from the
// server for a server's project, and keeps them where they were.
func (ui *desktopUI) editProjectEnv(target projectTarget, project string) {
	title := "Environment variables · " + targetProjectName(target, project)
	if target.kind != targetServer {
		ui.envEditor(title, ui.projectEnvOf(target, project), func(env map[string]string) {
			ui.keepProjectEnv(target, project, env)
		})
		return
	}
	client, err := ui.serverClient(target)
	if err != nil {
		ui.notice("Environment variables", err.Error())
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		defer cancel()
		env, err := serverProjectEnv(ctx, client, project)
		fyne.Do(func() {
			if err != nil {
				ui.notice("Environment variables", err.Error())
				return
			}
			ui.envEditor(title, env, func(env map[string]string) {
				go func() {
					ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
					defer cancel()
					if err := saveServerProjectEnv(ctx, client, project, env); err != nil {
						fyne.Do(func() { ui.notice("Environment variables", err.Error()) })
					}
				}()
			})
		})
	}()
}

// serverClient is a client of a paired server: the window's when it works
// on it, else one made from its pairing.
func (ui *desktopUI) serverClient(target projectTarget) (*remoteClient, error) {
	if remote := ui.server; remote != nil && remote.client.address.URL == target.id {
		return remote.client, nil
	}
	saved, ok := pairedServer(target.id)
	if !ok {
		return nil, fmt.Errorf("This server is no longer paired. Connect it again with the target menu under the conversation.")
	}
	return newRemoteClient(saved)
}

// serverProjectEnv reads a server project's variables.
func serverProjectEnv(ctx context.Context, client *remoteClient, project string) (map[string]string, error) {
	var reply remoteapi.ProjectEnv
	if err := client.json(ctx, "GET", "/api/projects/env?path="+url.QueryEscape(project), nil, &reply); err != nil {
		return nil, fmt.Errorf("This server does not keep environment variables (update it): %w", err)
	}
	return reply.Env, nil
}

// saveServerProjectEnv replaces a server project's variables.
func saveServerProjectEnv(ctx context.Context, client *remoteClient, project string, env map[string]string) error {
	if err := client.json(ctx, "POST", "/api/projects/env", remoteapi.ProjectEnv{Path: project, Env: env}, nil); err != nil {
		return fmt.Errorf("The server did not keep the environment variables: %w", err)
	}
	return nil
}
