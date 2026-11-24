// SPDX-License-Identifier: MPL-2.0

package ui

// The New project dialog (#408), Add project's single step:
//
//	Location   This computer ▾            (when there is more than one)
//	Name       [            ]
//	Folders                        [Add]  (this computer)
//	  ~/dev/app · main            ☐ Worktree
//	Environment variables  None   [Edit]
//	                       [Cancel] [Create]
//
// On this computer, with no folder, Create makes a folder of that name
// beside the last project; with folders, each one is a project, and a
// checked Worktree makes a new worktree of it on the branch Name. On an
// SSH machine or a server, Name is a folder of its projects folder, made
// there unless it exists.

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/gitrepo"
)

// projectForm is the New project dialog while it is open.
type projectForm struct {
	ui        *desktopUI
	target    projectTarget
	location  *picker
	byOption  map[string]projectTarget
	name      *widget.Entry
	folders   []*formFolder
	list      *fyne.Container // the folder rows
	foldersAt *fyne.Container // the Folders section, shown on this computer
	hint      *flowText       // what Create does
	env       map[string]string
	envDetail *flowText
	problem   *flowText
	create    *tapRow
	dialog    *sheet
	// named: the user typed the name, so the first folder does not set it.
	named bool
}

// formFolder is a folder of the project, as the dialog lists it.
type formFolder struct {
	path     string
	branch   *flowText
	worktree *widget.Check
	row      fyne.CanvasObject
}

// addProjectOn opens the New project dialog with target chosen.
func (ui *desktopUI) addProjectOn(target projectTarget) { ui.newProjectForm(target) }

// newProjectForm opens the New project dialog and returns it.
func (ui *desktopUI) newProjectForm(target projectTarget) *projectForm {
	form := &projectForm{ui: ui, target: target, byOption: map[string]projectTarget{}}
	form.show()
	return form
}

func (form *projectForm) show() {
	ui := form.ui
	body := container.New(layout.NewCustomPaddedVBoxLayout(8))
	if targets := ui.addTargets(); len(targets) > 1 {
		form.location = newPicker(ui.window, "Location", styleField, func(option string) {
			if target, ok := form.byOption[option]; ok {
				form.setTarget(target)
			}
		})
		form.location.Details = map[string]string{}
		for _, target := range targets {
			name, detail, _ := ui.targetName(target)
			option := name
			if _, taken := form.byOption[option]; taken {
				option = name + " (" + detail + ")"
			}
			form.byOption[option] = target
			form.location.Options = append(form.location.Options, option)
			form.location.Details[option] = detail
			if target == form.target {
				form.location.Selected = option // shown, not yet applied: the form is not built
				form.location.refresh()
			}
		}
		body.Add(fieldLabel("Location"))
		body.Add(form.location.row)
	}
	form.name = widget.NewEntry()
	form.name.SetPlaceHolder("Project name")
	form.name.OnChanged = func(string) {
		form.named = true
		form.refresh()
	}
	form.name.OnSubmitted = func(string) { form.submit() }
	body.Add(fieldLabel("Name"))
	body.Add(form.name)

	form.list = container.New(layout.NewCustomPaddedVBoxLayout(4))
	add := newRow("Add", iconPlus, styleChip, form.addFolder)
	form.foldersAt = container.New(layout.NewCustomPaddedVBoxLayout(6),
		container.NewBorder(nil, nil, nil, add, container.NewVBox(layout.NewSpacer(), fieldLabel("Folders (optional)"), layout.NewSpacer())),
		form.list)
	body.Add(form.foldersAt)

	form.envDetail = plainFlow(envSummary(nil), 12.5, colMuted)
	edit := newRow("Edit", nil, styleOutline, form.editEnv)
	body.Add(container.NewBorder(nil, nil, nil, container.NewCenter(edit),
		container.New(layout.NewCustomPaddedVBoxLayout(2), plainFlow("Environment variables", 13.5, colText), form.envDetail)))

	form.hint = plainFlow("", 12.5, colMuted)
	form.problem = plainFlow("", 12.5, colDanger)
	body.Add(form.hint)
	body.Add(form.problem)

	cancel := newRow("Cancel", nil, styleOutline, func() { form.dialog.dismiss() })
	form.create = newRow("Create", nil, stylePrimary, form.submit)
	form.dialog = ui.modal("New project", body, 520, nil, cancel, form.create)
	form.setTarget(form.target)
	ui.window.Canvas().Focus(form.name)
}

// fieldLabel names a field of the dialog.
func fieldLabel(text string) fyne.CanvasObject { return textLine(text, 13, colMuted) }

// setTarget shows what the dialog asks on a target: the folders of this
// computer are listed only there.
func (form *projectForm) setTarget(target projectTarget) {
	form.target = target
	if target.kind == targetLocal {
		form.foldersAt.Show()
	} else {
		form.foldersAt.Hide()
	}
	form.refresh()
}

// worktrees are the folders whose Worktree box is checked.
func (form *projectForm) worktrees() (folders []*formFolder) {
	for _, folder := range form.folders {
		if folder.worktree.Checked {
			folders = append(folders, folder)
		}
	}
	return folders
}

// local says whether Create adds folders of this computer as they are.
func (form *projectForm) local() bool {
	return form.target.kind == targetLocal && len(form.folders) > 0
}

// newFolderBase is where Create makes a new folder of this computer: beside
// the last project, else in the home folder.
func (form *projectForm) newFolderBase() string {
	home, _ := os.UserHomeDir()
	if form.ui.localWorkspace == "" && len(form.ui.projects) == 0 {
		return home
	}
	return filepath.Dir(form.ui.lastLocalWorkspace())
}

// refresh says what Create will do.
func (form *projectForm) refresh() {
	name := strings.TrimSpace(form.name.Text)
	if name == "" {
		name = "…"
	}
	var hint string
	switch {
	case form.target.kind == targetSSH:
		label, projects := form.ui.targetLabelOf(form.target.id), "its projects folder"
		if settings, ok := form.ui.machineSettings(form.target.id); ok && settings.Projects != "" {
			projects = settings.Projects
		}
		hint = "Creates the folder " + name + " in " + projects + " on " + label + ", or opens it if it exists."
	case form.target.kind == targetServer:
		hint = "Creates the folder " + name + " in the projects folder of " + serverName(form.target.id) + ", or opens it if it exists."
	case len(form.folders) == 0:
		hint = "Creates the folder " + filepath.Join(form.newFolderBase(), name) + ", or opens it if it exists. Or add existing folders."
	default:
		hint = fmt.Sprintf("Adds %d folder(s) as projects.", len(form.folders))
		if len(form.worktrees()) > 0 {
			hint += " A checked Worktree makes a new worktree of its folder on the branch " + name + ", where the chat starts."
		}
	}
	setFlow(form.hint, hint)
}

// addFolder picks folders of this computer for the project.
func (form *projectForm) addFolder() {
	ui := form.ui
	start := ui.lastLocalWorkspace()
	if len(form.folders) > 0 {
		start = form.folders[len(form.folders)-1].path
	}
	ui.pick(pickRequest{title: "Add a project folder", start: filepath.Dir(start), folder: true}, func(paths []string) {
		for _, picked := range paths {
			root, err := agent.ResolveWorkspace(picked)
			if err != nil {
				setFlow(form.problem, err.Error())
				continue
			}
			form.addFolderRow(root)
		}
	})
}

// addFolderRow lists a folder with its branch, read in the background.
func (form *projectForm) addFolderRow(root string) {
	for _, folder := range form.folders {
		if folder.path == root {
			return
		}
	}
	folder := &formFolder{path: root, branch: plainFlow("Reading its branch…", 12, colMuted)}
	folder.worktree = widget.NewCheck("Worktree", func(bool) { form.refresh() })
	folder.worktree.Disable()
	remove := newIconButton(iconClose, color.NRGBA{}, colMuted, nil)
	remove.onTap = func() {
		for i, other := range form.folders {
			if other == folder {
				form.folders = append(form.folders[:i], form.folders[i+1:]...)
				break
			}
		}
		form.list.Remove(folder.row)
		form.refresh()
	}
	text := container.New(layout.NewCustomPaddedVBoxLayout(1), plainFlow(root, 13, colText), folder.branch)
	folder.row = container.NewBorder(nil, nil, nil,
		container.NewHBox(container.NewCenter(folder.worktree), container.NewCenter(remove)), text)
	form.folders = append(form.folders, folder)
	form.list.Add(folder.row)
	if !form.named || strings.TrimSpace(form.name.Text) == "" {
		form.name.OnChanged = nil
		form.name.SetText(filepath.Base(root))
		form.name.OnChanged = func(string) { form.named = true; form.refresh() }
	}
	form.refresh()
	go func() {
		ctx, cancel := context.WithTimeout(form.ui.ctx, 10*time.Second)
		defer cancel()
		branch, err := folderBranch(ctx, root)
		fyne.Do(func() {
			switch {
			case err != nil:
				setFlow(folder.branch, "Not a Git repository")
			case branch == "":
				setFlow(folder.branch, "No branch (detached)")
				folder.worktree.Enable()
			default:
				setFlow(folder.branch, "Branch "+branch)
				folder.worktree.Enable()
			}
		})
	}()
}

// folderBranch is the branch checked out in a folder of this computer, ""
// when none is; an error when it is not in a repository.
func folderBranch(ctx context.Context, folder string) (string, error) {
	if _, err := gitrepo.Local.Git(ctx, folder, "rev-parse", "--git-dir"); err != nil {
		return "", err
	}
	branch, err := gitrepo.Local.Git(ctx, folder, "branch", "--show-current")
	return strings.TrimSpace(branch), err
}

// editEnv opens the variables in their editor, over the dialog.
func (form *projectForm) editEnv() {
	form.ui.envEditor("Environment variables", form.env, func(env map[string]string) {
		form.env = env
		setFlow(form.envDetail, envSummary(env))
	})
}

// folderName is a valid folder name, or an error saying why not.
func folderName(text string) (string, error) {
	name := strings.TrimSpace(text)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") {
		return "", errors.New("Enter a folder name, without / or \\.")
	}
	return name, nil
}

// branchName is the name a new worktree's branch gets from the project's.
func branchName(name string) string { return strings.Join(strings.Fields(name), "-") }

// madeProject is what Create made: the projects to keep, and the folder
// whose chat opens (a project, or a new worktree of the first).
type madeProject struct {
	projects  []string
	open      string
	worktrees map[string]string // a new worktree's folder → its project
}

// submit makes the project on its target, off the interface's thread.
func (form *projectForm) submit() {
	name, err := folderName(form.name.Text)
	if err != nil && (!form.local() || len(form.worktrees()) > 0) {
		setFlow(form.problem, err.Error())
		return
	}
	run, err := form.maker(name)
	if err != nil {
		setFlow(form.problem, err.Error())
		return
	}
	setFlow(form.problem, "")
	form.create.Disable()
	target, env := form.target, form.env
	go func() {
		ctx, cancel := context.WithTimeout(form.ui.ctx, 2*time.Minute)
		defer cancel()
		made, err := run(ctx)
		fyne.Do(func() {
			form.create.Enable()
			if err != nil {
				setFlow(form.problem, err.Error())
				return
			}
			form.dialog.Hide()
			form.ui.projectMade(target, made, env)
		})
	}()
}

// maker is what Create runs for the dialog's target.
func (form *projectForm) maker(name string) (func(context.Context) (madeProject, error), error) {
	ui := form.ui
	env := form.env
	switch form.target.kind {
	case targetSSH:
		machine := ui.machineFor(form.target.id)
		if machine == nil {
			return nil, errors.New("This SSH machine is no longer in Settings → General")
		}
		return func(ctx context.Context) (madeProject, error) {
			folder, err := machine.ProjectsFolder(ctx)
			project := path.Join(folder, name)
			if err == nil {
				err = machine.MkdirAll(ctx, project)
			}
			return madeProject{projects: []string{project}, open: project}, err
		}, nil
	case targetServer:
		client, err := ui.serverClient(form.target)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (madeProject, error) {
			var reply struct{ Workspace string }
			err := client.json(ctx, "POST", "/api/projects", map[string]any{"path": name}, &reply)
			if err != nil { // not there yet: made
				err = client.json(ctx, "POST", "/api/projects", map[string]any{"path": name, "create": true}, &reply)
			}
			if err == nil && reply.Workspace == "" {
				err = errors.New("The server did not say where the project is")
			}
			if err == nil && len(env) > 0 { // kept on the server, with the project
				err = saveServerProjectEnv(ctx, client, reply.Workspace, env)
			}
			return madeProject{projects: []string{reply.Workspace}, open: reply.Workspace}, err
		}, nil
	}
	if len(form.folders) == 0 {
		folder := filepath.Join(form.newFolderBase(), name)
		return func(context.Context) (madeProject, error) {
			if err := os.MkdirAll(folder, 0o755); err != nil {
				return madeProject{}, err
			}
			root, err := agent.ResolveWorkspace(folder)
			return madeProject{projects: []string{root}, open: root}, err
		}, nil
	}
	type folderJob struct {
		path     string
		worktree bool
	}
	var jobs []folderJob
	for _, folder := range form.folders {
		jobs = append(jobs, folderJob{folder.path, folder.worktree.Checked})
	}
	branch := branchName(name)
	return func(ctx context.Context) (madeProject, error) {
		made := madeProject{worktrees: map[string]string{}}
		for _, job := range jobs {
			made.projects = append(made.projects, job.path)
			open := job.path
			if job.worktree {
				created, err := localGit{}.create(ctx, job.path, gitrepo.NewWorktree{Kind: "new", Branch: branch})
				if err != nil {
					return made, fmt.Errorf("%s: %w", filepath.Base(job.path), err)
				}
				made.worktrees[created.Path] = job.path
				open = created.Path
			}
			if made.open == "" {
				made.open = open
			}
		}
		return made, nil
	}, nil
}

// projectMade keeps what Create made, and opens its first folder.
func (ui *desktopUI) projectMade(target projectTarget, made madeProject, env map[string]string) {
	ui.keepMade(target, made, env)
	switch target.kind {
	case targetSSH:
		ui.setReach(target, reachOnline, "")
	case targetServer:
		ui.setReach(target, reachOnline, "")
		if link := ui.linkTo(target.id); link != nil { // its listing is read again, at once
			select {
			case link.wake <- struct{}{}:
			default:
			}
		}
		if remote := ui.server; remote != nil && remote.client.address.URL == target.id {
			remote.notify()
		}
	}
	if made.worktrees[made.open] != "" {
		ui.save()
		ui.openProjectOn(target, made.open)
		ui.startNew()
		return
	}
	ui.projectAdded(target, made.open)
}

// keepMade keeps what Create made under its target, with its variables (a
// server's are already there), and its new worktrees under their projects.
func (ui *desktopUI) keepMade(target projectTarget, made madeProject, env map[string]string) {
	for _, project := range made.projects {
		ui.keepProjectOn(target, project)
		if target.kind != targetServer {
			ui.keepProjectEnv(target, project, env)
		}
	}
	if len(made.worktrees) == 0 {
		return
	}
	if ui.worktrees == nil {
		ui.worktrees = map[string]string{}
	}
	for folder, project := range made.worktrees {
		ui.worktrees[folder] = project
		ui.repoFor(project).at = time.Time{}
	}
}
