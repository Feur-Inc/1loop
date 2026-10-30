package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/gitrepo"
)

// repoWindow lists a project's worktrees, branches, pull requests and
// issues, and opens any of them in a worktree.
type repoWindow struct {
	ui      *desktopUI
	project string
	tab     string
	sheet   *sheet
	tabs    map[string]*tapRow
	header  *fyne.Container
	body    *fyne.Container
}

var repoTabs = []struct{ id, label string }{{"worktrees", "Worktrees"}, {"branches", "Branches"}, {"pulls", "Pull requests"}, {"issues", "Issues"}}

var sortLabels = map[string]string{gitrepo.SortRecent: "Recent", gitrepo.SortName: "Name", gitrepo.SortPull: "Pull request"}

func (ui *desktopUI) showRepository(project, tab string) {
	if ui.gitSource() == nil {
		ui.notice("Repository", "Worktrees are not available on an SSH machine yet.")
		return
	}
	if ui.repoWindow != nil {
		ui.repoWindow.close()
	}
	window := &repoWindow{ui: ui, project: project, tab: tab, tabs: map[string]*tapRow{},
		header: container.New(layout.NewCustomPaddedVBoxLayout(4)), body: container.New(layout.NewCustomPaddedVBoxLayout(8))}
	bar := container.New(layout.NewCustomPaddedHBoxLayout(6))
	for _, entry := range repoTabs {
		entry := entry
		row := newRow(entry.label, nil, styleChip, func() { window.tab = entry.id; window.render() })
		window.tabs[entry.id] = row
		bar.Add(row)
	}
	refresh := newRow("Refresh", iconReload, styleChip, func() {
		state := ui.repoFor(project)
		state.at, state.hubAt = time.Time{}, time.Time{}
		ui.loadRepo(project, true, 0)
		window.render()
	})
	scroll := container.NewVScroll(window.body)
	scroll.SetMinSize(fyne.NewSize(860, 440))
	content := container.NewBorder(container.NewVBox(window.header, padded(10, 8, 0, 0, container.NewBorder(nil, nil, nil, refresh, bar))), nil, nil, nil, scroll)
	ui.repoWindow = window
	window.sheet = ui.modal("Repository · "+ui.projectName(project), content, 900, func() {
		if ui.repoWindow == window {
			ui.repoWindow = nil
		}
	})
	window.render()
	ui.loadRepo(project, true, 5*time.Second)
}

func (window *repoWindow) close() {
	if window.ui.repoWindow == window {
		window.ui.repoWindow = nil
	}
	window.sheet.dismiss()
}

func (window *repoWindow) render() {
	ui, state := window.ui, window.ui.repoFor(window.project)
	window.header.RemoveAll()
	switch {
	case state.repo == nil && state.loading || state.repo == nil && state.err == "":
		window.header.Add(note("Reading the repository…"))
	case state.repo == nil && state.notRepository():
		window.header.Add(note("This project is not a Git repository."))
	case state.repo == nil:
		window.header.Add(note(state.err))
	default:
		repo := state.repo
		name := ui.projectName(window.project)
		if repo.Owner != "" {
			name = repo.Owner + "/" + repo.Name
		}
		parts := []string{name}
		if repo.Current != "" {
			parts = append(parts, "this folder on "+repo.Current)
		}
		if repo.DefaultBranch != "" {
			parts = append(parts, "default branch "+repo.DefaultBranch)
		}
		window.header.Add(plainFlow(strings.Join(parts, " · "), 14, colText))
		var about []string
		if state.hub != nil && state.hub.Description != "" {
			about = append(about, state.hub.Description)
		}
		if repo.Remote != "" {
			about = append(about, "origin "+repo.Remote)
		}
		if len(about) > 0 {
			window.header.Add(plainFlow(strings.Join(about, " · "), 12.5, colMuted))
		}
	}
	counts := map[string]int{}
	if state.repo != nil {
		counts["worktrees"], counts["branches"] = len(state.repo.Worktrees), len(state.repo.Branches)
	}
	if state.hub != nil {
		counts["pulls"], counts["issues"] = len(state.hub.Pulls), len(state.hub.Issues)
	}
	for _, entry := range repoTabs {
		row := window.tabs[entry.id]
		label := entry.label
		if count, ok := counts[entry.id]; ok {
			label += fmt.Sprintf("  %d", count)
		}
		row.text = label
		row.SetSelected(entry.id == window.tab)
	}
	window.body.RemoveAll()
	if state.repo == nil {
		window.body.Refresh()
		return
	}
	switch window.tab {
	case "branches":
		window.branches(state)
	case "pulls":
		window.pulls(state)
	case "issues":
		window.issues(state)
	default:
		window.worktrees(state)
	}
	window.body.Refresh()
}

func controls(rows ...fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedHBoxLayout(6), rows...)
}

func chip(label string, run func()) *tapRow { return newRow(label, nil, styleChip, run) }

// open makes a worktree the current folder and closes the window.
func (window *repoWindow) open(folder string, newChat bool) {
	window.close()
	window.ui.selectFolder(folder)
	if newChat {
		window.ui.startNew()
	}
}

func (window *repoWindow) create(request gitrepo.NewWorktree, draft string, button *tapRow) {
	button.Disable()
	button.text = "Creating…"
	button.Refresh()
	window.ui.createWorktree(window.project, request, draft, func(err error) {
		if err != nil {
			button.Enable()
			window.ui.notice("Worktree", err.Error())
			window.render()
		}
	})
}

func (window *repoWindow) worktrees(state *repoState) {
	ui := window.ui
	current := ui.worktreeSort
	if sortLabels[current] == "" {
		current = gitrepo.SortRecent
	}
	order := newPicker(ui.window, "Sort", styleChip, nil)
	order.Options = []string{sortLabels[gitrepo.SortRecent], sortLabels[gitrepo.SortName], sortLabels[gitrepo.SortPull]}
	order.SetSelected(sortLabels[current])
	// Set after the first choice: SetSelected calls it, and it redraws the list.
	order.onChanged = func(choice string) {
		for id, label := range sortLabels {
			if label == choice && id != ui.worktreeSort {
				ui.worktreeSort = id
				ui.save()
				ui.renderChats()
				window.render()
			}
		}
	}
	add := newRow("New worktree", iconPlus, stylePrimary, func() { ui.newWorktreeDialog(window.project) })
	window.body.Add(container.NewBorder(nil, nil, container.NewHBox(textLine("Sort by", 13, colMuted), order.row), add))
	var rows []fyne.CanvasObject
	for _, worktree := range gitrepo.Sorted(state.repo.Worktrees, ui.worktreeSort) {
		worktree := worktree
		title := worktree.Label()
		if worktree.Main {
			title += " · the project's folder"
		}
		var detail []string
		detail = append(detail, worktree.Path)
		var status []string
		switch {
		case worktree.Missing:
			status = append(status, "folder missing")
		case worktree.Changes > 0:
			status = append(status, fmt.Sprintf("%d uncommitted change(s)", worktree.Changes))
		default:
			status = append(status, "clean")
		}
		if worktree.Ahead > 0 || worktree.Behind > 0 {
			status = append(status, fmt.Sprintf("↑%d ↓%d %s", worktree.Ahead, worktree.Behind, worktree.Upstream))
		}
		if worktree.Subject != "" {
			status = append(status, "“"+worktree.Subject+"” "+ago(worktree.Updated))
		}
		detail = append(detail, strings.Join(status, " · "))
		if number := state.pulls[worktree.Path]; number > 0 && state.hub != nil {
			for _, pull := range state.hub.Pulls {
				if pull.Number == number {
					detail = append(detail, pullLine(pull))
				}
			}
		}
		buttons := []fyne.CanvasObject{chip("Open", func() { window.open(worktree.Path, false) }), chip("New chat", func() { window.open(worktree.Path, true) })}
		if !worktree.Main {
			remove := newRow("", iconTrash, styleChip, func() { ui.removeWorktreeDialog(window.project, worktree) })
			buttons = append(buttons, remove)
		}
		rows = append(rows, settingRow(title, strings.Join(detail, " · "), controls(buttons...)))
	}
	window.body.Add(card(rows...))
}

func (window *repoWindow) branches(state *repoState) {
	var rows []fyne.CanvasObject
	for _, branch := range state.repo.Branches {
		branch := branch
		title := branch.Name
		var detail []string
		if branch.Remote {
			detail = append(detail, "remote branch")
		}
		if branch.Subject != "" {
			detail = append(detail, "“"+branch.Subject+"” "+ago(branch.Updated))
		}
		if branch.Ahead > 0 || branch.Behind > 0 {
			detail = append(detail, fmt.Sprintf("↑%d ↓%d %s", branch.Ahead, branch.Behind, branch.Upstream))
		}
		var action *tapRow
		if branch.Worktree != "" {
			detail = append(detail, "checked out in "+branch.Worktree)
			action = chip("Open", func() { window.open(branch.Worktree, false) })
		} else {
			action = chip("Open in a worktree", nil)
			action.onTap = func() { window.create(gitrepo.NewWorktree{Kind: "branch", Branch: branch.Name}, "", action) }
		}
		rows = append(rows, settingRow(title, strings.Join(detail, " · "), action))
	}
	if len(rows) == 0 {
		window.body.Add(note("No branches yet."))
		return
	}
	window.body.Add(card(rows...))
}

func (window *repoWindow) hubProblem(state *repoState) bool {
	switch {
	case state.repo.Owner == "":
		window.body.Add(note("This repository has no GitHub remote named origin."))
	case state.hub == nil && state.hubErr != "":
		window.body.Add(note(state.hubErr))
	case state.hub == nil:
		window.body.Add(note("Reading GitHub…"))
	default:
		if !state.hub.Signed {
			window.body.Add(note("Read without a GitHub account: checks are unknown and private repositories are hidden. Connect GitHub in Settings → Connectors."))
		}
		return false
	}
	return true
}

func pullLine(pull gitrepo.Pull) string {
	parts := []string{fmt.Sprintf("#%d %s", pull.Number, pull.Title)}
	switch pull.Checks {
	case "success":
		parts = append(parts, "checks passed")
	case "failure":
		parts = append(parts, "checks failing")
	case "pending":
		parts = append(parts, "checks running")
	}
	if pull.Review != "" {
		parts = append(parts, pull.Review)
	}
	return strings.Join(parts, " · ")
}

func (window *repoWindow) pulls(state *repoState) {
	if window.hubProblem(state) {
		return
	}
	worktreeOf := map[int]string{}
	for folder, number := range state.pulls {
		worktreeOf[number] = folder
	}
	var rows []fyne.CanvasObject
	for _, pull := range state.hub.Pulls {
		pull := pull
		detail := []string{pull.Branch + " → " + pull.Base}
		if pull.Author != "" {
			detail = append(detail, "@"+pull.Author)
		}
		if pull.Draft {
			detail = append(detail, "draft")
		}
		if pull.Fork {
			detail = append(detail, "from a fork")
		}
		switch pull.Checks {
		case "success":
			detail = append(detail, "✓ checks passed")
		case "failure":
			detail = append(detail, "✗ checks failing")
		case "pending":
			detail = append(detail, "● checks running")
		}
		if pull.Review != "" {
			detail = append(detail, pull.Review)
		}
		detail = append(detail, fmt.Sprintf("%d comment(s)", pull.Comments), "updated "+ago(pull.Updated))
		var action *tapRow
		if folder := worktreeOf[pull.Number]; folder != "" {
			action = chip("Open", func() { window.open(folder, false) })
		} else {
			action = chip("Check out in a worktree", nil)
			action.onTap = func() {
				window.create(gitrepo.NewWorktree{Kind: "pull", Pull: pull.Number, PullBranch: pull.Branch, Fork: pull.Fork}, "", action)
			}
		}
		rows = append(rows, settingRow(fmt.Sprintf("#%d %s", pull.Number, pull.Title), strings.Join(detail, " · "),
			controls(action, chip("View", func() { openLink(pull.URL) }))))
	}
	if len(rows) == 0 {
		window.body.Add(note("No open pull requests."))
		return
	}
	window.body.Add(card(rows...))
}

func (window *repoWindow) issues(state *repoState) {
	if window.hubProblem(state) {
		return
	}
	var rows []fyne.CanvasObject
	for _, issue := range state.hub.Issues {
		issue := issue
		var detail []string
		if len(issue.Labels) > 0 {
			detail = append(detail, strings.Join(issue.Labels, ", "))
		}
		if issue.Author != "" {
			detail = append(detail, "@"+issue.Author)
		}
		detail = append(detail, fmt.Sprintf("%d comment(s)", issue.Comments), "updated "+ago(issue.Updated))
		start := chip("Start a worktree", nil)
		start.onTap = func() {
			draft := fmt.Sprintf("Work on issue #%d: %s\n%s", issue.Number, issue.Title, issue.URL)
			window.create(gitrepo.NewWorktree{Kind: "new", Branch: gitrepo.IssueBranch(issue.Number, issue.Title)}, draft, start)
		}
		rows = append(rows, settingRow(fmt.Sprintf("#%d %s", issue.Number, issue.Title), strings.Join(detail, " · "),
			controls(start, chip("View", func() { openLink(issue.URL) }))))
	}
	if len(rows) == 0 {
		window.body.Add(note("No open issues."))
		return
	}
	window.body.Add(card(rows...))
}
