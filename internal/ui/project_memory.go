// SPDX-License-Identifier: MPL-2.0

package ui

// Project memory: a project's AGENTS.md, the instructions every agent reads
// there and where the agent saves what it learns about the project. It is
// edited from the project's … menu in the sidebar, where the project is:
// this computer, an SSH machine, or the paired server. It is a file of the
// project, so it is written only with Save, and refused when it changed
// since it was read.

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// memoryFile is where a project's AGENTS.md is read and saved.
type memoryFile struct {
	path string // shown in the dialog
	read func(context.Context) (text string, exists bool, err error)
	save func(ctx context.Context, before, after string) error
}

// projectMemoryAction is the sidebar menu's entry for a project.
func (ui *desktopUI) projectMemoryAction(project string) menuAction {
	return menuAction{label: "Project memory…", icon: iconDocument, run: func() { ui.editProjectMemory(project) }}
}

// projectMemoryFile is the project's AGENTS.md where the agent works now.
func (ui *desktopUI) projectMemoryFile(project string) memoryFile {
	if ui.server != nil {
		client := ui.server.client
		query := "/api/projects/memory?workspace=" + url.QueryEscape(project)
		file := memoryFile{path: path.Join(project, "AGENTS.md")}
		file.read = func(ctx context.Context) (string, bool, error) {
			var reply struct {
				Text   string
				Exists bool
				Path   string
			}
			err := client.json(ctx, "GET", query, nil, &reply)
			return reply.Text, reply.Exists, err
		}
		file.save = func(ctx context.Context, before, after string) error {
			return client.json(ctx, "POST", query, map[string]string{"before": before, "after": after}, nil)
		}
		return file
	}
	if machine := ui.machineFor(ui.cfg.MachineID); machine != nil {
		return memoryFile{
			path: machine.Label() + ":" + path.Join(project, "AGENTS.md"),
			read: func(ctx context.Context) (string, bool, error) {
				return agent.MachineProjectMemory(ctx, machine, project)
			},
			save: func(ctx context.Context, before, after string) error {
				return agent.SaveMachineProjectMemory(ctx, machine, project, before, after)
			},
		}
	}
	shown := filepath.Join(project, "AGENTS.md")
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(shown, home+string(filepath.Separator)) {
		shown = "~" + strings.TrimPrefix(shown, home)
	}
	return memoryFile{
		path: shown,
		read: func(context.Context) (string, bool, error) {
			text, err := agent.ProjectMemory(project)
			return text, agent.ProjectMemoryExists(project), err
		},
		save: func(_ context.Context, before, after string) error {
			return agent.SaveProjectMemory(project, before, after)
		},
	}
}

// editProjectMemory reads the project's AGENTS.md, off the window's thread
// since it may be on a machine or the server, then opens its editor.
func (ui *desktopUI) editProjectMemory(project string) {
	file := ui.projectMemoryFile(project)
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 15*time.Second)
		defer cancel()
		saved, exists, err := file.read(ctx)
		fyne.Do(func() {
			if err != nil {
				ui.notice("Project memory", file.path+": "+err.Error())
				return
			}
			ui.projectMemoryDialog(project, file, saved, exists)
		})
	}()
}

// projectMemoryDialog is the editor, with Save and Cancel.
func (ui *desktopUI) projectMemoryDialog(project string, file memoryFile, saved string, exists bool) {
	var dialog *sheet
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(14)
	entry.SetText(saved)
	entry.SetPlaceHolder("Nothing saved yet: build and test commands, layout, the project's rules…")
	about := file.path + " · read by 1loop and the other agents in this project."
	if !exists {
		about = file.path + " does not exist yet: Save creates it. 1loop and the other agents read it in this project."
	}
	status := note("")
	status.Hide()
	save := newRow("Save", nil, stylePrimary, nil)
	save.onTap = func() {
		text := strings.TrimRight(entry.Text, "\n")
		if text != "" {
			text += "\n"
		}
		save.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 15*time.Second)
			defer cancel()
			err := file.save(ctx, saved, text)
			fyne.Do(func() {
				if err != nil {
					status.SetText(err.Error())
					status.Show()
					save.Enable()
					return
				}
				dialog.dismiss()
			})
		}()
	}
	save.Disable()
	entry.OnChanged = func(text string) {
		if text == saved {
			save.Disable()
		} else {
			save.Enable()
		}
	}
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Project memory · "+ui.projectName(project), container.NewVBox(note(about), entry, status), 680, nil, cancel, save)
	ui.window.Canvas().Focus(entry)
}
