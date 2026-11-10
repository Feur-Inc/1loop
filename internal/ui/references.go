// SPDX-License-Identifier: MPL-2.0

package ui

// Issues and pull requests cited in the conversation: #12 (the project's
// GitHub repository), owner/name#12, or their GitHub address. They show as
// links. Hovering one shows its title, state and author, and for a pull
// request its size; clicking one opens it in a tab of the side panel, as on
// GitHub: description, comments and reviews, checks, commits and the diff
// of each file, so the user sees what the agent did. Nothing is read from
// GitHub until the user hovers or opens one; what was read is kept a while.

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/githubconnector"
	"1loop/internal/gitrepo"
)

// refScheme starts a reference's link: refScheme + "#12" or "owner/name#12".
const refScheme = "1loop-ref:"

// refAt is the length of the reference at the start of s, 0 if none: #12,
// or with full owner/name#12. The number must end there.
func refAt(s string, full bool) int {
	i := 0
	if full {
		owner := 0
		for owner < len(s) && owner < 40 && (isASCIIWord(s[owner]) || s[owner] == '-') {
			owner++
		}
		if owner == 0 || owner >= len(s) || s[owner] != '/' {
			return 0
		}
		name := owner + 1
		for name < len(s) && name-owner < 101 && (isASCIIWord(s[name]) || strings.IndexByte("._-", s[name]) >= 0) {
			name++
		}
		if name == owner+1 {
			return 0
		}
		i = name
	}
	if i >= len(s) || s[i] != '#' {
		return 0
	}
	digits := i + 1
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits == i+1 || digits-i-1 > 7 || s[i+1] == '0' || digits < len(s) && isWordByte(s[digits]) {
		return 0
	}
	return digits
}

func isASCIIWord(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// refLabel shows an issue's or pull request's address as GitHub does,
// owner/name#12; other addresses as they are.
func refLabel(address string) string {
	if ref, ok := gitrepo.ParseRef(address); ok && strings.Contains(address, "github.com/") {
		return ref.String()
	}
	return address
}

// referenceOf is the issue or pull request a link leads to, if any.
func referenceOf(link string) (gitrepo.Ref, bool) {
	if strings.HasPrefix(link, refScheme) {
		return gitrepo.ParseRef(strings.TrimPrefix(link, refScheme))
	}
	if strings.Contains(link, "github.com/") {
		return gitrepo.ParseRef(link)
	}
	return gitrepo.Ref{}, false
}

// refRepository is owner/name of the open chat's project on GitHub, "" when
// it has none (set by the desktop): #12 is a link only when it is known.
var refRepository = func() string { return "" }

// target is where a span leads: its link, or nothing for #12 while the
// project's GitHub repository is unknown (it stays plain text).
func (spanValue span) target() string {
	if strings.HasPrefix(spanValue.link, refScheme+"#") && refRepository() == "" {
		return ""
	}
	return spanValue.link
}

// openReference and hoverReference are set by the desktop.
var (
	openReference  = func(link string) {}
	hoverReference = func(link string, at fyne.Position) {}
)

// referenceCache keeps what was read of each issue or pull request.
type referenceCache struct {
	item    *gitrepo.Item
	err     string
	at      time.Time
	loading bool
}

// referenceProject is the project of the open chat, which the server
// checks the request against.
func (ui *desktopUI) referenceProject() string {
	return ui.projectOf(ui.cfg.Workspace)
}

// chatRepository is owner/name of the open chat's project on GitHub.
func (ui *desktopUI) chatRepository() string {
	project := ui.referenceProject()
	if project == "" || ui.gitSource() == nil {
		return ""
	}
	state := ui.repoFor(project)
	if state.repo == nil {
		ui.loadRepo(project, false, time.Minute)
		return ""
	}
	if state.repo.Owner == "" {
		return ""
	}
	return state.repo.Owner + "/" + state.repo.Name
}

// resolve gives #12 the open chat's repository.
func (ui *desktopUI) resolve(ref gitrepo.Ref) (gitrepo.Ref, error) {
	if ref.Owner != "" {
		return ref, nil
	}
	owner, name, ok := strings.Cut(ui.chatRepository(), "/")
	if !ok {
		return ref, fmt.Errorf("This project has no GitHub repository for %s", ref)
	}
	ref.Owner, ref.Name = owner, name
	return ref, nil
}

// readReference reads an issue or pull request from GitHub, through the
// server when the agent works there (its GitHub sign-in), else from here.
func (ui *desktopUI) readReference(ctx context.Context, ref gitrepo.Ref, detail bool, into any) error {
	if ui.server != nil {
		path := "/api/git/github/item?project=" + url.QueryEscape(ui.referenceProject()) + "&ref=" + url.QueryEscape(ref.String())
		if detail {
			path += "&detail=1"
		}
		return ui.server.client.json(ctx, "GET", path, nil, into)
	}
	authorizations := githubconnector.Default.Authorizations(ctx)
	if detail {
		found, err := gitrepo.Details(ctx, ref, authorizations)
		if err == nil {
			*into.(*gitrepo.Detail) = *found
		}
		return err
	}
	found, err := gitrepo.Summary(ctx, ref, authorizations)
	if err == nil {
		*into.(*gitrepo.Item) = *found
	}
	return err
}

// hoverReference shows the hover card of the reference under the pointer,
// or hides it when the pointer left it.
func (ui *desktopUI) hoverReference(link string, at fyne.Position) {
	ref, ok := referenceOf(link)
	if !ok {
		if ui.refHover != "" {
			ui.refHover = ""
			ui.tips.hide()
		}
		return
	}
	if link == ui.refHover {
		return
	}
	ui.refHover = link
	ref, err := ui.resolve(ref)
	if err != nil {
		ui.tips.showAt(at, [][]tipRun{{{ref.String(), colAmber}}, {{err.Error(), colMuted}}})
		return
	}
	key := ref.String()
	entry := ui.refCache[key]
	if entry == nil || !entry.loading && time.Since(entry.at) > 2*time.Minute {
		if ui.refCache == nil {
			ui.refCache = map[string]*referenceCache{}
		}
		entry = &referenceCache{loading: true}
		ui.refCache[key] = entry
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
			defer cancel()
			var item gitrepo.Item
			err := ui.readReference(ctx, ref, false, &item)
			fyne.Do(func() {
				entry.loading, entry.at = false, time.Now()
				if err != nil {
					entry.err = err.Error()
				} else {
					entry.item = &item
				}
				if ui.refHover == link {
					ui.tips.showAt(at, referenceCard(ref, entry))
				}
			})
		}()
	}
	ui.tips.showAt(at, referenceCard(ref, entry))
}

// referenceCard is the hover card: the title in the accent, then what it
// is, its state in its colour and, for a pull request, its lines added in
// green and removed in red.
func referenceCard(ref gitrepo.Ref, entry *referenceCache) [][]tipRun {
	switch {
	case entry.loading:
		return [][]tipRun{{{ref.String(), colAmber}}, {{"Loading from GitHub…", colMuted}}}
	case entry.err != "":
		return [][]tipRun{{{ref.String(), colAmber}}, {{entry.err, colDanger}}}
	}
	item := entry.item
	title := item.Title
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:79]) + "…"
	}
	kind := "Issue"
	if item.Pull {
		kind = "Pull request"
	}
	lines := [][]tipRun{{{title, colAmber}}, {
		{kind + " " + ref.String() + " · ", colMuted},
		{stateName(item.State), stateColor(item.State)},
		{" · by " + item.Author, colMuted},
	}}
	if item.Pull {
		lines = append(lines, []tipRun{
			{fmt.Sprintf("+%d", item.Additions), colAdded},
			{fmt.Sprintf(" −%d", item.Deletions), colDanger},
			{fmt.Sprintf(" · %s · %s · %s ← %s", plural(item.Files, "file"), plural(item.Commits, "commit"), item.Base, item.Head), colMuted},
		})
	} else {
		line := plural(item.Comments, "comment")
		if len(item.Labels) > 0 {
			line += " · " + strings.Join(item.Labels, ", ")
		}
		lines = append(lines, []tipRun{{line, colMuted}})
	}
	return lines
}

func stateName(state string) string {
	if state == "" {
		return ""
	}
	return strings.ToUpper(state[:1]) + state[1:]
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// openReference shows an issue or pull request in the side panel.
func (ui *desktopUI) openReference(link string) {
	ref, ok := referenceOf(link)
	if !ok {
		return
	}
	ui.refHover = ""
	ui.tips.hide()
	ref, err := ui.resolve(ref)
	if err != nil {
		ui.notice("GitHub", err.Error())
		return
	}
	ui.panel.openReference(ref, ui.referenceProject())
}

func (panel *toolPanel) openReference(ref gitrepo.Ref, project string) {
	for i, tab := range panel.tabs {
		if tab.kind == "github" && tab.ref == ref.String() {
			panel.choose(i)
			panel.unfold()
			return
		}
	}
	panel.tabs = append(panel.tabs, panel.referenceTab(ref, project))
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

// referenceTab is the tab of an issue or pull request, already loading.
func (panel *toolPanel) referenceTab(ref gitrepo.Ref, project string) panelTab {
	ui := panel.ui
	ctx, cancel := context.WithCancel(ui.ctx)
	body := container.NewStack(padded(20, 20, 18, 18, note("Loading "+ref.String()+" from GitHub…")))
	var load func()
	load = func() {
		go func() {
			read, stop := context.WithTimeout(ctx, 60*time.Second)
			defer stop()
			var detail gitrepo.Detail
			err := ui.readReference(read, ref, true, &detail)
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					retry := newRow("Try again", nil, styleOutline, nil)
					retry.onTap = func() {
						body.Objects = []fyne.CanvasObject{padded(20, 20, 18, 18, note("Loading "+ref.String()+" from GitHub…"))}
						body.Refresh()
						load()
					}
					body.Objects = []fyne.CanvasObject{padded(20, 20, 18, 18, container.NewVBox(note(err.Error()), container.NewHBox(retry)))}
				} else {
					body.Objects = []fyne.CanvasObject{referenceView(&detail, load)}
				}
				body.Refresh()
			})
		}()
	}
	load()
	return panelTab{kind: "github", title: ref.String(), icon: iconBranch, cancel: cancel, ref: ref.String(), workspace: project, content: body}
}

// referenceView is the page: a header, then the conversation, commits,
// checks and files, one at a time.
func referenceView(detail *gitrepo.Detail, refresh func()) fyne.CanvasObject {
	kind := "Issue"
	if detail.Pull {
		kind = "Pull request"
	}
	title := plainFlow(detail.Title, 16, colText)
	title.bold = true
	meta := kind + " " + detail.Ref.String() + " · " + stateName(detail.State) + " · opened by " + detail.Author + " on " + detail.Created.Local().Format("2 Jan 2006")
	if detail.Pull {
		meta += fmt.Sprintf(" · %s ← %s · +%d −%d", detail.Base, detail.Head, detail.Additions, detail.Deletions)
	}
	open := newRow("Open on GitHub", nil, styleOutline, func() { openLink(detail.URL) })
	again := newRow("Refresh", nil, styleOutline, refresh)
	header := padded(12, 8, 16, 16, container.NewVBox(
		container.NewBorder(nil, nil, textLine(stateName(detail.State), 12.5, stateColor(detail.State)), container.NewHBox(again, open)),
		title,
		plainFlow(meta, 12.5, colMuted)))

	views := []struct {
		name  string
		count int
		build func() fyne.CanvasObject
	}{{"Conversation", len(detail.Posts), func() fyne.CanvasObject { return newKineticVScroll(conversationView(detail)) }}}
	if detail.Pull {
		views = append(views,
			struct {
				name  string
				count int
				build func() fyne.CanvasObject
			}{"Commits", len(detail.CommitList), func() fyne.CanvasObject { return newKineticVScroll(commitsView(detail)) }},
			struct {
				name  string
				count int
				build func() fyne.CanvasObject
			}{"Checks", len(detail.Checks), func() fyne.CanvasObject { return newKineticVScroll(checksView(detail)) }},
			struct {
				name  string
				count int
				build func() fyne.CanvasObject
			}{"Files", detail.Files, func() fyne.CanvasObject { return filesView(detail) }})
	}
	page := container.NewStack()
	tabs := container.New(layout.NewCustomPaddedHBoxLayout(4))
	var rows []*tapRow
	for i, view := range views {
		i, view := i, view
		row := newRow(fmt.Sprintf("%s  %d", view.name, view.count), nil, styleChip, nil)
		row.onTap = func() {
			for j, other := range rows {
				other.SetSelected(j == i)
			}
			page.Objects = []fyne.CanvasObject{view.build()}
			page.Refresh()
		}
		rows = append(rows, row)
		tabs.Add(row)
	}
	rows[0].onTap()
	return container.NewBorder(container.NewVBox(header, padded(0, 6, 12, 12, tabs)), nil, nil, nil, page)
}

// stateColor is GitHub's meaning of each state, in 1loop's colours.
func stateColor(state string) color.NRGBA {
	switch state {
	case "open":
		return colAdded
	case "merged":
		return colAmber
	case "closed":
		return colDanger
	}
	return colMuted
}

// conversationView is the description, then the comments and reviews.
func conversationView(detail *gitrepo.Detail) fyne.CanvasObject {
	box := container.New(layout.NewCustomPaddedVBoxLayout(14))
	post := func(heading, text string) {
		parts := []fyne.CanvasObject{plainFlow(heading, 12.5, colMuted)}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, newMarkdownView(text).box)
		}
		box.Add(container.NewVBox(parts...))
	}
	description := detail.Body
	if strings.TrimSpace(description) == "" {
		description = "_No description._"
	}
	post(detail.Author+" · "+detail.Created.Local().Format("2 Jan 2006 15:04"), description)
	for _, item := range detail.Posts {
		heading := item.Author + " · " + item.At.Local().Format("2 Jan 2006 15:04")
		switch item.Kind {
		case "review":
			heading = item.Author + " reviewed: " + item.State + " · " + item.At.Local().Format("2 Jan 2006 15:04")
		case "line":
			heading = fmt.Sprintf("%s on %s:%d · %s", item.Author, item.Path, item.Line, item.At.Local().Format("2 Jan 2006 15:04"))
		}
		post(heading, item.Body)
	}
	return padded(8, 20, 18, 18, box)
}

// commitsView lists the commits, oldest first, as GitHub does.
func commitsView(detail *gitrepo.Detail) fyne.CanvasObject {
	box := container.New(layout.NewCustomPaddedVBoxLayout(10))
	for _, commit := range detail.CommitList {
		sha := commit.SHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		box.Add(container.NewVBox(plainFlow(commit.Title, 13, colText),
			plainFlow(sha+" · "+commit.Author+" · "+commit.At.Local().Format("2 Jan 2006 15:04"), 12, colMuted)))
	}
	if len(detail.CommitList) == 0 {
		box.Add(note("No commits."))
	}
	return padded(8, 20, 18, 18, box)
}

// checksView lists the checks of the last commit; one opens on GitHub.
func checksView(detail *gitrepo.Detail) fyne.CanvasObject {
	box := container.New(layout.NewCustomPaddedVBoxLayout(4))
	for _, check := range detail.Checks {
		check := check
		result := check.Conclusion
		if check.Status != "completed" {
			result = strings.ReplaceAll(check.Status, "_", " ")
		}
		ink := colMuted
		switch result {
		case "success":
			ink = colAdded
		case "failure", "timed_out", "cancelled", "action_required":
			ink = colDanger
		}
		row := newRow(check.Name, nil, styleNav, func() { openLink(check.URL) })
		box.Add(container.NewBorder(nil, nil, nil, container.NewCenter(textLine(strings.ReplaceAll(result, "_", " "), 12.5, ink)), row))
	}
	if len(detail.Checks) == 0 {
		box.Add(note("No checks on the last commit."))
	}
	return padded(8, 20, 12, 18, box)
}

// filesView is the diff of every changed file, as the Changes tab shows a
// diff.
func filesView(detail *gitrepo.Detail) fyne.CanvasObject {
	var patch strings.Builder
	for _, file := range detail.FileList {
		old := file.Name
		if file.Previous != "" {
			old = file.Previous
		}
		fmt.Fprintf(&patch, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", old, file.Name, old, file.Name)
		if file.Patch == "" {
			fmt.Fprintf(&patch, "@@ -0,0 +0,0 @@ %s, +%d −%d: no diff from GitHub (binary or too large)\n", file.Status, file.Additions, file.Deletions)
			continue
		}
		patch.WriteString(strings.TrimSuffix(file.Patch, "\n") + "\n")
	}
	if patch.Len() == 0 {
		return padded(20, 20, 18, 18, note("No changed files."))
	}
	view := newDiffView(patch.String())
	if detail.Files > len(detail.FileList) {
		return container.NewBorder(padded(0, 6, 18, 18, note(fmt.Sprintf("The first %d of %d files: open it on GitHub for the others.", len(detail.FileList), detail.Files))), nil, nil, nil, view)
	}
	return view
}
