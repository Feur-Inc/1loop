package ui

// Worktrees: a Git project's other checkouts show under it in the sidebar,
// each with its own chats, and the Repository window lists its worktrees,
// branches, pull requests and issues (internal/gitrepo).

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/githubconnector"
	"1loop/internal/gitrepo"
)

var iconBranch = lineIcon("branch", `<circle cx="5" cy="3.8" r="1.5"/><circle cx="5" cy="12.2" r="1.5"/><circle cx="11" cy="5.6" r="1.5"/><path d="M5 5.3v5.4M11 7.1c0 2.6-6 2-6 3.6"/>`)

// gitSource reads and changes a project's repository where the target
// keeps it: this computer, or the server.
type gitSource interface {
	repository(ctx context.Context, project string) (*gitrepo.Repository, error)
	github(ctx context.Context, project string) (*gitrepo.Hub, map[string]int, error)
	create(ctx context.Context, project string, request gitrepo.NewWorktree) (gitrepo.Created, error)
	remove(ctx context.Context, project string, removal gitrepo.Removal) (string, error)
}

type localGit struct{}

func (localGit) repository(ctx context.Context, project string) (*gitrepo.Repository, error) {
	return gitrepo.Open(ctx, gitrepo.Local, project)
}

func (localGit) github(ctx context.Context, project string) (*gitrepo.Hub, map[string]int, error) {
	repo, err := gitrepo.Open(ctx, gitrepo.Local, project)
	if err != nil {
		return nil, nil, err
	}
	if repo.Owner == "" {
		return nil, nil, fmt.Errorf("This repository has no GitHub remote named origin")
	}
	hub, err := gitrepo.GitHub(ctx, repo.Owner, repo.Name, githubconnector.Default.Authorizations(ctx))
	if err != nil {
		return nil, nil, err
	}
	repo.Link(hub)
	pulls := map[string]int{}
	for _, worktree := range repo.Worktrees {
		if worktree.Pull != nil {
			pulls[worktree.Path] = worktree.Pull.Number
		}
	}
	return hub, pulls, nil
}

func (localGit) create(ctx context.Context, project string, request gitrepo.NewWorktree) (gitrepo.Created, error) {
	return gitrepo.AddWorktree(ctx, gitrepo.Local, project, request)
}

func (localGit) remove(ctx context.Context, project string, removal gitrepo.Removal) (string, error) {
	return gitrepo.RemoveWorktree(ctx, gitrepo.Local, project, removal)
}

type serverGit struct{ client *remoteClient }

func (source serverGit) repository(ctx context.Context, project string) (*gitrepo.Repository, error) {
	var repo gitrepo.Repository
	err := source.client.json(ctx, "GET", "/api/git?project="+url.QueryEscape(project), nil, &repo)
	if err != nil && err.Error() == gitrepo.ErrNotRepository.Error() {
		return nil, gitrepo.ErrNotRepository
	}
	if err != nil {
		return nil, err
	}
	return &repo, nil
}

func (source serverGit) github(ctx context.Context, project string) (*gitrepo.Hub, map[string]int, error) {
	var reply struct {
		Hub           *gitrepo.Hub
		WorktreePulls map[string]int
	}
	if err := source.client.json(ctx, "GET", "/api/git/github?project="+url.QueryEscape(project), nil, &reply); err != nil {
		return nil, nil, err
	}
	return reply.Hub, reply.WorktreePulls, nil
}

func (source serverGit) create(ctx context.Context, project string, request gitrepo.NewWorktree) (gitrepo.Created, error) {
	var created gitrepo.Created
	body := struct {
		Project string `json:"project"`
		gitrepo.NewWorktree
	}{project, request}
	err := source.client.json(ctx, "POST", "/api/git/worktrees", body, &created)
	return created, err
}

func (source serverGit) remove(ctx context.Context, project string, removal gitrepo.Removal) (string, error) {
	var reply struct{ Note string }
	body := struct {
		Project string `json:"project"`
		gitrepo.Removal
	}{project, removal}
	err := source.client.json(ctx, "POST", "/api/git/worktrees/remove", body, &reply)
	return reply.Note, err
}

// repoState is what the window knows of a project's repository.
type repoState struct {
	repo                *gitrepo.Repository
	hub                 *gitrepo.Hub
	pulls               map[string]int // worktree → its pull request
	err, hubErr         string
	at, hubAt           time.Time
	loading, hubLoading bool
}

// gitSource is the target's, nil on an SSH machine (not supported yet).
func (ui *desktopUI) gitSource() gitSource {
	if ui.server != nil {
		return serverGit{ui.server.client}
	}
	if ui.cfg.MachineID != "" {
		return nil
	}
	return localGit{}
}

// worktreeMap is the target's worktrees, folder → project.
func (ui *desktopUI) worktreeMap() map[string]string {
	if ui.server != nil {
		return ui.server.worktrees
	}
	if ui.cfg.MachineID != "" {
		return nil
	}
	return ui.worktrees
}

// projectOf is the project a folder belongs to: itself, or the project of
// the worktree it is.
func (ui *desktopUI) projectOf(folder string) string {
	if project := ui.worktreeMap()[folder]; project != "" {
		return project
	}
	return folder
}

func (ui *desktopUI) repoFor(project string) *repoState {
	key := "local"
	if ui.server != nil {
		key = ui.server.client.address.URL
	}
	key += "\x00" + project
	if ui.repos == nil {
		ui.repos = map[string]*repoState{}
	}
	if ui.repos[key] == nil {
		ui.repos[key] = &repoState{}
	}
	return ui.repos[key]
}

// loadRepo reads a project's repository again if it is older than fresh,
// and its GitHub side too when hub.
func (ui *desktopUI) loadRepo(project string, hub bool, fresh time.Duration) {
	source := ui.gitSource()
	if source == nil || project == "" {
		return
	}
	state := ui.repoFor(project)
	if !state.loading && time.Since(state.at) > fresh {
		state.loading = true
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
			repo, err := source.repository(ctx, project)
			cancel()
			fyne.Do(func() {
				state.loading, state.at = false, time.Now()
				state.repo, state.err = repo, ""
				if err != nil {
					state.err = err.Error()
				}
				if repo != nil && ui.server == nil {
					ui.noteWorktrees(project, repo)
				}
				ui.gitChanged(project)
			})
		}()
	}
	if hub && !state.hubLoading && time.Since(state.hubAt) > max(fresh, time.Minute) && (state.repo == nil || state.repo.Owner != "") {
		state.hubLoading = true
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 45*time.Second)
			found, pulls, err := source.github(ctx, project)
			cancel()
			fyne.Do(func() {
				state.hubLoading, state.hubAt = false, time.Now()
				state.hub, state.pulls, state.hubErr = found, pulls, ""
				if err != nil {
					state.hubErr = err.Error()
				}
				ui.gitChanged(project)
			})
		}()
	}
}

// noteWorktrees keeps this computer's list of worktrees: their chats
// belong to the project, not to a project of their own.
func (ui *desktopUI) noteWorktrees(project string, repo *gitrepo.Repository) {
	changed := false
	if ui.worktrees == nil {
		ui.worktrees = map[string]string{}
	}
	for _, worktree := range repo.Worktrees {
		if !worktree.Main && ui.worktrees[worktree.Path] != project {
			ui.worktrees[worktree.Path] = project
			changed = true
		}
	}
	if changed {
		ui.save()
	}
}

func (ui *desktopUI) gitChanged(project string) {
	ui.renderChats()
	ui.updateHeader()
	if ui.repoWindow != nil && ui.repoWindow.project == project {
		ui.repoWindow.render()
	}
}

// worktreeLabel names a worktree folder: its branch when known.
func (ui *desktopUI) worktreeLabel(folder string) string {
	if state := ui.repoFor(ui.projectOf(folder)); state.repo != nil {
		if worktree := state.repo.Worktree(folder); worktree != nil {
			return worktree.Label()
		}
	}
	return path.Base(filepath.ToSlash(folder))
}

// worktreeRows are a project's other worktrees under it in the sidebar.
func (ui *desktopUI) worktreeRows(project string, open func(folder string)) []fyne.CanvasObject {
	state := ui.repoFor(project)
	if state.repo == nil {
		return nil
	}
	var rows []fyne.CanvasObject
	for _, worktree := range gitrepo.Sorted(state.repo.Worktrees, ui.worktreeSort) {
		if worktree.Main {
			continue
		}
		worktree := worktree
		row := newRow(worktree.Label(), iconBranch, styleNav, func() { open(worktree.Path) })
		row.accent = true
		row.truncate = true
		row.selected = worktree.Path == ui.cfg.Workspace
		if number := state.pulls[worktree.Path]; number > 0 {
			row.trailing = fmt.Sprintf("#%d", number)
		} else if worktree.Missing {
			row.trailing = "missing"
		}
		if ui.worktreeRunning(worktree.Path) {
			row.badge = iconDot // the agent works in it
		}
		rows = append(rows, padded(0, 0, 14, 0, withMore(row, func(anchor fyne.CanvasObject) *sheet {
			return showMenu(ui.window, anchor, ui.worktreeActions(project, worktree, open))
		})))
	}
	add := newRow("New worktree", iconPlus, styleNav, func() { ui.newWorktreeDialog(project) })
	add.ink = colFaint
	rows = append(rows, padded(0, 0, 14, 0, add))
	return rows
}

func (ui *desktopUI) worktreeRunning(folder string) bool {
	if ui.server != nil {
		for _, chat := range ui.server.chats {
			if chat.Workspace == folder && chat.Running {
				return true
			}
		}
		return false
	}
	for _, chat := range ui.chatsIn(folder) {
		if chat.run != nil {
			return true
		}
	}
	return false
}

// selectFolder makes a project or worktree folder the target's current one.
func (ui *desktopUI) selectFolder(folder string) {
	if ui.server != nil {
		ui.server.selectProject(folder)
		return
	}
	ui.selectProject(folder)
}

func (ui *desktopUI) worktreeActions(project string, worktree gitrepo.Worktree, open func(string)) []menuAction {
	state := ui.repoFor(project)
	actions := []menuAction{{label: "New chat", icon: iconPlus, run: func() { open(worktree.Path); ui.startNew() }}}
	if number := state.pulls[worktree.Path]; number > 0 && state.hub != nil {
		actions = append(actions, menuAction{label: fmt.Sprintf("Open pull request #%d", number), icon: iconGlobe, run: func() {
			openLink(fmt.Sprintf("%s/pull/%d", state.hub.URL, number))
		}})
	} else if state.repo != nil && state.repo.Owner != "" && worktree.Branch != "" {
		actions = append(actions, menuAction{label: "Create a pull request on GitHub", icon: iconGlobe, run: func() {
			openLink(fmt.Sprintf("https://github.com/%s/%s/compare/%s?expand=1", state.repo.Owner, state.repo.Name, url.PathEscape(worktree.Branch)))
		}})
	}
	actions = append(actions,
		menuAction{label: "Repository…", icon: iconBranch, run: func() { ui.showRepository(project, "worktrees") }},
		menuAction{label: "Copy path", icon: iconCopy, run: func() { ui.window.Clipboard().SetContent(worktree.Path) }},
		menuAction{label: "Remove worktree…", icon: iconTrash, danger: true, run: func() { ui.removeWorktreeDialog(project, worktree) }})
	return actions
}

// createWorktree makes a worktree, opens it and starts a chat there, with
// draft in the composer when given.
func (ui *desktopUI) createWorktree(project string, request gitrepo.NewWorktree, draft string, done func(error)) {
	source := ui.gitSource()
	if source == nil {
		done(fmt.Errorf("Worktrees are not available on this target"))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 2*time.Minute)
		created, err := source.create(ctx, project, request)
		cancel()
		fyne.Do(func() {
			done(err)
			if err != nil {
				return
			}
			if ui.server == nil {
				if ui.worktrees == nil {
					ui.worktrees = map[string]string{}
				}
				ui.worktrees[created.Path] = project
			} else {
				if ui.server.worktrees == nil {
					ui.server.worktrees = map[string]string{}
				}
				ui.server.worktrees[created.Path] = project
			}
			if ui.repoWindow != nil {
				ui.repoWindow.close()
			}
			ui.selectFolder(created.Path)
			ui.startNew()
			if draft != "" {
				ui.prompt.SetText(draft)
			}
			ui.repoFor(project).at = time.Time{}
			ui.repoFor(project).hubAt = time.Time{}
			ui.loadRepo(project, true, 0)
		})
	}()
}

func (ui *desktopUI) newWorktreeDialog(project string) {
	state := ui.repoFor(project)
	name := widget.NewEntry()
	name.SetPlaceHolder("feature/my-change")
	base := newPicker(ui.window, "Default branch", styleField, nil)
	var options []string
	defaultLabel := "Default branch"
	if state.repo != nil && state.repo.DefaultBranch != "" {
		defaultLabel = state.repo.DefaultBranch + " (latest)"
	}
	options = append(options, defaultLabel)
	if state.repo != nil {
		for _, branch := range state.repo.Branches {
			if branch.Name != state.repo.DefaultBranch {
				options = append(options, branch.Name)
			}
		}
	}
	base.Options = options
	base.SetSelected(defaultLabel)
	problem := note("")
	var dialog *sheet
	var create *tapRow
	submit := func() {
		request := gitrepo.NewWorktree{Kind: "new", Branch: strings.TrimSpace(name.Text)}
		if base.Selected != defaultLabel {
			request.Base = base.Selected
		}
		if request.Branch == "" {
			problem.SetText("Name the new branch.")
			return
		}
		create.Disable()
		problem.SetText("Creating the worktree…")
		ui.createWorktree(project, request, "", func(err error) {
			if err != nil {
				create.Enable()
				problem.SetText(err.Error())
				return
			}
			dialog.Hide()
		})
	}
	name.OnSubmitted = func(string) { submit() }
	create = newRow("Create", nil, stylePrimary, submit)
	body := container.New(layout.NewCustomPaddedVBoxLayout(8),
		note("A new branch, checked out in its own folder of the project ("+gitrepo.Folder+"/), with its own chats. The project's folder keeps its branch."),
		textLine("Branch", 13, colMuted), name, textLine("Starts from", 13, colMuted), base.row, problem)
	dialog = ui.modal("New worktree · "+ui.projectName(project), body, 480, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }), create)
	ui.window.Canvas().Focus(name)
}

func (ui *desktopUI) removeWorktreeDialog(project string, worktree gitrepo.Worktree) {
	source := ui.gitSource()
	if source == nil {
		return
	}
	// Its changes as they are now, not as last listed.
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		repo, err := source.repository(ctx, project)
		cancel()
		fyne.Do(func() {
			if err == nil {
				if fresh := repo.Worktree(worktree.Path); fresh != nil {
					worktree = *fresh
				}
			}
			ui.confirmRemoveWorktree(source, project, worktree)
		})
	}()
}

func (ui *desktopUI) confirmRemoveWorktree(source gitSource, project string, worktree gitrepo.Worktree) {
	chats := 0
	if ui.server != nil {
		for _, chat := range ui.server.chats {
			if chat.Workspace == worktree.Path {
				chats++
			}
		}
	} else {
		chats = len(ui.chatsIn(worktree.Path))
	}
	text := fmt.Sprintf("The folder %s will be deleted.", worktree.Path)
	if chats > 0 {
		text += fmt.Sprintf(" Its %d chat(s) will be removed from 1loop.", chats)
	}
	if worktree.Changes > 0 {
		text += fmt.Sprintf(" It has %d uncommitted change(s): they will be lost.", worktree.Changes)
	}
	deleteBranch := false
	body := container.New(layout.NewCustomPaddedVBoxLayout(10), note(text))
	if worktree.Branch != "" {
		body.Add(card(settingRow("Also delete the branch "+worktree.Branch, "Only if it is merged, unless it has uncommitted changes being discarded.", newToggle(false, func(on bool) { deleteBranch = on }))))
	}
	problem := note("")
	body.Add(problem)
	var dialog *sheet
	var remove *tapRow
	remove = newRow("Remove", nil, stylePrimary, func() {
		remove.Disable()
		removal := gitrepo.Removal{Path: worktree.Path, Force: worktree.Changes > 0, DeleteBranch: deleteBranch}
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, time.Minute)
			note, err := source.remove(ctx, project, removal)
			cancel()
			fyne.Do(func() {
				if err != nil {
					remove.Enable()
					problem.SetText(err.Error())
					return
				}
				dialog.Hide()
				ui.forgetWorktree(project, worktree.Path)
				if note != "" {
					ui.notice("Worktree removed", note)
				}
			})
		}()
	})
	remove.danger = true
	dialog = ui.modal("Remove worktree "+worktree.Label()+"?", body, 500, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }), remove)
}

// forgetWorktree drops a removed worktree and, on this computer, its chats
// (the server drops its own).
func (ui *desktopUI) forgetWorktree(project, folder string) {
	if ui.server != nil {
		delete(ui.server.worktrees, folder)
		if ui.cfg.Workspace == folder {
			ui.current = nil
			ui.selectFolder(project)
			ui.startNew()
		}
		ui.repoFor(project).at = time.Time{}
		ui.loadRepo(project, false, 0)
		ui.renderChats()
		return
	}
	ui.forgetLocalWorktree(project, folder)
}

// forgetLocalWorktree drops one of this computer's worktrees and its chats.
func (ui *desktopUI) forgetLocalWorktree(project, folder string) {
	var kept []*conversation
	for _, chat := range ui.chats {
		if chat.Config.Workspace == folder && chat.Config.MachineID == "" {
			if chat.run != nil {
				chat.run.cancel()
			}
			chat.tasks.StopAll()
			chat.disposePanel()
			continue
		}
		kept = append(kept, chat)
	}
	ui.chats = kept
	delete(ui.worktrees, folder)
	ui.projects = slices.DeleteFunc(ui.projects, func(path string) bool { return path == folder })
	ui.save()
	if ui.server == nil {
		if ui.current != nil && ui.current.Config.Workspace == folder || ui.cfg.Workspace == folder {
			ui.current = nil
			ui.selectProject(project)
			ui.startNew()
		}
		ui.repoFor(project).at = time.Time{}
		ui.loadRepo(project, false, 0)
	}
	ui.renderChats()
}

// phoneGitTarget serves this computer's repositories to the phone.
type phoneGitTarget struct{ server *remoteServer }

func (target phoneGitTarget) Project(folder string) (string, error) {
	registered := false
	target.server.onUI(func() { registered = slices.Contains(target.server.ui.projects, folder) })
	if !registered {
		return "", fmt.Errorf("Choose a registered project")
	}
	return folder, nil
}

func (target phoneGitTarget) Opened(project, worktree string) {
	target.server.onUI(func() {
		ui := target.server.ui
		if ui.worktrees[worktree] == project {
			return
		}
		if ui.worktrees == nil {
			ui.worktrees = map[string]string{}
		}
		ui.worktrees[worktree] = project
		ui.save()
		ui.renderChats()
	})
}

func (target phoneGitTarget) Removed(project, worktree string) {
	target.server.onUI(func() { target.server.ui.forgetLocalWorktree(project, worktree) })
}

// notRepository says the project is not a Git repository.
func (state *repoState) notRepository() bool {
	return state.err == gitrepo.ErrNotRepository.Error()
}

// repositoryActions are a Git project's entries in its menu.
func (ui *desktopUI) repositoryActions(project string) []menuAction {
	state := ui.repoFor(project)
	if ui.gitSource() == nil || state.repo == nil {
		return nil
	}
	return []menuAction{
		{label: "New worktree…", icon: iconBranch, run: func() { ui.newWorktreeDialog(project) }},
		{label: "Repository…", icon: iconBranch, run: func() { ui.showRepository(project, "worktrees") }},
	}
}

// worktreeChoices are a project's other worktrees in the folder menu.
func (ui *desktopUI) worktreeChoices(project string, open func(string)) []menuAction {
	state := ui.repoFor(project)
	if state.repo == nil {
		return nil
	}
	var actions []menuAction
	for _, worktree := range gitrepo.Sorted(state.repo.Worktrees, ui.worktreeSort) {
		if worktree.Main || worktree.Missing {
			continue
		}
		folder := worktree.Path
		icon := iconBranch
		if folder == ui.cfg.Workspace {
			icon = iconCheck
		}
		actions = append(actions, menuAction{label: "    " + worktree.Label(), icon: icon, run: func() { open(folder) }})
	}
	return actions
}
