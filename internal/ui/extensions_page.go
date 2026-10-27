package ui

// Settings → Extensions: a store of skills and plugins found on GitHub
// (cards with their picture, stars and description; a page with the README;
// a confirmation listing what installing writes, may run and risks), what is
// installed, and the MCP servers 1loop connects to.

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/extensions"
)

var (
	iconPuzzle = lineIcon("puzzle", `<path d="M3 5.5h2.6a1.4 1.4 0 1 1 2.8 0H11v2.6a1.4 1.4 0 1 1 0 2.8V13.5H8.4a1.4 1.4 0 1 0-2.8 0H3v-2.6a1.4 1.4 0 1 0 0-2.8z"/>`)
	iconStar   = lineIcon("star", `<path d="M8 2.3l1.7 3.6 3.9.5-2.9 2.7.7 3.9L8 11.1 4.6 13l.7-3.9-2.9-2.7 3.9-.5z"/>`)
	iconGlobe  = lineIcon("globe", `<circle cx="8" cy="8" r="5.5"/><path d="M2.5 8h11M8 2.5c2 2.2 2 8.8 0 11-2-2.2-2-8.8 0-11"/>`)
)

// extensionsPage is the section while it is shown.
type extensionsPage struct {
	ui       *desktopUI
	tab      string
	tabs     map[string]*tapRow
	body     *fyne.Container
	query    *widget.Entry
	kind     string
	kinds    map[string]*tapRow
	order    *picker
	grid     *fyne.Container
	status   *widget.Label
	more     *tapRow
	page     int
	listings []extensions.Listing
	search   int // the search on screen; an older one's answer is dropped
	statuses map[string]extensions.MCPStatus
	checking bool
}

var storeOrders = map[string]string{"Most stars": "stars", "Recently updated": "updated", "Best match": "best"}

func (ui *desktopUI) extensionSettings() fyne.CanvasObject {
	page := &extensionsPage{ui: ui, tab: "store", tabs: map[string]*tapRow{}, kind: "all", kinds: map[string]*tapRow{}, body: container.NewVBox(), statuses: map[string]extensions.MCPStatus{}}
	ui.extPage = page
	tabs := container.New(layout.NewCustomPaddedHBoxLayout(6))
	for _, tab := range []struct{ id, label string }{{"store", "Store"}, {"installed", "Installed"}, {"mcp", "MCP servers"}} {
		tab := tab
		row := newRow(tab.label, nil, styleChip, func() { page.show(tab.id) })
		page.tabs[tab.id] = row
		tabs.Add(row)
	}
	intro := note("Skills teach the agent a task; plugins bundle skills, MCP servers and hooks. Both work with 1loop's agent and with Claude Code. MCP servers give the agent the tools of other apps.")
	page.show("store")
	return container.NewVBox(ui.prefs.pageTitle("Extensions", "", nil), intro, padded(6, 10, 0, 0, tabs), page.body)
}

// extensionsChanged redraws the section after a change made elsewhere
// (the phone).
func (ui *desktopUI) extensionsChanged() {
	if page := ui.extPage; page != nil && ui.prefs.open && ui.prefs.section == "Extensions" && len(ui.prefs.frames) == 1 {
		page.show(page.tab)
	}
}

func (page *extensionsPage) show(tab string) {
	page.tab = tab
	for id, row := range page.tabs {
		row.SetSelected(id == tab)
	}
	switch tab {
	case "installed":
		page.body.Objects = []fyne.CanvasObject{page.installedView()}
	case "mcp":
		page.body.Objects = []fyne.CanvasObject{page.mcpView()}
	default:
		page.body.Objects = []fyne.CanvasObject{page.storeView()}
	}
	page.body.Refresh()
}

// ---------------------------------------------------------------------------
// The store.

func (page *extensionsPage) storeView() fyne.CanvasObject {
	if page.query == nil {
		page.query = widget.NewEntry()
		page.query.SetPlaceHolder("Search skills and plugins on GitHub…")
		page.query.OnSubmitted = func(string) { page.runSearch(1) }
		page.order = newPicker(page.ui.window, "Most stars", styleChip, nil)
		page.order.Options = []string{"Most stars", "Recently updated", "Best match"}
		page.order.SetSelected("Most stars")
		page.order.onChanged = func(string) { page.runSearch(1) }
		page.grid = container.NewGridWithColumns(2)
		page.status = note("")
		page.more = newRow("Load more", nil, styleOutline, func() { page.runSearch(page.page + 1) })
		page.more.Hide()
	}
	kinds := container.New(layout.NewCustomPaddedHBoxLayout(6))
	for _, kind := range []struct{ id, label string }{{"all", "All"}, {"skill", "Skills"}, {"plugin", "Plugins"}} {
		kind := kind
		row := newRow(kind.label, nil, styleChip, func() {
			page.kind = kind.id
			for id, row := range page.kinds {
				row.SetSelected(id == kind.id)
			}
			page.runSearch(1)
		})
		row.SetSelected(kind.id == page.kind)
		page.kinds[kind.id] = row
		kinds.Add(row)
	}
	search := newRow("Search", iconGlobe, stylePrimary, func() { page.runSearch(1) })
	bar := container.NewBorder(nil, nil, nil, search, page.query)
	filters := container.NewBorder(nil, nil, kinds, page.order.row)
	if len(page.listings) == 0 {
		page.runSearch(1)
	}
	return container.NewVBox(bar, padded(8, 6, 0, 0, filters), page.status, page.grid, container.NewCenter(page.more))
}

func (page *extensionsPage) runSearch(number int) {
	page.search++
	search := page.search
	query, kind, order := page.query.Text, page.kind, storeOrders[page.order.Selected]
	page.status.SetText("Searching GitHub…")
	page.more.Disable()
	go func() {
		ctx, cancel := context.WithTimeout(page.ui.ctx, 40*time.Second)
		defer cancel()
		result, err := extensions.Search(ctx, kind, query, order, number)
		fyne.Do(func() {
			if search != page.search {
				return
			}
			page.more.Enable()
			if err != nil {
				page.status.SetText(err.Error())
				return
			}
			if number == 1 {
				page.listings = nil
				page.grid.Objects = nil
			}
			page.page = number
			// A repository with both topics can come back on a later page.
			shown := map[string]bool{}
			for _, listing := range page.listings {
				shown[listing.Repo] = true
			}
			for _, listing := range result.Listings {
				if !shown[listing.Repo] {
					shown[listing.Repo] = true
					page.listings = append(page.listings, listing)
					page.grid.Add(page.card(listing))
				}
			}
			page.grid.Refresh()
			switch {
			case len(page.listings) == 0:
				page.status.SetText("Nothing found. Try other words, or another kind.")
			default:
				page.status.SetText(fmt.Sprintf("%s on GitHub · showing %d", countText(result.Total, "result"), len(page.listings)))
			}
			if result.More {
				page.more.Show()
			} else {
				page.more.Hide()
			}
		})
	}()
}

// card is a listing: its picture, name, owner, stars, description, and
// Install.
func (page *extensionsPage) card(listing extensions.Listing) fyne.CanvasObject {
	picture := remotePicture(listing.Image, fyne.NewSize(340, 170))
	name := textLine(listing.Name, 15, colText)
	name.TextStyle = fyne.TextStyle{Bold: true}
	kind := "Skill"
	if listing.Kind == "plugin" {
		kind = "Plugin"
	}
	badge := surface(textLine(kind, 11, colMuted), colRaised, nil, 6, 4)
	owner := textLine("by "+listing.Owner, 12.5, colMuted)
	stars := container.NewHBox(iconImage(iconStar, colAmber, 13), textLine(compactCount(listing.Stars), 12.5, colText), textLine("· updated "+ago(listing.Updated), 12.5, colFaint))
	description := listing.Description
	if description == "" {
		description = "No description."
	}
	if runes := []rune(description); len(runes) > 150 {
		description = string(runes[:147]) + "…"
	}
	var action fyne.CanvasObject
	if listing.Installed {
		action = newRow("Installed", iconCheck, styleChip, func() { page.show("installed") })
	} else {
		action = newRow("Install", iconPlus, styleOutline, func() { page.ui.installFromStore(listing.Repo) })
	}
	open := newRow("Details", nil, styleChip, func() { page.details(listing) })
	body := container.NewVBox(
		picture,
		padded(8, 0, 2, 2, container.NewBorder(nil, nil, nil, container.NewCenter(badge), name)),
		padded(0, 0, 2, 2, owner),
		padded(0, 0, 2, 2, stars),
		padded(2, 0, 2, 2, plainFlow(description, 13, colMuted)),
		layout.NewSpacer(),
		padded(6, 0, 0, 0, container.NewHBox(action, open)),
	)
	return padded(0, 8, 0, 8, container.NewStack(newTapArea(func() { page.details(listing) }), surface(body, colSurface, colBorder, 12, 10)))
}

// details is a listing's page: its picture, numbers, topics and README.
func (page *extensionsPage) details(listing extensions.Listing) {
	ui := page.ui
	readme := container.NewVBox(note("Loading the README…"))
	install := newRow("Install", iconPlus, stylePrimary, func() { ui.installFromStore(listing.Repo) })
	if listing.Installed {
		install = newRow("Installed: reinstall", iconArrowTurn, styleOutline, func() { ui.installFromStore(listing.Repo) })
	}
	github := newRow("Open on GitHub", iconGlobe, styleOutline, func() {
		if address, err := url.Parse(listing.URL); err == nil {
			fyne.CurrentApp().OpenURL(address)
		}
	})
	facts := []string{compactCount(listing.Stars) + " stars", compactCount(listing.Forks) + " forks", "updated " + ago(listing.Updated)}
	if listing.License != "" {
		facts = append(facts, listing.License)
	}
	topics := container.NewHBox()
	for i, topic := range listing.Topics {
		if i == 8 {
			break
		}
		topics.Add(surface(textLine(topic, 11.5, colMuted), colRaised, nil, 6, 4))
	}
	content := container.NewVBox(
		ui.prefs.pageTitle(listing.Name, "Extensions", nil),
		remotePicture(listing.Image, fyne.NewSize(700, 350)),
		padded(10, 0, 0, 0, textLine(listing.Repo, 13, colMuted)),
		plainFlow(listing.Description, 14, colText),
		textLine(strings.Join(facts, " · "), 12.5, colMuted),
		container.NewHScroll(topics),
		padded(10, 10, 0, 0, container.NewHBox(install, github)),
		sectionTitle("README"),
		readme,
	)
	ui.prefs.push(settingsFrame{content: content})
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		defer cancel()
		text, err := extensions.Readme(ctx, listing.Repo)
		fyne.Do(func() {
			switch {
			case err != nil:
				readme.Objects = []fyne.CanvasObject{note(err.Error())}
			case strings.TrimSpace(text) == "":
				readme.Objects = []fyne.CanvasObject{note("This repository has no README.")}
			default:
				readme.Objects = []fyne.CanvasObject{newMarkdownView(readmeMarkdown(text)).box}
			}
			readme.Refresh()
		})
	}()
}

// installFromStore downloads a repository, shows what installing it does,
// and installs what the user keeps.
func (ui *desktopUI) installFromStore(repo string) {
	progress := note("Downloading " + repo + " to see what it holds…")
	var waiting *sheet
	waiting = ui.modal("Install "+repo, progress, 520, nil, newRow("Cancel", nil, styleOutline, func() { waiting.dismiss() }))
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 5*time.Minute)
		defer cancel()
		plan, err := extensions.Prepare(ctx, repo)
		fyne.Do(func() {
			if !waiting.open {
				return // cancelled
			}
			waiting.dismiss()
			if err != nil {
				ui.notice("Install "+repo, err.Error())
				return
			}
			ui.confirmInstall(plan)
		})
	}()
}

// confirmInstall is the confirmation: what it adds, the folders it writes,
// the commands it may run and the risks.
func (ui *desktopUI) confirmInstall(plan *extensions.Plan) {
	keep := map[string]bool{}
	body := container.NewVBox()
	if len(plan.Plugins) > 0 {
		heading := "Plugin"
		if plan.Kind == "marketplace" {
			heading = fmt.Sprintf("Plugins of this marketplace (%d) · choose those to install", len(plan.Plugins))
		}
		body.Add(sectionTitle(heading))
		preselect := plan.Kind != "marketplace" || len(plan.Plugins) <= 8
		for _, plugin := range plan.Plugins {
			plugin := plugin
			keep[plugin.ID] = preselect
			parts := []string{}
			if len(plugin.Skills) > 0 {
				parts = append(parts, countText(len(plugin.Skills), "skill"))
			}
			if len(plugin.MCP) > 0 {
				parts = append(parts, countText(len(plugin.MCP), "MCP server"))
			}
			if len(plugin.Hooks) > 0 {
				parts = append(parts, countText(len(plugin.Hooks), "hook"))
			}
			lines := []string{plugin.Description, strings.Join(parts, " · ")}
			title := plugin.Name
			if plugin.Version != "" {
				title += " " + plugin.Version
			}
			var control fyne.CanvasObject = spacer(0, 0)
			if plan.Kind == "marketplace" {
				control = newToggle(preselect, func(on bool) { keep[plugin.ID] = on })
			}
			body.Add(card(extensionRow(title, lines, control)))
		}
	}
	if len(plan.Skills) > 0 {
		body.Add(sectionTitle(countText(len(plan.Skills), "skill")))
		var rows []fyne.CanvasObject
		for _, skill := range plan.Skills {
			rows = append(rows, settingRow(skill.Name, skill.Description, nil))
		}
		body.Add(card(rows...))
	}
	body.Add(sectionTitle("Folders it writes"))
	// Whole, not in a scroll of its own inside the dialog's.
	body.Add(surface(codeText(strings.Join(plan.Folders, "\n")), colCode, colBorderSoft, 10, 4))
	body.Add(sectionTitle("Commands it may run on this computer"))
	if len(plan.Commands) == 0 {
		body.Add(note("None: no MCP server and no hook."))
	} else {
		body.Add(surface(codeText(strings.Join(plan.Commands, "\n")), colCode, colBorderSoft, 10, 4))
	}
	body.Add(sectionTitle("Risks"))
	for _, risk := range plan.Risks {
		body.Add(container.NewBorder(nil, nil, container.NewVBox(padded(3, 0, 0, 0, iconImage(iconShield, colAmber, 14))), nil, plainFlow(risk, 13, colText)))
	}
	if len(plan.Notes) > 0 {
		body.Add(sectionTitle("Notes"))
		for _, line := range plan.Notes {
			body.Add(plainFlow(line, 12.5, colMuted))
		}
	}
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(0, 460))
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	install := newRow("Install", iconPlus, stylePrimary, nil)
	install.onTap = func() {
		var chosen []string
		if plan.Kind == "marketplace" {
			for id, on := range keep {
				if on {
					chosen = append(chosen, id)
				}
			}
			if len(chosen) == 0 {
				ui.notice("Install", "Choose at least one plugin.")
				return
			}
		}
		install.Disable()
		go func() {
			_, err := extensions.Install(plan.ID, chosen)
			fyne.Do(func() {
				dialog.dismiss()
				if err != nil {
					ui.notice("Install "+plan.Repo, err.Error())
					return
				}
				ui.remote.broadcast("extensions", map[string]bool{"changed": true})
				if page := ui.extPage; page != nil {
					for i := range page.listings {
						if page.listings[i].Repo == plan.Repo {
							page.listings[i].Installed = true
						}
					}
				}
				if ui.prefs.open && ui.prefs.section == "Extensions" {
					ui.prefs.show("Extensions")
					if page := ui.extPage; page != nil {
						page.show("installed")
					}
				}
			})
		}()
	}
	dialog = ui.modal("Install "+plan.Repo+"?", scroll, 720, nil, cancel, install)
}

// ---------------------------------------------------------------------------
// What is installed.

func (page *extensionsPage) installedView() fyne.CanvasObject {
	view, err := extensions.View()
	if err != nil {
		return note(err.Error())
	}
	box := container.NewVBox()
	box.Add(sectionTitle("Plugins"))
	if len(view.Plugins) == 0 {
		box.Add(note("No plugin yet: find some in the store."))
	} else {
		var rows []fyne.CanvasObject
		for _, plugin := range view.Plugins {
			plugin := plugin
			parts := []string{"from " + plugin.Repo}
			if len(plugin.Skills) > 0 {
				parts = append(parts, countText(len(plugin.Skills), "skill"))
			}
			if len(plugin.MCP) > 0 {
				parts = append(parts, countText(len(plugin.MCP), "MCP server"))
			}
			if plugin.Hooks > 0 {
				parts = append(parts, countText(plugin.Hooks, "hook"))
			}
			if plugin.Commands > 0 {
				parts = append(parts, countText(plugin.Commands, "command")+" (Claude Code only)")
			}
			lines := []string{plugin.Description, strings.Join(parts, " · ")}
			title := plugin.Name
			if plugin.Version != "" {
				title += " " + plugin.Version
			}
			toggle := newToggle(plugin.Enabled, func(on bool) { page.enable("plugin", plugin.ID, on) })
			remove := newIconButton(iconTrash, colRaised, colMuted, func() { page.remove("plugin", plugin.ID, plugin.Name) })
			rows = append(rows, extensionRow(title, lines, container.NewHBox(remove, container.NewCenter(toggle))))
		}
		box.Add(card(rows...))
	}
	box.Add(sectionTitle("Skills"))
	if len(view.Skills) == 0 {
		box.Add(note("No skill installed on its own. Plugins' skills show with their plugin."))
	} else {
		var rows []fyne.CanvasObject
		for _, skill := range view.Skills {
			skill := skill
			lines := []string{skill.Description}
			if skill.Source != "" {
				lines = append(lines, "from "+skill.Source)
			}
			toggle := newToggle(skill.Enabled, func(on bool) { page.enable("skill", skill.Name, on) })
			remove := newIconButton(iconTrash, colRaised, colMuted, func() { page.remove("skill", skill.Name, skill.Name) })
			rows = append(rows, extensionRow(skill.Name, lines, container.NewHBox(remove, container.NewCenter(toggle))))
		}
		box.Add(card(rows...))
	}
	box.Add(padded(10, 0, 0, 0, note("Installed in "+view.Dir+". Changes apply to the next message.")))
	return box
}

func (page *extensionsPage) enable(kind, id string, on bool) {
	if err := extensions.SetEnabled(kind, id, on); err != nil {
		page.ui.notice("Extensions", err.Error())
	}
	if kind == "mcp" {
		extensions.Servers.Forget(id)
	}
	page.ui.remote.broadcast("extensions", map[string]bool{"changed": true})
	page.show(page.tab)
}

func (page *extensionsPage) remove(kind, id, name string) {
	page.ui.confirm("Remove "+name+"?", "Its files are deleted from 1loop's extensions folder.", "Remove", func() {
		if err := extensions.Remove(kind, id); err != nil {
			page.ui.notice("Extensions", err.Error())
		}
		page.ui.remote.broadcast("extensions", map[string]bool{"changed": true})
		page.show(page.tab)
	})
}

// ---------------------------------------------------------------------------
// MCP servers.

func (page *extensionsPage) mcpView() fyne.CanvasObject {
	ui := page.ui
	registry, err := extensions.Load()
	if err != nil {
		return note(err.Error())
	}
	add := newRow("Add server", iconPlus, stylePrimary, func() { page.editMCP(extensions.MCPServer{Type: "stdio", Enabled: true}, "") })
	check := newRow("Check connections", iconArrowTurn, styleOutline, page.checkMCP)
	box := container.NewVBox(note("1loop connects to these servers as a client: their tools join the agent's, and Claude Code gets them too. A command starts in the chat's project."),
		padded(4, 6, 0, 0, container.NewHBox(add, check)))
	var rows []fyne.CanvasObject
	for _, server := range registry.MCP {
		server := server
		toggle := newToggle(server.Enabled, func(on bool) { page.enable("mcp", server.Name, on) })
		edit := newIconButton(iconPencil, colRaised, colMuted, func() { page.editMCP(server, server.Name) })
		remove := newIconButton(iconTrash, colRaised, colMuted, func() {
			ui.confirm("Remove "+server.Name+"?", "1loop stops using this MCP server.", "Remove", func() {
				extensions.Servers.Forget(server.Name)
				if err := extensions.RemoveMCP(server.Name); err != nil {
					ui.notice("MCP servers", err.Error())
				}
				ui.remote.broadcast("extensions", map[string]bool{"changed": true})
				page.show("mcp")
			})
		})
		rows = append(rows, extensionRow(server.Name, page.mcpDetail(server), container.NewHBox(edit, remove, container.NewCenter(toggle))))
	}
	for _, server := range extensions.Enabled().MCP {
		if server.Plugin != "" {
			rows = append(rows, extensionRow(server.Name, append(page.mcpDetail(server), "from the plugin "+server.Plugin+" (turn the plugin off in Installed)"), nil))
		}
	}
	if len(rows) == 0 {
		box.Add(note("No server yet. Add one with its command (for example npx -y @modelcontextprotocol/server-filesystem ~/docs) or its address."))
	} else {
		box.Add(card(rows...))
	}
	if len(page.statuses) == 0 && !page.checking && len(rows) > 0 {
		page.checkMCP()
	}
	return box
}

// mcpDetail is how a server runs, and how it answered.
func (page *extensionsPage) mcpDetail(server extensions.MCPServer) []string {
	run := server.URL
	if server.Type == "stdio" {
		run = strings.TrimSpace(server.Command + " " + strings.Join(server.Args, " "))
	}
	if runes := []rune(run); len(runes) > 160 {
		run = string(runes[:159]) + "…" // a plugin's command can be a whole script
	}
	state := ""
	switch status, ok := page.statuses[server.Name+"\x00"+server.Plugin]; {
	case !server.Enabled:
		state = "○ Off"
	case page.checking && !ok:
		state = "Connecting…"
	case !ok:
		state = "Not checked yet"
	case status.Connected:
		state = "● Connected · " + countText(len(status.Tools), "tool")
		if len(status.Tools) > 0 {
			shown := status.Tools
			if len(shown) > 8 {
				shown = append(shown[:8:8], "…")
			}
			state += ": " + strings.Join(shown, ", ")
		}
	default:
		state = "● " + status.Error
	}
	return []string{run, state}
}

func (page *extensionsPage) checkMCP() {
	if page.checking {
		return
	}
	page.checking = true
	page.statuses = map[string]extensions.MCPStatus{}
	if page.tab == "mcp" {
		page.show("mcp")
	}
	workspace := page.ui.cfg.Workspace
	go func() {
		ctx, cancel := context.WithTimeout(page.ui.ctx, time.Minute)
		defer cancel()
		statuses := extensions.Servers.Status(ctx, extensions.Enabled().MCP, workspace)
		fyne.Do(func() {
			page.checking = false
			for _, status := range statuses {
				page.statuses[status.Name+"\x00"+status.Plugin] = status
			}
			if page.tab == "mcp" && page.ui.prefs.open && page.ui.prefs.section == "Extensions" && len(page.ui.prefs.frames) == 1 {
				page.show("mcp")
			}
		})
	}()
}

// editMCP is the form of a server: its name, and its command or address.
func (page *extensionsPage) editMCP(server extensions.MCPServer, previous string) {
	ui := page.ui
	name := widget.NewEntry()
	name.SetText(server.Name)
	name.SetPlaceHolder("github")
	kind := newPicker(ui.window, "Command (stdio)", styleField, nil)
	kind.Options = []string{"Command (stdio)", "Address (HTTP)"}
	command := widget.NewEntry()
	command.SetText(server.Command)
	command.SetPlaceHolder("npx")
	args := widget.NewMultiLineEntry()
	args.SetText(strings.Join(server.Args, "\n"))
	args.SetPlaceHolder("-y\n@modelcontextprotocol/server-filesystem\n/home/me/docs")
	args.SetMinRowsVisible(3)
	env := widget.NewMultiLineEntry()
	env.SetText(pairsText(server.Env, "="))
	env.SetPlaceHolder("API_KEY=…")
	env.SetMinRowsVisible(2)
	address := widget.NewEntry()
	address.SetText(server.URL)
	address.SetPlaceHolder("https://mcp.example.com/mcp")
	headers := widget.NewMultiLineEntry()
	headers.SetText(pairsText(server.Headers, ": "))
	headers.SetPlaceHolder("Authorization: Bearer …")
	headers.SetMinRowsVisible(2)
	stdio := card(
		settingStack("Command", "The program that starts the server.", command),
		settingStack("Arguments", "One per line.", args),
		settingStack("Environment", "One KEY=value per line. $NAME takes this computer's variable.", env))
	web := card(
		settingStack("Address", "Its Streamable HTTP endpoint.", address),
		settingStack("Headers", "One Name: value per line, for example its token.", headers))
	sync := func() {
		if kind.Selected == kind.Options[0] {
			stdio.Show()
			web.Hide()
		} else {
			stdio.Hide()
			web.Show()
		}
	}
	kind.onChanged = func(string) { sync() }
	if server.Type == "http" {
		kind.SetSelected(kind.Options[1])
	} else {
		kind.SetSelected(kind.Options[0])
	}
	sync()
	problem := note("")
	title := "Add an MCP server"
	if previous != "" {
		title = previous
	}
	content := container.NewVBox(ui.prefs.pageTitle(title, "Extensions", nil),
		card(settingStack("Name", "How 1loop and the agent call it: letters, digits, - or _.", name),
			settingRow("Connects with", "", sized(200, kind.row))),
		stdio, web, problem)
	cancel := newRow("Cancel", nil, styleOutline, ui.prefs.back)
	save := newRow("Save", nil, stylePrimary, func() {
		edited := extensions.MCPServer{Name: strings.TrimSpace(name.Text), Enabled: server.Enabled || previous == ""}
		if kind.Selected == kind.Options[1] {
			edited.Type, edited.URL, edited.Headers = "http", strings.TrimSpace(address.Text), textPairs(headers.Text, ":")
		} else {
			edited.Type, edited.Command, edited.Env = "stdio", strings.TrimSpace(command.Text), textPairs(env.Text, "=")
			for _, line := range strings.Split(args.Text, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					edited.Args = append(edited.Args, line)
				}
			}
		}
		if err := extensions.SaveMCP(edited, previous); err != nil {
			problem.SetText(err.Error())
			return
		}
		extensions.Servers.Forget(edited.Name)
		if previous != "" {
			extensions.Servers.Forget(previous)
		}
		ui.remote.broadcast("extensions", map[string]bool{"changed": true})
		page.statuses = map[string]extensions.MCPStatus{}
		ui.prefs.done()
		if page := ui.extPage; page != nil {
			page.show("mcp")
		}
	})
	ui.prefs.push(settingsFrame{content: content, actions: []fyne.CanvasObject{cancel, save}})
}

func pairsText(values map[string]string, separator string) string {
	var lines []string
	for key, value := range values {
		lines = append(lines, key+separator+value)
	}
	return strings.Join(lines, "\n")
}

func textPairs(text, separator string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, found := strings.Cut(line, separator)
		if key = strings.TrimSpace(key); found && key != "" {
			values[key] = strings.TrimSpace(value)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// ---------------------------------------------------------------------------
// Small parts.

var htmlTag = regexp.MustCompile(`(?s)<!--.*?-->|</?[A-Za-z][^<>]*>`)

// readmeMarkdown leaves out the HTML a README often holds (centered
// headers, badges), which the Markdown view would show as text; code
// blocks keep it.
func readmeMarkdown(text string) string {
	parts := strings.Split(text, "```")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = htmlTag.ReplaceAllString(parts[i], "")
	}
	return strings.Join(parts, "```")
}

// extensionRow is settingRow with several lines under the title.
func extensionRow(title string, lines []string, control fyne.CanvasObject) fyne.CanvasObject {
	text := container.New(layout.NewCustomPaddedVBoxLayout(3), plainFlow(title, 14, colText))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			text.Add(plainFlow(line, 12.5, colMuted))
		}
	}
	var end fyne.CanvasObject
	if control != nil {
		end = padded(0, 0, 20, 0, container.NewCenter(control))
	}
	return padded(13, 13, 16, 16, container.NewBorder(nil, nil, nil, end, container.NewVBox(layout.NewSpacer(), text, layout.NewSpacer())))
}

var (
	picturesMu sync.Mutex
	pictures   = map[string]image.Image{}
)

// remotePicture shows a picture from the web once it has loaded.
func remotePicture(address string, size fyne.Size) fyne.CanvasObject {
	view := canvas.NewImageFromImage(nil)
	view.FillMode = canvas.ImageFillContain
	view.SetMinSize(size)
	background := canvas.NewRectangle(colCode)
	background.CornerRadius = 8
	picturesMu.Lock()
	cached := pictures[address]
	picturesMu.Unlock()
	if cached != nil {
		view.Image = cached
	} else if address != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
			if err != nil {
				return
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return
			}
			defer response.Body.Close()
			decoded, _, err := image.Decode(io.LimitReader(response.Body, 8<<20))
			if err != nil {
				return
			}
			picturesMu.Lock()
			pictures[address] = decoded
			picturesMu.Unlock()
			fyne.Do(func() {
				view.Image = decoded
				view.Refresh()
			})
		}()
	}
	return container.NewStack(background, view)
}

func iconImage(icon fyne.Resource, ink color.NRGBA, size float32) fyne.CanvasObject {
	view := canvas.NewImageFromResource(tinted(icon, ink))
	view.SetMinSize(fyne.NewSquareSize(size))
	return container.NewCenter(view)
}

func compactCount(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func countText(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%s %ss", compactCount(n), word)
}

// ago says how long ago a time was, as people say it.
func ago(at time.Time) string {
	if at.IsZero() {
		return "a while ago"
	}
	since := time.Since(at)
	switch {
	case since < time.Hour:
		return "just now"
	case since < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(since.Hours()))
	case since < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(since.Hours()/24))
	case since < 730*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(since.Hours()/24/30))
	}
	return fmt.Sprintf("%d years ago", int(since.Hours()/24/365))
}
