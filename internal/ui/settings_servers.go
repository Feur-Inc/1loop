// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Servers (#639). Settings are this computer's, whatever project
// is open: each paired server has its own group under a Servers heading,
// with its pages (Connections, Chat names, Agent, Extensions, Connectors,
// Installs, Devices, Updates), read and changed through its API. A server's
// page names it in its title, says whether it answers and, when it does
// not, why the page has nothing else.
//
// The server the window works on is reached through the window's own
// connection (remote_desktop.go). Another one gets a connection of the
// settings' own, opened when one of its pages is shown and closed when
// Settings leave its group.

import (
	"context"
	"errors"
	"image/color"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
	registry "1loop/internal/settings"
	"1loop/internal/update"
)

// serverPageMark joins a page's name and its server's address in the page's
// ID: "Connections @ https://192.168.1.20:47101".
const serverPageMark = " @ "

// serverPageID names a server's page among the settings' pages.
func serverPageID(address, page string) string { return page + serverPageMark + address }

// serverPageOf splits a server page's ID into its server's address and the
// page's name; ok is false for a page of this computer.
func serverPageOf(id string) (address, page string, ok bool) {
	page, address, ok = strings.Cut(id, serverPageMark)
	return address, page, ok
}

// serverHead is a server page's heading, with a control at its end.
type serverHead func(trailing fyne.CanvasObject) fyne.CanvasObject

// serverPage is one of the pages every server has.
type serverPage struct {
	name   string
	scoped bool // this computer has the same setting: the page says whose chats use it
	build  func(view *settingsView, remote *remoteDesktop, id string, head serverHead) fyne.CanvasObject
	// find is what the search finds on it: this computer's same settings
	// (by key), or settings of its own.
	find []registry.Setting
}

// serverPages are a server's pages, in the order of its group.
func serverPages() []serverPage {
	same := func(keys ...string) []registry.Setting {
		var found []registry.Setting
		for _, key := range keys {
			if setting, ok := registry.Lookup(key); ok {
				found = append(found, setting)
			}
		}
		return found
	}
	return []serverPage{
		{name: "Connections", scoped: true, find: same("models.connections"),
			build: func(_ *settingsView, remote *remoteDesktop, _ string, head serverHead) fyne.CanvasObject {
				return remote.connectionSettings(head)
			}},
		{name: "Chat names", scoped: true, find: same(registry.KeyNamingModel),
			build: func(_ *settingsView, remote *remoteDesktop, _ string, head serverHead) fyne.CanvasObject {
				return container.NewVBox(head(nil), card(remote.namingSetting()))
			}},
		{name: "Agent", scoped: true, find: same(registry.KeyApprovalMode, "agent.preferences", registry.KeyMemory, registry.KeyWrapUp, "advanced.hooks"),
			build: func(_ *settingsView, remote *remoteDesktop, _ string, head serverHead) fyne.CanvasObject {
				return remote.agentSettings(head)
			}},
		{name: "Extensions", scoped: true, find: same("tools.extensions"),
			build: func(view *settingsView, remote *remoteDesktop, id string, head serverHead) fyne.CanvasObject {
				return view.ui.extensionSettings(serverExtensions{remote, id}, head(nil))
			}},
		{name: "Connectors", scoped: true, find: same("tools.github"),
			build: func(view *settingsView, remote *remoteDesktop, id string, head serverHead) fyne.CanvasObject {
				return view.ui.connectorSettings(serverGitHub{remote, id}, head(nil))
			}},
		{name: "Installs", find: []registry.Setting{
			{Key: "server.installs", Title: "Chromium and Claude Code", Kind: registry.Action,
				Description: "Install the browser and Claude Code on the server.",
				Keywords:    []string{"install", "chromium", "browser", "claude code", "download"}}},
			build: func(_ *settingsView, remote *remoteDesktop, _ string, head serverHead) fyne.CanvasObject {
				return container.NewVBox(head(nil), remote.installRows(true))
			}},
		{name: "Devices", find: []registry.Setting{
			{Key: "server.devices", Title: "Paired devices", Kind: registry.Action,
				Description: "The devices that use the server, each with Remove.",
				Keywords:    []string{"devices", "pair", "pairing", "unpair", "revoke", "forget", "phone", "token"}}},
			build: func(view *settingsView, remote *remoteDesktop, _ string, head serverHead) fyne.CanvasObject {
				return container.NewVBox(head(nil), view.ui.serverDevicesCard(remote))
			}},
		{name: "Updates", find: []registry.Setting{
			{Key: "server.updates", Title: "Server updates", Kind: registry.Action,
				Description: "The server's version, and its update.",
				Keywords:    []string{"update", "upgrade", "version", "release"}}},
			build: func(_ *settingsView, remote *remoteDesktop, _ string, head serverHead) fyne.CanvasObject {
				return remote.updateSettings(head)
			}},
	}
}

// serverPageNamed is a server's page by its name.
func serverPageNamed(name string) (serverPage, bool) {
	for _, page := range serverPages() {
		if page.name == name {
			return page, true
		}
	}
	return serverPage{}, false
}

// settingsServer is a paired server the settings showed: their own
// connection to it, while one of its pages is on screen.
type settingsServer struct {
	address serverAddress
	remote  *remoteDesktop // nil until a page of it is shown, and after it is left
	state   reach          // reachUnknown while it connects
	problem string
	failed  bool // it could not connect: Reconnect tries again
}

// label is how the settings name a server (serverLabel).
func (remote *remoteDesktop) label() string {
	return remote.parent.serverLabel(remote.client.address.URL)
}

// serverLabel is how the settings name a paired server: its host, as the
// target menu and the sidebar do, or its host and port when another paired
// server has the same host (two servers on one machine).
func (ui *desktopUI) serverLabel(address string) string {
	name := serverName(address)
	for _, other := range ui.serverAddresses() {
		if other != address && serverName(other) == name {
			if parsed, err := url.Parse(address); err == nil && parsed.Host != "" {
				return parsed.Host
			}
		}
	}
	return name
}

// releaseBehind says that a server runs an older release than the latest:
// a dev build is never behind, whatever the release page says.
func releaseBehind(status *update.Status) bool {
	return status != nil && status.Available && update.Code(status.Current) > 0
}

// serverStatus is how a server is, as last seen: by the window's
// connection while it works on it, else by the settings' own connection,
// else by its sidebar link. outdated is a newer release being out.
func (view *settingsView) serverStatus(address string) (state reach, problem string, outdated bool) {
	ui := view.ui
	reached := ui.reaches[serverTarget(address).key()]
	if remote := ui.server; remote != nil && remote.client.address.URL == address {
		outdated = releaseBehind(remote.update)
		switch {
		case reached.state == reachOffline:
			return reachOffline, reached.problem, outdated
		case remote.shown:
			return reachOnline, "", outdated
		}
		return reachUnknown, "", outdated
	}
	if entry := view.servers[address]; entry != nil && (entry.remote != nil || entry.failed) {
		if entry.remote != nil {
			outdated = releaseBehind(entry.remote.update)
		}
		return entry.state, entry.problem, outdated
	}
	return reached.state, reached.problem, false
}

// serverFor is the connection a server's pages use, opened if needed, and
// how the server is.
func (view *settingsView) serverFor(address string) (*remoteDesktop, reach, string) {
	ui := view.ui
	if remote := ui.server; remote != nil && remote.client.address.URL == address {
		state, problem, _ := view.serverStatus(address)
		return remote, state, problem
	}
	entry := view.servers[address]
	if entry == nil {
		link := ui.linkTo(address)
		if link == nil {
			return nil, reachOffline, "This server is not paired."
		}
		entry = &settingsServer{address: link.address}
		view.servers[address] = entry
	}
	if entry.remote == nil && !entry.failed {
		view.dial(entry)
	}
	return entry.remote, entry.state, entry.problem
}

// serverSettingsState is what /api/state says of a server's settings.
type serverSettingsState struct {
	Models                      []remoteapi.Model
	Accounts                    []remoteapi.Account
	DefaultPresets              bool
	Model, Effort, ApprovalMode string
	Tier                        string
	AutoMode                    bool // the server's model has an Auto mode
	NamingModel                 *agent.NamingModel
}

// takeSettings keeps what the server says of its settings.
func (remote *remoteDesktop) takeSettings(settings serverSettingsState) {
	remote.models = settings.Models
	remote.accounts, remote.defaultPresets = settings.Accounts, settings.DefaultPresets
	remote.model, remote.effort, remote.tier = settings.Model, settings.Effort, settings.Tier
	remote.approvalMode, remote.autoMode = settings.ApprovalMode, settings.AutoMode
	remote.namingModel = settings.NamingModel
}

// dial opens the settings' connection to a server: a client of its own,
// which reads the server's state once. Nothing follows its events.
func (view *settingsView) dial(entry *settingsServer) {
	ui := view.ui
	address := entry.address.URL
	entry.state, entry.problem, entry.failed = reachUnknown, "", false
	client, err := newRemoteClient(entry.address)
	if err != nil {
		entry.state, entry.problem, entry.failed = reachOffline, err.Error(), true
		return
	}
	ctx, cancel := context.WithCancel(ui.ctx)
	remote := &remoteDesktop{parent: ui, client: client, ctx: ctx, cancel: cancel, window: ui.window,
		status: serverNote{view, address}, wake: make(chan struct{}, 1), name: serverName(address),
		convs: map[string]*conversation{}, openCommands: map[string]bool{}}
	entry.remote = remote
	go func() {
		attempt, done := context.WithTimeout(ctx, 10*time.Second)
		var state struct {
			Name, Workspace string
			Settings        serverSettingsState
		}
		err := client.json(attempt, "GET", "/api/state", nil, &state)
		done()
		fyne.Do(func() {
			if entry.remote != remote {
				return // left meanwhile
			}
			if err != nil {
				view.hangUp(entry)
				entry.state, entry.problem, entry.failed = reachOffline, err.Error(), true
			} else {
				remote.name, remote.defaultWorkspace, remote.shown = state.Name, state.Workspace, true
				remote.takeSettings(state.Settings)
				entry.state = reachOnline
				go view.checkUpdate(entry, remote)
			}
			view.serverChanged(address)
		})
	}()
}

// checkUpdate asks a server whether a newer version is out, for its row.
func (view *settingsView) checkUpdate(entry *settingsServer, remote *remoteDesktop) {
	ctx, cancel := context.WithTimeout(remote.ctx, 40*time.Second)
	defer cancel()
	var status update.Status
	if remote.client.json(ctx, "GET", "/api/update", nil, &status) != nil || !releaseBehind(&status) {
		return // up to date, a dev build, or an older server without updates
	}
	fyne.Do(func() {
		if entry.remote == remote {
			remote.update = &status
			view.serverChanged(entry.address.URL)
		}
	})
}

// hangUp closes the settings' connection to a server.
func (view *settingsView) hangUp(entry *settingsServer) {
	if entry.remote != nil {
		entry.remote.cancel()
		entry.remote.client.http.CloseIdleConnections()
		entry.remote = nil
	}
	entry.state, entry.problem, entry.failed = reachUnknown, "", false
}

// release closes the settings' connections, but the one to the server whose
// group is shown.
func (view *settingsView) release(except string) {
	for address, entry := range view.servers {
		if address != except {
			view.hangUp(entry)
		}
	}
}

// reconnect tries a server again, from its page.
func (view *settingsView) reconnect(address string) {
	ui := view.ui
	if remote := ui.server; remote != nil && remote.client.address.URL == address {
		ui.setReach(serverTarget(address), reachUnknown, "")
		remote.notify() // its next read says how it is
	} else if entry := view.servers[address]; entry != nil {
		view.hangUp(entry)
	}
	if link := ui.linkTo(address); link != nil {
		select {
		case link.wake <- struct{}{}:
		default:
		}
	}
	view.show(view.page)
}

// serverChanged follows a server's state: its row in the sidebar, and its
// page when it showed only that it connects or does not answer.
func (view *settingsView) serverChanged(address string) {
	if !view.open || view.ui.home != "" {
		return
	}
	if entry := view.servers[address]; entry != nil && entry.failed && view.ui.reaches[serverTarget(address).key()].state == reachOnline {
		entry.failed = false // its link reaches it again: so will the page
	}
	view.renderServers()
	if view.server == address && view.waiting && !view.searching && view.at(view.page) {
		view.showNow(view.page)
	}
}

// serverNote is remoteDesktop.status of the settings' connections: what
// the window's would write under the conversation (a change that failed,
// an update going on) shows in a notice.
type serverNote struct {
	view    *settingsView
	address string
}

func (note serverNote) SetText(text string) {
	if text != "" {
		note.view.ui.notice(note.view.ui.serverLabel(note.address), text)
	}
}

// serverWord is a server's state in a word, as the sidebar and its pages
// say it. An update being out is said beside it, not instead.
func serverWord(state reach) (string, color.Color) {
	switch state {
	case reachOffline:
		return "offline", colFaint
	case reachUnknown:
		return "connecting…", colAmber
	}
	return "connected", colAdded
}

// serverMark is a server's state beside its name in the sidebar, short
// enough for a narrow one: a dot while it answers or connects, the word
// too while it does not. Its page's heading says it in full.
func serverMark(state reach, word string) string {
	switch state {
	case reachOnline:
		return "●"
	case reachUnknown:
		return "◌"
	}
	return "○ " + word
}

// plainProblem says why a server does not answer in words, from the error
// its client gave; the error itself goes to the developer log.
func plainProblem(address, problem string) string {
	if problem == "" {
		return ""
	}
	platform.Devf("remote", "%s does not answer: %s", address, problem)
	where := address
	if parsed, err := url.Parse(address); err == nil && parsed.Host != "" {
		where = parsed.Host
	}
	lower := strings.ToLower(problem)
	switch {
	case strings.Contains(lower, "connection refused"):
		return "Connection refused: nothing answers at " + where + "."
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "server misbehaving"):
		return where + " can't be found on the network."
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"):
		return "No answer from " + where + " in time."
	case strings.Contains(lower, "no route to host"), strings.Contains(lower, "network is unreachable"):
		return where + " can't be reached from this computer's network."
	case strings.Contains(lower, "connection reset"), strings.Contains(lower, "eof"):
		return where + " closed the connection."
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "not paired"):
		return "It no longer accepts this computer: pair it again."
	}
	if _, rest, ok := strings.Cut(problem, "\": "); ok { // Get "https://…": what went wrong
		return rest
	}
	return problem
}

// updateNote is what a server's Updates row and page heading say while a
// newer release is out.
const updateNote = "update available"

// renderServers lists the paired servers under the sections, each with its
// state; the one whose page is shown has its pages under it, unless its
// row folded them.
func (view *settingsView) renderServers() {
	if view.serverNav == nil {
		return
	}
	for key := range view.rows {
		if _, _, ok := serverPageOf(key); ok {
			delete(view.rows, key)
		}
	}
	view.serverNav.Objects, view.serverRow = nil, nil
	addresses := view.ui.serverAddresses()
	if view.ui.home != "" || len(addresses) == 0 { // the browser's settings are its server's already
		view.serverNav.Refresh()
		return
	}
	view.serverNav.Add(padded(14, 6, 12, 0, textLine("Servers", 13, colFaint)))
	for _, address := range addresses {
		address := address
		state, _, outdated := view.serverStatus(address)
		word, _ := serverWord(state)
		shown := address == view.server // one of its pages is on screen
		open := shown && !view.folded
		icon := iconChevronRight
		if open {
			icon = iconChevron
		}
		header := newRow(view.ui.serverLabel(address), icon, styleNav, func() {
			if shown { // its chevron folds or unfolds its pages, the page stays
				view.folded = !view.folded
				view.renderServers()
				view.keepInView()
				return
			}
			view.show(serverPageID(address, "Connections"))
		})
		header.mark = serverTarget(address).mark("")
		header.accent = shown
		header.selected = shown && view.folded // where you are, its pages folded
		header.truncate = true
		if shown {
			view.serverRow = header
		}
		header.trailing = serverMark(state, word)
		view.serverNav.Add(header)
		if !open {
			continue
		}
		for _, page := range serverPages() {
			id := serverPageID(address, page.name)
			row := newRow(page.name, nil, styleNav, view.link(id))
			row.selected = id == view.page
			if page.name == "Updates" && outdated {
				row.trailing = updateNote
			}
			view.rows[id] = row
			view.serverNav.Add(padded(0, 0, 22, 0, row))
		}
	}
	view.serverNav.Refresh()
	if view.sideScroll != nil {
		view.sideScroll.fit() // the rows just listed can be scrolled to at once
	}
}

// settingsScroll is the settings' sidebar scroll. Fyne's scroll moves only
// as far as its content's size, which follows the content's rows when it
// is laid out again: fit sizes it to them first, so a server's pages
// just listed can be scrolled to.
type settingsScroll struct{ kineticScroll }

func newSettingsScroll(content fyne.CanvasObject) *settingsScroll {
	scroll := &settingsScroll{}
	scroll.Direction = container.ScrollVerticalOnly
	scroll.Content = content
	scroll.ExtendBaseWidget(scroll)
	return scroll
}

// fit gives the content the height its rows need, and the bar its length.
func (scroll *settingsScroll) fit() {
	if scroll.Content == nil {
		return
	}
	if want := scroll.Content.MinSize().Max(scroll.Size()); scroll.Content.Size() != want {
		scroll.Content.Resize(want)
		scroll.Refresh()
	}
}

func (scroll *settingsScroll) Scrolled(ev *fyne.ScrollEvent) {
	scroll.fit()
	before := scroll.Offset.Y
	scroll.kineticScroll.Scrolled(ev)
	platform.Devf("ui", "settings sidebar wheel %.0f: offset %.0f → %.0f, view %.0f, content %.0f (rows %.0f)",
		ev.Scrolled.DY, before, scroll.Offset.Y, scroll.Size().Height, scroll.Content.Size().Height, scroll.Content.MinSize().Height)
}

// sideColumn is the settings' sidebar column: a wheel anywhere on it, its
// search and title included, scrolls its sections and servers. The driver
// gives a wheel to the last scroll under the pointer: the column's own
// takes it where nothing inside it is one.
type sideColumn struct {
	widget.BaseWidget
	content fyne.CanvasObject
	scroll  *settingsScroll
}

func newSideColumn(content fyne.CanvasObject, scroll *settingsScroll) *sideColumn {
	column := &sideColumn{content: content, scroll: scroll}
	column.ExtendBaseWidget(column)
	return column
}

func (column *sideColumn) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(column.content)
}

func (column *sideColumn) Scrolled(ev *fyne.ScrollEvent) { column.scroll.Scrolled(ev) }

// keepInView scrolls the settings' sidebar to the selected entry, or the
// open server's row, when it is out of sight.
func (view *settingsView) keepInView() {
	scroll := view.sideScroll
	if scroll == nil || scroll.Size().Height <= 0 {
		return
	}
	var target fyne.CanvasObject
	for _, row := range view.rows {
		if row.selected {
			target = row
		}
	}
	if target == nil && view.serverRow != nil { // its pages folded
		target = view.serverRow
	}
	if target == nil {
		return
	}
	scroll.Refresh() // the rows just listed take their places
	scroll.Content.Refresh()
	at, ok := offsetIn(scroll.Content, target)
	if !ok {
		return
	}
	y := at.Y
	top, height := scroll.Offset.Y, scroll.Size().Height
	bottom := y + target.Size().Height
	switch {
	case y < top:
		scroll.ScrollToOffset(fyne.NewPos(0, max(0, y-8)))
	case bottom > top+height:
		scroll.ScrollToOffset(fyne.NewPos(0, bottom-height+8))
	}
}

// buildServer makes a server's page: its heading, then what the server
// says, or why it says nothing.
func (view *settingsView) buildServer(address, name string) fyne.CanvasObject {
	page, _ := serverPageNamed(name)
	remote, state, problem := view.serverFor(address)
	_, _, outdated := view.serverStatus(address)
	head := func(trailing fyne.CanvasObject) fyne.CanvasObject {
		return view.serverHeading(address, page, state, outdated, trailing)
	}
	view.waiting = remote == nil || state != reachOnline
	if !view.waiting {
		return page.build(view, remote, serverPageID(address, name), head)
	}
	label := view.ui.serverLabel(address)
	body := settingRow("Connecting to "+label+"…", "Its "+strings.ToLower(name)+" show here once it answers.", nil)
	if state == reachOffline {
		detail := "Its settings show here once it is back."
		if problem != "" {
			detail += "\n" + plainProblem(address, problem)
		}
		body = settingRow(label+" doesn't answer", detail, nil)
	}
	return container.NewVBox(head(nil), card(body))
}

// serverHeading is a server page's title, which names the server, and the
// server's state under it: a Reconnect button while it does not answer,
// and whose chats the page's settings are for.
func (view *settingsView) serverHeading(address string, page serverPage, state reach, outdated bool, trailing fyne.CanvasObject) fyne.CanvasObject {
	label := view.ui.serverLabel(address)
	word, ink := serverWord(state)
	dot := canvas.NewCircle(ink)
	line := container.New(layout.NewCustomPaddedHBoxLayout(6),
		container.NewCenter(container.NewGridWrap(fyne.NewSquareSize(8), dot)),
		textLine(strings.ToUpper(word[:1])+word[1:], 12.5, colMuted))
	if outdated && state == reachOnline {
		line.Add(textLine("· "+strings.ToUpper(updateNote[:1])+updateNote[1:], 12.5, colAmber))
	}
	if page.scoped {
		line.Add(textLine("· Used by chats on "+label, 12.5, colMuted))
	}
	var end fyne.CanvasObject = trailing
	if state == reachOffline {
		end = newRow("Reconnect", iconArrowTurn, styleOutline, func() { view.reconnect(address) })
	}
	title := container.NewBorder(nil, nil, nil, end, container.NewVBox(textLine(page.name+" · on "+label, 26, colText), line))
	return padded(8, 14, 0, 0, title)
}

// scope is the line under a page of this computer whose settings each
// server has too: whose chats they are for. Without a server, it says
// nothing.
func (view *settingsView) scope() fyne.CanvasObject {
	if view.ui.home != "" || len(view.ui.serverAddresses()) == 0 {
		return layout.NewSpacer()
	}
	return padded(0, 6, 2, 0, textLine("Used by chats on this computer", 12.5, colFaint))
}

// olderServer says that a server answers a page's request with 404: it
// runs a version from before the page.
func olderServer(err error) bool {
	var refused remoteError
	return errors.As(err, &refused) && refused.status == http.StatusNotFound
}

// olderText is what a page says of a server too old for it.
func olderText(remote *remoteDesktop) string {
	return remote.label() + " runs an older 1loop without this page. Update it from its Updates page."
}

// serverMatch is a server's setting the search found.
type serverMatch struct {
	address string // "" for this computer's
	page    string // the server's page
	match   registry.Match
}

// findSettings searches this computer's settings and every paired
// server's, best first; a server's are found by its name too.
func (view *settingsView) findSettings(query string) []serverMatch {
	var found []serverMatch
	for _, match := range registry.Search(registry.On(registry.OnDesktop), query) {
		found = append(found, serverMatch{match: match})
	}
	if view.ui.home == "" {
		for _, address := range view.ui.serverAddresses() {
			label := view.ui.serverLabel(address)
			var settings []registry.Setting
			pageOf := map[string]string{}
			for _, page := range serverPages() {
				for _, setting := range page.find {
					setting.Key = serverPageID(address, page.name) + "#" + setting.Key
					setting.Section, setting.Page = "", page.name
					setting.Keywords = append(append([]string(nil), setting.Keywords...), label, "server")
					pageOf[setting.Key] = page.name
					settings = append(settings, setting)
				}
			}
			for _, match := range registry.Search(settings, query) {
				found = append(found, serverMatch{address: address, page: pageOf[match.Setting.Key], match: match})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].match.Score > found[j].match.Score })
	return found
}

// computerConfig is this computer's configuration, put aside while a
// server is the window's target.
func (ui *desktopUI) computerConfig() agent.Config {
	if ui.server != nil {
		return ui.localCfg
	}
	return ui.cfg
}
