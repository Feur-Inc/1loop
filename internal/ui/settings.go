// SPDX-License-Identifier: MPL-2.0

package ui

// Settings are a page, not a window: opening them replaces the sidebar with
// their sections and the conversation with the section chosen, the whole
// window being theirs. Each section is a column of cards whose rows carry a
// title, an explanation and a control. Going deeper (a connection, then one
// of its models) opens another page in the same place, with a way back,
// instead of a window over a window.
//
// Most settings apply as they are changed. A connection is edited as a
// draft, checked and saved with the buttons pinned under its page.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/computeruse"
	"1loop/internal/core"
	"1loop/internal/githubconnector"
	"1loop/internal/gitrepo"
	"1loop/internal/platform"
	registry "1loop/internal/settings"
)

func note(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.Importance = widget.LowImportance
	return label
}

func cloneConnection(conn agent.Connection) agent.Connection {
	b, _ := json.Marshal(conn)
	var copy agent.Connection
	_ = json.Unmarshal(b, &copy)
	return copy
}

func jsonEntry(value map[string]any) *widget.Entry {
	entry := widget.NewMultiLineEntry()
	entry.TextStyle = fyne.TextStyle{Monospace: true}
	entry.SetMinRowsVisible(5)
	entry.SetText(agent.JSONText(value))
	return entry
}

// ---------------------------------------------------------------------------
// The page and its navigation.

// The sections are the shared list's (internal/settings), with an icon each.
var sectionIcons = map[string]fyne.Resource{
	registry.General:  iconSliders,
	registry.Models:   iconPlug,
	registry.Agent:    iconShield,
	registry.Tools:    iconPuzzle,
	registry.Phone:    iconPhone,
	registry.Updates:  iconArrowTurn,
	registry.Advanced: iconTerminal,
}

// subPages are pages of their own, opened from a row of their section.
var subPages = map[string]string{
	"Extensions":           registry.Tools,
	"Connectors":           registry.Tools,
	registry.ShortcutsPage: registry.General,
}

// pageAliases are pages of earlier versions, still named by links and
// messages: they open where their settings went.
var pageAliases = map[string]string{
	"SSH machines": "General",
	"Connections":  "Models & accounts",
	"Instructions": "Agent & approvals",
	"Phone remote": "Phone",
}

// pageSection is the title of the section a page belongs to.
func pageSection(page string) string {
	if section, ok := subPages[page]; ok {
		return registry.SectionTitle(section)
	}
	return page
}

// settingsFrame is one page of the settings: a section, or what was opened
// from it.
type settingsFrame struct {
	content     fyne.CanvasObject
	actions     []fyne.CanvasObject // pinned under the page: Cancel, Save
	leave       func()              // called when the page is left without saving
	beforeLeave func(func())        // asks what to do with an unsaved draft
}

type settingsView struct {
	ui      *desktopUI
	open    bool
	section string // the section selected in the sidebar
	page    string // the page shown: the section, or one of its sub-pages
	rows    map[string]*tapRow
	side    fyne.CanvasObject // what the sidebar shows
	view    *fyne.Container   // what the main area shows
	column  *fyne.Container
	scroll  *kineticScroll
	bar     *fyne.Container
	barRow  *fyne.Container
	frames  []settingsFrame // the page, then what was opened from it
	panelOn bool            // the tool panel's button was showing

	search    *widget.Entry
	searching bool                      // the results are on screen
	anchors   map[string]*settingAnchor // the rows of the page shown, by setting

	// Servers (settings_servers.go).
	serverNav  *fyne.Container            // the Servers group of the sidebar
	sideScroll *settingsScroll            // the sidebar's sections and servers, scrolled
	server     string                     // the server whose page is shown, "" for this computer's
	folded     bool                       // that server's row folded its pages
	serverRow  *tapRow                    // and its row
	servers    map[string]*settingsServer // the settings' own connections, by address
	waiting    bool                       // the server's page shows only that it connects or does not answer
}

func (ui *desktopUI) buildSettings() {
	view := &settingsView{ui: ui, rows: map[string]*tapRow{}, section: "General", page: "General", anchors: map[string]*settingAnchor{}, servers: map[string]*settingsServer{}}
	back := newRow("Back to 1loop", iconChevronLeft, styleNav, view.close)
	nav := container.New(layout.NewCustomPaddedVBoxLayout(1))
	for _, section := range registry.Sections() {
		name := section.Title
		if !settingsSectionShown(name) {
			continue
		}
		row := newRow(name, sectionIcons[section.ID], styleNav, func() { view.show(name) })
		view.rows[name] = row
		nav.Add(row)
		if section.ID == registry.Models { // beside the accounts it counts (#415)
			usage := newRow(UsagePage, iconUsage, styleNav, func() { view.show(UsagePage) })
			view.rows[UsagePage] = usage
			nav.Add(usage)
		}
	}
	view.search = widget.NewEntry()
	view.search.SetPlaceHolder("Search settings")
	view.search.OnChanged = view.find
	view.search.OnSubmitted = func(query string) {
		if matches := view.findSettings(query); len(matches) > 0 {
			view.openMatch(matches[0])
		}
	}
	view.serverNav = container.New(layout.NewCustomPaddedVBoxLayout(1))
	// The sections and servers scroll, under what stays at the top: with a
	// server's pages listed, they run below a small window's bottom.
	view.sideScroll = newSettingsScroll(container.NewVBox(nav, view.serverNav))
	view.side = newSideColumn(container.NewBorder(container.NewVBox(back, padded(14, 6, 12, 0, textLine("Settings", 13, colFaint)), padded(0, 8, 6, 6, view.search)),
		nil, nil, nil, view.sideScroll), view.sideScroll)
	view.side.Hide()

	view.column = container.NewStack()
	view.scroll = newKineticVScroll(container.New(columnLayout{width: 760, inset: 28}, view.column))
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	view.barRow = container.New(layout.NewCustomPaddedHBoxLayout(8))
	view.bar = container.NewVBox(rule, container.New(columnLayout{width: 760, inset: 12}, container.NewHBox(layout.NewSpacer(), view.barRow)))
	view.bar.Hide()
	view.view = container.NewBorder(nil, view.bar, nil, nil, container.NewThemeOverride(view.scroll, flatScrollTheme{loopTheme{theme.DefaultTheme()}}))
	view.view.Hide()
	ui.prefs = view
}

// settings opens the page (the sidebar's Settings row).
func (ui *desktopUI) settings() { ui.prefs.openOn("") }

// openOn opens the settings on a page: a section's title or a sub-page's.
func (view *settingsView) openOn(page string) {
	ui := view.ui
	if page == "" {
		page = view.page
	}
	if !view.open {
		platform.Devf("ui", "settings opened")
		view.open = true
		ui.clearSelection()
		view.panelOn = ui.panelToggle.Visible()
		ui.panelToggle.Hide()
		ui.sideApp.Hide()
		view.side.Show()
		view.sideScroll.fit()
		ui.paneArea().Hide()
		view.view.Show()
		ui.header.SetText("Settings")
	}
	view.show(page)
}

// close goes back to the conversation.
func (view *settingsView) close() {
	if !view.open {
		return
	}
	view.confirmLeave(0, view.closeNow)
}

func (view *settingsView) closeNow() {
	ui := view.ui
	for len(view.frames) > 0 {
		view.drop()
	}
	view.open = false
	view.release("")
	view.clearSearch()
	view.side.Hide()
	ui.sideApp.Show()
	view.view.Hide()
	ui.paneArea().Show()
	if view.panelOn {
		ui.panelToggle.Show()
	}
	ui.windowOf().panel.redraw()
	ui = ui.focused() // the pane with the focus, in a split window (panes.go)
	ui.updateHeader()
	ui.relayoutColumn()
	ui.curtain.reveal()
	ui.window.Canvas().Focus(ui.prompt)
}

// show opens a page, leaving whatever was open.
func (view *settingsView) show(page string) {
	view.confirmLeave(0, func() { view.showNow(page) })
}

// base is how many frames the page shown has of its own: a sub-page is
// opened over its section. A server's page is its own section.
func (view *settingsView) base() int {
	if _, ok := subPages[view.page]; ok {
		return 2
	}
	return 1
}

// at tells whether a page is on screen, with nothing opened over it.
func (view *settingsView) at(page string) bool {
	return view.open && view.page == page && len(view.frames) == view.base()
}

// on tells whether a page is the one shown, or under what is.
func (view *settingsView) on(page string) bool { return view.open && view.page == page }

func (view *settingsView) showNow(page string) {
	for len(view.frames) > 0 {
		view.drop()
	}
	view.clearSearch()
	if alias, ok := pageAliases[page]; ok {
		page = alias
	}
	section := pageSection(page)
	view.server = ""
	if address, name, ok := serverPageOf(page); ok {
		if _, known := serverPageNamed(name); known {
			view.server, section = address, page
		}
	}
	view.release(view.server)
	view.page, view.folded = page, false // the group's pages are listed under its server
	view.renderServers()
	if _, ok := view.rows[section]; !ok {
		section, page, view.server = "General", "General", ""
		view.renderServers()
	}
	view.section, view.page = section, page
	view.waiting = false
	for name, row := range view.rows {
		row.SetSelected(name == section)
	}
	view.anchors = map[string]*settingAnchor{}
	view.frames = []settingsFrame{{content: view.build(section)}}
	if page != section {
		view.frames = append(view.frames, settingsFrame{content: view.build(page)})
	}
	view.present()
	view.keepInView()
}

// build makes a page from what is stored now.
func (view *settingsView) build(page string) fyne.CanvasObject {
	ui := view.ui
	if address, name, ok := serverPageOf(page); ok {
		return view.buildServer(address, name)
	}
	if content := ui.browserSettings(page); content != nil {
		return content
	}
	switch page {
	case "Models & accounts":
		return ui.modelSettings()
	case "Agent & approvals":
		return ui.agentSettings()
	case "Tools & extensions":
		return ui.toolSettings()
	case "Phone":
		return ui.remoteSettings()
	case "Updates":
		return ui.updateSettings()
	case "Advanced":
		return ui.advancedSettings()
	case registry.ShortcutsPage:
		return ui.shortcutSettings()
	case "Extensions":
		return ui.extensionSettings(localExtensions{ui}, container.NewVBox(view.pageTitle("Extensions", "Tools & extensions", nil), view.scope()))
	case "Connectors":
		return ui.connectorSettings(localGitHub{}, container.NewVBox(view.pageTitle("Connectors", "Tools & extensions", nil), view.scope()))
	case UsagePage:
		return ui.usageSettings()
	}
	return ui.generalSettings()
}

// link shows a sub-page over its section, from one of the section's rows.
func (view *settingsView) link(page string) func() {
	return func() { view.show(page) }
}

// push opens a page over the current one.
func (view *settingsView) push(frame settingsFrame) {
	view.frames = append(view.frames, frame)
	view.present()
}

// drop leaves the page on top.
func (view *settingsView) drop() {
	if n := len(view.frames); n > 0 {
		if leave := view.frames[n-1].leave; leave != nil {
			leave()
		}
		view.frames = view.frames[:n-1]
	}
}

// back returns to the page below, or to the conversation from a section.
func (view *settingsView) back() {
	if len(view.frames) <= 1 {
		view.close()
		return
	}
	if len(view.frames) == view.base() { // from a sub-page to its section, built again
		view.show(view.section)
		return
	}
	view.confirmLeave(len(view.frames)-1, func() {
		view.drop()
		view.present()
	})
}

func (view *settingsView) confirmLeave(keep int, next func()) {
	for i := len(view.frames) - 1; i >= keep; i-- {
		if ask := view.frames[i].beforeLeave; ask != nil {
			ask(next)
			return
		}
	}
	next()
}

// done leaves the page on top after it saved: the one below is built again
// from what is now stored.
func (view *settingsView) done() {
	if n := len(view.frames); n > 1 {
		view.frames[n-1].leave = nil
		view.frames[n-1].beforeLeave = nil
	}
	if len(view.frames) == view.base()+1 {
		view.show(view.page)
		return
	}
	view.back()
}

func (view *settingsView) present() {
	view.searching = false
	frame := view.frames[len(view.frames)-1]
	view.column.Objects = []fyne.CanvasObject{frame.content}
	view.column.Refresh()
	view.barRow.Objects = frame.actions
	if len(frame.actions) > 0 {
		view.bar.Show()
	} else {
		view.bar.Hide()
	}
	view.view.Refresh()
	view.scroll.ScrollToTop()
	view.ui.curtain.reveal()
}

// ---------------------------------------------------------------------------
// What the pages are made of.

func plainFlow(text string, size float32, ink color.Color) *flowText {
	return newFlowText([]span{{text: text}}, size, ink)
}

// pageTitle heads a page; back, when given, names where the page was opened
// from and returns there.
func (settings *settingsView) pageTitle(title, back string, trailing fyne.CanvasObject) fyne.CanvasObject {
	heading := container.NewBorder(nil, nil, nil, trailing, textLine(title, 26, colText))
	if back == "" {
		return padded(8, 14, 0, 0, heading)
	}
	link := newRow(back, iconChevronLeft, styleChip, settings.back)
	return container.NewVBox(container.NewHBox(link), padded(4, 14, 0, 0, heading))
}

func sectionTitle(text string) fyne.CanvasObject {
	title := textLine(text, 14, colText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	return padded(18, 8, 2, 0, title)
}

// card frames rows, with a rule between two of them.
func card(rows ...fyne.CanvasObject) fyne.CanvasObject {
	box := container.New(layout.NewCustomPaddedVBoxLayout(0))
	for i, row := range rows {
		if i > 0 {
			rule := canvas.NewRectangle(colBorderSoft)
			rule.SetMinSize(fyne.NewSize(0, 1))
			box.Add(padded(0, 0, 16, 16, rule))
		}
		box.Add(row)
	}
	return surface(box, colSurface, colBorder, 12, 0)
}

// settingRow is a title and its explanation, with a control at the end.
func settingRow(title, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	return settingRowTitled(plainFlow(title, 14, colText), detail, control)
}

// settingRowTitled is settingRow with a title of its own, such as titleInfo's.
func settingRowTitled(title fyne.CanvasObject, detail string, control fyne.CanvasObject) fyne.CanvasObject {
	text := container.New(layout.NewCustomPaddedVBoxLayout(3), title)
	if detail != "" {
		text.Add(plainFlow(detail, 12.5, colMuted))
	}
	var end fyne.CanvasObject
	if control != nil {
		end = padded(0, 0, 20, 0, container.NewCenter(control))
	}
	// The text keeps the row's width and sits at mid-height beside a taller control.
	return padded(13, 13, 16, 16, container.NewBorder(nil, nil, nil, end, container.NewVBox(layout.NewSpacer(), text, layout.NewSpacer())))
}

// settingStack is a title and its explanation above a wide control.
func settingStack(title, detail string, below fyne.CanvasObject) fyne.CanvasObject {
	return settingStackTitled(plainFlow(title, 14, colText), detail, below)
}

// settingStackTitled is settingStack with a title of its own.
func settingStackTitled(title fyne.CanvasObject, detail string, below fyne.CanvasObject) fyne.CanvasObject {
	text := container.New(layout.NewCustomPaddedVBoxLayout(3), title)
	if detail != "" {
		text.Add(plainFlow(detail, 12.5, colMuted))
	}
	return padded(13, 14, 16, 16, container.New(layout.NewCustomPaddedVBoxLayout(9), text, below))
}

// iconInfo is the (i) after a title that has a longer explanation.
var iconInfo = lineIcon("info", `<circle cx="8" cy="8" r="5.5"/><path d="M8 7.2v3.8M8 4.8h.01" stroke-width="1.8"/>`)

// titleInfo is a title with an (i) after it: the explanation shows in a
// tooltip while the (i) is hovered, so the row's own text can stay short.
func titleInfo(tips *tipLayer, title, explanation string) fyne.CanvasObject {
	mark := newRow("", iconInfo, styleIcon, nil)
	mark.onHover = func(in bool) {
		if in {
			tips.show(mark, explanation)
		} else {
			tips.hide()
		}
	}
	// One line: in a box a wrapping text gets its narrowest width and
	// breaks after every word.
	return container.NewHBox(container.NewCenter(textLine(title, 14, colText)), padded(2, 0, 6, 0, mark))
}

// widthLayout gives its content a width of its own and the height it
// needs at that width, as it changes: a message that wraps.
type widthLayout struct{ width float32 }

func (wide *widthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for _, object := range objects {
		height = fyne.Max(height, object.MinSize().Height)
	}
	return fyne.NewSize(wide.width, height)
}

func (wide *widthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
}

// sized gives a control (an entry, a picker) a width of its own.
func sized(width float32, object fyne.CanvasObject) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(width, object.MinSize().Height), object)
}

// toggle is an on/off switch.
type toggle struct {
	widget.BaseWidget
	on       bool
	at       float32 // 0 off .. 1 on, eased
	anim     *fyne.Animation
	onChange func(bool)
}

func newToggle(on bool, onChange func(bool)) *toggle {
	switchControl := &toggle{on: on, onChange: onChange}
	if on {
		switchControl.at = 1
	}
	switchControl.ExtendBaseWidget(switchControl)
	return switchControl
}

func (switchControl *toggle) Cursor() desktop.Cursor { return desktop.PointerCursor }

// set changes the switch without telling onChange.
func (switchControl *toggle) set(on bool) {
	if switchControl.on == on {
		return
	}
	switchControl.on = on
	if switchControl.anim != nil {
		switchControl.anim.Stop()
	}
	from, to := switchControl.at, float32(0)
	if on {
		to = 1
	}
	switchControl.anim = motion(130*time.Millisecond, fyne.AnimationEaseOut, func(p float32) {
		switchControl.at = from + (to-from)*p
		switchControl.Refresh()
	})
}

func (switchControl *toggle) Tapped(*fyne.PointEvent) {
	switchControl.set(!switchControl.on)
	if switchControl.onChange != nil {
		switchControl.onChange(switchControl.on)
	}
}

func (switchControl *toggle) CreateRenderer() fyne.WidgetRenderer {
	renderer := &toggleRenderer{toggle: switchControl, track: canvas.NewRectangle(colBorder), knob: canvas.NewCircle(colMuted)}
	renderer.track.CornerRadius = rounded(10)
	renderer.Refresh()
	return renderer
}

type toggleRenderer struct {
	toggle *toggle
	track  *canvas.Rectangle
	knob   *canvas.Circle
}

func (renderer *toggleRenderer) MinSize() fyne.Size { return fyne.NewSize(36, 20) }
func (renderer *toggleRenderer) Layout(fyne.Size) {
	renderer.track.Resize(fyne.NewSize(36, 20))
	renderer.knob.Resize(fyne.NewSquareSize(14))
	renderer.knob.Move(fyne.NewPos(3+16*renderer.toggle.at, 3))
}

func (renderer *toggleRenderer) Refresh() {
	renderer.track.FillColor = mix(colBorder, colAmber, renderer.toggle.at)
	renderer.knob.FillColor = mix(colMuted, colInk, renderer.toggle.at)
	renderer.Layout(renderer.toggle.Size())
	renderer.track.Refresh()
	renderer.knob.Refresh()
}

func (renderer *toggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.track, renderer.knob}
}

func (renderer *toggleRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// General.

func (ui *desktopUI) generalSettings() fyne.CanvasObject {
	view := ui.prefs
	change := newRow("Open…", nil, styleOutline, func() {
		ui.pick(pickRequest{title: "Open a project", start: ui.computerConfig().Workspace, folder: true}, func(paths []string) {
			root, err := agent.ResolveWorkspace(paths[0])
			if err != nil {
				ui.notice("Workspace", err.Error())
				return
			}
			ui.useComputer() // a folder of this computer: its chats run here
			if root != ui.cfg.Workspace {
				ui.selectProject(root) // shows the project's start screen: come back here
				view.openOn("General")
			}
		})
	})
	sortPicker := newPicker(ui.window, "Worktree order", styleField, nil)
	order, _ := registry.Lookup(registry.KeyWorktreeSort)
	for _, value := range order.Values {
		sortPicker.Options = append(sortPicker.Options, value.Label)
	}
	current := ui.worktreeSort
	if sortLabels[current] == "" {
		current = gitrepo.SortRecent
	}
	sortPicker.SetSelected(order.Label(current))
	sortPicker.onChanged = func(label string) {
		for _, value := range order.Values {
			if value.Label == label {
				ui.setWorktreeSort(value.ID)
			}
		}
	}
	worktrees := view.mark(registry.KeyWorktreeSort, settingRow(order.Title, "", sized(230, sortPicker.row)))
	page := container.NewVBox(view.pageTitle("General", "", nil), view.intro(registry.General), sectionTitle("Workspace"))
	page.Add(card(view.mark("general.workspace", settingRow("Project folder", "", change)), worktrees))
	for _, object := range ui.targetRows() {
		page.Add(object)
	}
	page.Add(sectionTitle("Appearance"))
	page.Add(card(ui.appearanceRows(view)...))
	page.Add(sectionTitle("Window"))
	page.Add(card(view.mark("general.close_to_tray", settingRow("Close to tray", "",
		newToggle(!ui.closeQuits, func(on bool) {
			ui.closeQuits = !on
			platform.Devf("ui", "close to tray %v", on)
			ui.save()
		})))))
	page.Add(sectionTitle("Motion"))
	page.Add(card(view.mark(registry.KeyAnimations, settingRow("Animations", "",
		newToggle(!animationsOff, ui.setAnimations)))))
	page.Add(sectionTitle("Discord"))
	page.Add(card(view.mark(registry.KeyDiscordPresence, settingRow("Discord status", "Shows what the agent is doing on your Discord profile.",
		newToggle(ui.discordPresence, func(on bool) { ui.setDiscordPresence(on, ui.discordDetails) }))),
		view.mark(registry.KeyDiscordDetails, settingRow("Share the chat",
			"Adds the chat's title and project to the status.",
			newToggle(ui.discordDetails, func(on bool) { ui.setDiscordPresence(ui.discordPresence, on) })))))
	page.Add(sectionTitle("Keyboard"))
	page.Add(card(settingRow(registry.ShortcutsPage, "", view.pageButton(registry.ShortcutsPage))))
	page.Add(sectionTitle("Usage"))
	page.Add(card(view.mark("general.usage", settingRow("Usage & spend", "", view.pageButton(UsagePage)))))
	page.Add(sectionTitle("Import"))
	page.Add(card(view.mark("general.import", ui.importRow())))
	return page
}

// setDiscordPresence turns the Discord status on or off, with or without
// the chat's details.
func (ui *desktopUI) setDiscordPresence(on, details bool) {
	ui.discordPresence, ui.discordDetails = on, details
	platform.Devf("ui", "discord status %v, details %v", on, details)
	ui.syncPresence()
	ui.save()
}

// setAnimations turns the interface's animations on or off.
func (ui *desktopUI) setAnimations(on bool) {
	animationsOff = !on
	platform.Devf("ui", "animations %v", on)
	ui.save()
}

// setWorktreeSort orders the worktrees, in the sidebar and the Repository
// window.
func (ui *desktopUI) setWorktreeSort(id string) {
	if id == ui.worktreeSort {
		return
	}
	ui.worktreeSort = id
	ui.save()
	ui.renderChats()
	if ui.repoWindow != nil {
		ui.repoWindow.render()
	}
}

// pageButton opens a sub-page of the section.
func (view *settingsView) pageButton(page string) fyne.CanvasObject {
	open := newRow("Open", iconChevronRight, styleChip, view.link(page))
	open.iconAfter = true
	return open
}

// intro is the line under a section's title, from the shared list.
func (view *settingsView) intro(section string) fyne.CanvasObject {
	for _, entry := range registry.Sections() {
		if entry.ID == section {
			return padded(0, 4, 2, 0, plainFlow(entry.Intro, 13, colMuted))
		}
	}
	return layout.NewSpacer()
}

// modelSettings is Models & accounts: the connections, and what happens at
// a rate limit.
func (ui *desktopUI) modelSettings() fyne.CanvasObject {
	view := ui.prefs
	add := newRow("Add connection", iconPlus, stylePrimary, func() {
		id := core.Identifier()
		ui.connectionEditor(agent.Connection{ID: id, AuthProfile: id, Name: "New connection", Provider: "compatible", Enabled: true, AllModels: true})
	})
	resume := ui.autoResume != nil && ui.autoResume.Enabled
	resumeToggle := newToggle(resume, nil)
	resumeToggle.onChange = func(on bool) {
		if err := ui.setAutoResume(on); err != nil {
			resumeToggle.set(!on)
			ui.configureAutoResume() // no steps yet: they are chosen there
		}
	}
	return container.NewVBox(
		view.pageTitle("Models & accounts", "", nil),
		view.intro(registry.Models),
		view.scope(),
		container.NewBorder(nil, nil, nil, padded(14, 4, 0, 0, add), sectionTitle("Connections")),
		padded(0, 10, 2, 0, plainFlow("Each enabled connection's models appear in the composer.", 13, colMuted)),
		view.mark("models.connections", ui.connectionRows()),
		sectionTitle("Rate limits"),
		card(view.mark(registry.KeyAutoResume, settingRow("Automatic resumption",
			"Waits for the reset, or switches model in your order.",
			container.New(layout.NewCustomPaddedHBoxLayout(14), container.NewCenter(resumeToggle), newRow("Configure…", nil, styleOutline, ui.configureAutoResume))))),
		sectionTitle("Chat names"),
		card(view.mark(registry.KeyNamingModel, ui.namingSetting())),
	)
}

// setAutoResume turns automatic resumption on or off; on needs steps.
func (ui *desktopUI) setAutoResume(on bool) error {
	if on && (ui.autoResume == nil || len(ui.autoResume.Steps) == 0) {
		return errors.New("automatic resumption has no steps yet: choose them in Settings first")
	}
	if ui.autoResume == nil {
		ui.autoResume = &agent.AutoResume{}
	}
	ui.autoResume.Enabled = on
	ui.save()
	ui.remote.settingsChanged()
	return nil
}

// agentSettings is Agent & approvals: permissions, what the agent is told
// and what it remembers, and its context.
func (ui *desktopUI) agentSettings() fyne.CanvasObject {
	view := ui.prefs
	mode := newPicker(ui.window, "Tool permissions", styleField, nil)
	mode.Options = []string{"Approval required", "YOLO mode"}
	modes := []string{"approval", "yolo"}
	if agent.AutoModeOffered(ui.computerConfig()) { // ChatGPT and Claude Code only
		mode.Options, modes = append(mode.Options, "Auto mode"), append(modes, "auto")
	}
	selected := mode.Options[0]
	for i, name := range modes {
		if ui.approvalMode == name {
			selected = mode.Options[i]
		}
	}
	mode.SetSelected(selected)
	mode.onChanged = func(label string) {
		for i, option := range mode.Options {
			if label == option {
				ui.approvalMode = modes[i]
			}
		}
		platform.Devf("ui", "approval mode %s", ui.approvalMode)
		ui.updateApprovalControl()
		ui.save()
	}
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(10)
	entry.SetText(ui.userInstructions)
	entry.SetPlaceHolder("Language, tone, coding conventions, level of detail…")
	entry.OnChanged = func(text string) {
		ui.userInstructions = strings.TrimSpace(text)
		ui.save()
	}
	ui.preferencesEntry = entry
	page := container.NewVBox(
		view.pageTitle("Agent & approvals", "", nil),
		view.intro(registry.Agent),
		view.scope(),
		sectionTitle("Permissions"),
		card(view.mark(registry.KeyApprovalMode, settingRowTitled(titleInfo(ui.tips, "Tool permissions", "Auto runs reads at once and other commands in a sandbox. YOLO runs everything with your rights."), "Asks before commands and edits. YOLO runs them at once.",
			sized(230, mode.row)))),
		sectionTitle("Instructions and memory"),
		card(view.mark("agent.preferences", settingStack("Your preferences",
			"Added to every chat's system prompt, from the next turn.",
			entry)),
			view.mark(registry.KeyMemory, settingRow("Memory",
				"The agent proposes what it learns; you approve each change.",
				newToggle(!ui.memoryOff, func(on bool) { ui.setMemory(on) })))),
		card(view.mark(registry.KeyWrapUp, settingRow("Next-step card",
			"A recap at the end of each turn, with buttons for your reply.",
			newToggle(!ui.wrapUpOff, func(on bool) { ui.setWrapUp(on) })))),
		sectionTitle("Project memory"),
		view.mark("agent.project_memory", card(settingRow("Project memory",
			"Each project's AGENTS.md. Edit it from the project's … menu.", nil))),
	)
	page.Add(sectionTitle("Context"))
	page.Add(card(view.mark(registry.KeyDynamicCompaction, settingRow("Dynamic compaction", "Compacts at natural breaks in the work, not when nearly full.",
		newToggle(!ui.classicCompaction, func(on bool) { ui.setDynamicCompaction(on) })))))
	return page
}

func (ui *desktopUI) setMemory(on bool) {
	ui.memoryOff = !on
	ui.save()
	ui.remote.settingsChanged()
}

func (ui *desktopUI) setWrapUp(on bool) {
	ui.wrapUpOff = !on
	ui.save()
	ui.remote.settingsChanged()
}

func (ui *desktopUI) setDynamicCompaction(on bool) {
	ui.classicCompaction = !on
	platform.Devf("ui", "dynamic compaction %v", on)
	ui.save()
}

// toolSettings is Tools & extensions: what extends the agent, and its
// browser and screen.
func (ui *desktopUI) toolSettings() fyne.CanvasObject {
	view := ui.prefs
	github := "Not connected."
	if state := githubconnector.Default.Status(); state.Connected {
		github = "Connected as @" + state.Login + "."
	}
	return container.NewVBox(
		view.pageTitle("Tools & extensions", "", nil),
		view.intro(registry.Tools),
		sectionTitle("Extensions"),
		card(view.mark("tools.extensions", settingRow("Skills, plugins and MCP servers", "", view.pageButton("Extensions"))),
			view.mark("tools.github", settingRow("GitHub connector", "Lets the agent work on your repositories and issues. "+github, view.pageButton("Connectors")))),
		sectionTitle("Browser"),
		card(view.mark("tools.browser_data", settingRow("Browsing data", "Cookies, logins and history the agents' browser keeps.",
			ui.clearBrowserButton())),
			view.mark("tools.import_cookies", settingRow("Import cookies",
				"Cookies from an export or a browser profile. Review them first.",
				newRow("Import…", nil, styleOutline, ui.importCookies)))),
		sectionTitle("Computer Use"),
		card(view.mark("tools.computer_screen", ui.computerScreenRow())),
	)
}

// advancedSettings is Advanced: the hooks and crash reports.
func (ui *desktopUI) advancedSettings() fyne.CanvasObject {
	view := ui.prefs
	return container.NewVBox(
		view.pageTitle("Advanced", "", nil),
		view.intro(registry.Advanced),
		sectionTitle("Hooks"),
		card(view.mark("advanced.hooks", ui.hooksRow())),
		sectionTitle("Crashes"),
		card(view.mark(registry.KeyCrashReports, settingRow("Offer to report crashes", "After a crash, offers an anonymous report to file on GitHub.",
			newToggle(!ui.crashReportsOff, ui.setCrashReports)))),
	)
}

// setCrashReports turns the offer after a crash on or off.
func (ui *desktopUI) setCrashReports(on bool) {
	ui.crashReportsOff = !on
	platform.Devf("ui", "crash reports %v", on)
	ui.save()
}

// hooksRow edits the user's hooks file in 1loop, or opens it (made with an
// empty list the first time) in the system's editor.
func (ui *desktopUI) hooksRow() fyne.CanvasObject {
	path := agent.HooksPath()
	count := agent.HookCount()
	detail := fmt.Sprintf("%d configured, in %s.", count, path)
	edit := newRow("Edit", nil, styleOutline, ui.editHooks)
	return settingRow("Hook commands", detail, container.NewHBox(edit, newRow("Open file", nil, styleChip, func() {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			_ = os.WriteFile(path, []byte("{\n  \"hooks\": {\n    \"PreToolUse\": [],\n    \"PostToolUse\": [],\n    \"UserPromptSubmit\": [],\n    \"Stop\": []\n  }\n}\n"), 0600)
		}
		if err := fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: path}); err != nil {
			ui.notice("Hooks", "The file could not be opened: "+err.Error()+"\n\n"+path)
		}
	})))
}

// editHooks is the hooks file in an editor in the window: saved only when
// it is valid, with the reason when it is not.
func (ui *desktopUI) editHooks() {
	content, err := agent.ReadHooks()
	if err != nil {
		ui.notice("Hooks", err.Error())
		return
	}
	editor := widget.NewMultiLineEntry()
	editor.TextStyle = fyne.TextStyle{Monospace: true}
	editor.Wrapping = fyne.TextWrapOff
	editor.SetText(content)
	problem := note("")
	scroll := container.NewScroll(editor)
	scroll.SetMinSize(fyne.NewSize(0, 360))
	body := container.NewBorder(note("Example: {\"hooks\": {\"PostToolUse\": [{\"matcher\": \"edit_file|apply_patch\", \"hooks\": [{\"type\": \"command\", \"command\": \"gofmt -w .\"}]}]}}"), problem, nil, nil, scroll)
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	save := newRow("Save", nil, stylePrimary, func() {
		if err := agent.SaveHooks(editor.Text); err != nil {
			problem.SetText(err.Error())
			return
		}
		dialog.dismiss()
		if ui.prefs.on("Advanced") {
			ui.prefs.show("Advanced") // the count follows
		}
	})
	dialog = ui.modal("Hooks", body, 640, nil, cancel, save)
}

func (ui *desktopUI) clearBrowserButton() fyne.CanvasObject {
	var button *tapRow
	button = newRow("Clear…", iconTrash, styleOutline, func() {
		ui.confirm("Clear browsing data?", "Cookies, logins and history of the agents' browser will be erased.", "Clear", func() {
			button.Disable()
			ui.clearBrowserData(func(err error) {
				button.Enable()
				if err != nil {
					ui.notice("Browsing data", "Some data could not be cleared: "+err.Error())
					return
				}
				ui.notice("Browsing data", "The browser's data is cleared.")
			})
		})
	})
	return button
}

// computerScreenRow shows the screen Computer Use shares, with buttons to
// pick another one in the desktop's dialog or to forget the permission.
func (ui *desktopUI) computerScreenRow() fyne.CanvasObject {
	grant := computeruse.LoadComputerGrant()
	detail := "Asks which screen to share. Choose \u201cRemember\u201d to stop asking."
	if grant.Token != "" {
		detail = fmt.Sprintf("Shared without asking: %d \u00d7 %d at %d, %d. Forget stops it.",
			grant.Width, grant.Height, grant.X, grant.Y)
	}
	refresh := func() { // the page, if it is still the one on screen
		if ui.prefs.on("Tools & extensions") {
			ui.prefs.show("Tools & extensions")
		}
	}
	var change *tapRow
	change = newRow("Change screen…", nil, styleOutline, func() {
		change.Disable()
		ui.chooseComputerScreen(func(err error) {
			change.Enable()
			if err != nil {
				ui.notice("Computer Use", "The screen did not change: "+err.Error())
			}
			refresh()
		})
	})
	forget := newRow("Forget", iconTrash, styleOutline, func() {
		if err := ui.forgetComputerScreen(); err != nil {
			ui.notice("Computer Use", "The permission could not be forgotten: "+err.Error())
		}
		refresh()
	})
	if grant.Token == "" {
		forget.Disable()
	}
	return settingRow("Shared screen", detail, container.NewHBox(change, forget))
}

// ---------------------------------------------------------------------------
// Connections.

// connectionRows is the card of the connections, each with its switch.
func (ui *desktopUI) connectionRows() fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, conn := range ui.connectionsList {
		connectionValue := conn
		enabled := newToggle(connectionValue.Enabled, func(on bool) {
			for i := range ui.connectionsList {
				if ui.connectionsList[i].ID == connectionValue.ID {
					ui.connectionsList[i].Enabled = on
				}
			}
			platform.Devf("ui", "connection %q enabled %v", connectionValue.Name, on)
			ui.save()
			ui.refreshModels()
		})
		edit := newRow("Edit", iconChevronRight, styleChip, func() { ui.connectionEditor(connectionValue) })
		edit.iconAfter = true
		detail := agent.ProviderName(connectionValue.Provider)
		if connectionValue.Provider == "claudecode" {
			detail += " · " + claudeAccountText(connectionValue)
		}
		rows = append(rows, settingRow(connectionValue.Name, detail, container.New(layout.NewCustomPaddedHBoxLayout(14), container.NewCenter(enabled), edit)))
	}
	if len(rows) == 0 {
		rows = append(rows, settingRow("No connection yet", "Add one to get started.", nil))
	}
	return card(rows...)
}

// removeConnection forgets a connection (and signs a ChatGPT account out).
func (ui *desktopUI) removeConnection(conn agent.Connection, done func()) {
	ui.confirm("Remove connection?", "Remove “"+conn.Name+"” from 1loop? Chats are kept. ChatGPT sessions end.", "Remove", func() {
		finish := func() {
			kept := []agent.Connection{}
			for _, entry := range ui.connectionsList {
				if entry.ID != conn.ID {
					kept = append(kept, entry)
				}
			}
			ui.connectionsList = kept
			delete(ui.keys, conn.ID)
			ui.save()
			ui.refreshModels()
			done()
		}
		if conn.Provider == "claudecode" && conn.ClaudeHome() != "" {
			go func() {
				ctx, cancel := context.WithTimeout(ui.ctx, 25*time.Second)
				defer cancel()
				err := agent.SignOutClaude(ctx, conn)
				fyne.Do(func() {
					finish()
					if err != nil {
						ui.notice("Claude Code account not signed out", err.Error())
					}
				})
			}()
			return
		}
		if conn.Provider != "openai" {
			finish()
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 25*time.Second)
			defer cancel()
			warning, err := ui.backend.DisconnectOpenAI(ctx, conn.AuthProfile)
			fyne.Do(func() {
				if err != nil {
					ui.notice("Unable to disconnect", err.Error())
					return
				}
				finish()
				if warning != "" {
					ui.notice("Account disconnected", warning)
				}
			})
		}()
	})
}

// connectionEditor opens a connection's page: a draft, saved as a whole.
func (ui *desktopUI) connectionEditor(original agent.Connection) {
	settings := ui.prefs
	draft := cloneConnection(original)
	existing := false
	for _, conn := range ui.connectionsList {
		existing = existing || conn.ID == original.ID
	}
	name := widget.NewEntry()
	name.SetText(draft.Name)
	base := widget.NewEntry()
	base.SetText(draft.BaseURL)
	key := widget.NewPasswordEntry()
	key.SetText(ui.keys[draft.ID])
	key.SetPlaceHolder("Kept in memory until 1loop closes")
	enabled := newToggle(draft.Enabled, func(on bool) { draft.Enabled = on })
	typePicker := newPicker(ui.window, "Connection type", styleField, nil)
	typePicker.Options = []string{"Local vLLM", "OpenRouter", "ChatGPT account", "OpenAI-compatible API", "Claude Code", "Antigravity CLI", "ChatGPT (Web)"}
	ids := map[string]string{
		"Local vLLM":            "vllm",
		"OpenRouter":            "openrouter",
		"ChatGPT account":       "openai",
		"OpenAI-compatible API": "compatible",
		"Claude Code":           "claudecode",
		"Antigravity CLI":       "antigravity",
		"ChatGPT (Web)":         "chatgptweb",
	}
	status := plainFlow("", 13, colMuted)
	statusWidth := &widthLayout{}
	say := func(text string) {
		status.spans, status.cut = []span{{text: text}}, nil
		// As wide as the message, on one line while it fits beside the
		// buttons; longer ones wrap.
		natural, _ := status.extent()
		statusWidth.width = fyne.Min(natural+2, 440)
		status.flow(statusWidth.width)
		status.Refresh()
		// The bar under the page takes the text's new height: two lines
		// would otherwise run out of the window.
		settings.view.Refresh()
	}
	var loadModels func() // set once its button is built

	// Access: an address and a key, a ChatGPT sign-in, or nothing (Claude Code).
	apiFields := card(
		settingRow("API base URL", "", sized(340, base)),
		settingRow("API key", "", sized(340, key)))
	loginLink := widget.NewHyperlink("Open sign-in in your browser", nil)
	loginLink.Hide()
	account := plainFlow("Not signed in.", 12.5, colMuted)
	showAccount := func(email string) {
		if email == "" {
			email = "Not signed in."
		}
		account.spans, account.cut = []span{{text: email}}, nil
		account.flow(account.Size().Width)
		account.Refresh()
	}
	if draft.Provider == "openai" {
		if auth, err := ui.backend.GetOpenAI(draft.AuthProfile); err == nil {
			auth.Mu.Lock()
			showAccount(auth.Account.Email)
			auth.Mu.Unlock()
		}
	}
	var cancelLogin context.CancelFunc
	login := newRow("Continue with ChatGPT", nil, styleOutline, nil)
	login.onTap = func() {
		if cancelLogin != nil {
			cancelLogin()
		}
		ctx, cancel := context.WithTimeout(ui.ctx, 5*time.Minute)
		cancelLogin = cancel
		profile := draft.AuthProfile
		login.Disable()
		say("Preparing sign-in…")
		go func() {
			defer cancel()
			raw, done, err := ui.backend.BeginOpenAI(ctx, profile)
			fyne.Do(func() {
				if err != nil {
					login.Enable()
					say(err.Error())
					return
				}
				parsed, _ := url.Parse(raw)
				loginLink.SetURL(parsed)
				loginLink.Show()
				say("Open the link, authorize 1loop, then return here.")
			})
			if err != nil {
				return
			}
			err = <-done
			fyne.Do(func() {
				login.Enable()
				loginLink.Hide()
				if err != nil {
					say(err.Error())
					return
				}
				say("Account connected.")
				email := ""
				if auth, errorValue := ui.backend.GetOpenAI(profile); errorValue == nil {
					auth.Mu.Lock()
					email = auth.Account.Email
					auth.Mu.Unlock()
					showAccount(email)
				}
				ui.chatGPTConnected(email, loadModels)
			})
		}()
	}
	oauthFields := card(settingStack("ChatGPT account", "Uses your ChatGPT subscription. Sign in with your browser.",
		container.NewVBox(account, container.NewHBox(login), loginLink)))
	claudeState := plainFlow("", 12.5, colMuted)
	claudeAccount := plainFlow("", 12.5, colMuted)
	installClaude := newRow("Install Claude Code", nil, styleOutline, nil)
	showClaudeAccount := func() { setFlow(claudeAccount, claudeAccountText(draft)) }
	signInClaude := newRow("Sign in", nil, styleOutline, func() { ui.signInClaude(draft, showClaudeAccount) })
	// The login: the system Claude Code one, or this connection's own, so
	// that two connections can be two accounts (claude_accounts.go).
	claudeLogins := map[string]string{"System Claude Code login": "", "Own login for this connection": agent.ClaudeOwnLogin}
	claudeLogin := newPicker(ui.window, "Login", styleField, func(label string) {
		draft.ClaudeLogin = claudeLogins[label]
		showClaudeAccount()
	})
	claudeLogin.Options = []string{"System Claude Code login", "Own login for this connection"}
	claudeLoginLabel := func() string {
		if draft.ClaudeLogin == agent.ClaudeOwnLogin {
			return "Own login for this connection"
		}
		return "System Claude Code login"
	}
	claudeLogin.SetSelected(claudeLoginLabel())
	showClaude := func() {
		path, err := agent.ClaudeExecutable()
		problem := agent.ClaudeInstallProblem()
		showClaudeAccount()
		switch {
		case err == nil:
			setFlow(claudeState, "Installed: "+path)
			installClaude.Hide()
			signInClaude.Show()
		case problem != "":
			signInClaude.Hide()
			setFlow(claudeState, "Not installed on this computer. "+problem)
			installClaude.Hide()
		default:
			signInClaude.Hide()
			setFlow(claudeState, "Not installed on this computer.")
			installClaude.Show()
		}
	}
	installClaude.onTap = func() { ui.installClaudeCode(showClaude) }
	claudeFields := card(settingStackTitled(titleInfo(ui.tips, "Claude Code on this computer", "Runs in Claude Code's own harness on your subscription. A run billed outside it is stopped."), "Runs the Claude Code installed here, with its own sign-in.",
		container.NewVBox(claudeState, container.NewHBox(installClaude))),
		settingRow("Login", "The system login, or this connection's own account.", sized(340, claudeLogin.row)),
		settingStack("Account", "", container.NewVBox(claudeAccount, container.NewHBox(signInClaude))))
	agyState := plainFlow("", 12.5, colMuted)
	showAgy := func() {
		if path, err := agent.AgyExecutable(); err == nil {
			setFlow(agyState, "Installed: "+path+". Sign in by running agy once in a terminal.")
		} else {
			setFlow(agyState, "Not installed. Install the Antigravity CLI, then run agy once.")
		}
	}
	agyFields := card(settingStack("Antigravity CLI on this computer", "Runs the agy command here, with its own Google sign-in.",
		container.NewVBox(agyState, plainFlow(agent.AgyTerms, 12, colMuted))))
	webFields, releaseWebSetup := ui.chatGPTWebSettings(&draft, say)
	access := container.NewStack(apiFields, oauthFields, claudeFields, agyFields, webFields)

	routing := jsonEntry(draft.Routing)
	parameters := jsonEntry(draft.Parameters)
	routingRow := settingStack("OpenRouter routing (JSON)", "Provider order and fallbacks, for every model here.", routing)
	advanced := container.NewVBox()
	showAdvanced := func() {
		rows := []fyne.CanvasObject{}
		if draft.Provider == "openrouter" {
			rows = append(rows, routingRow)
		}
		rows = append(rows, settingStack("Request parameters (JSON)",
			"Such as temperature or seed. Model and tools stay 1loop's.", parameters))
		advanced.Objects = []fyne.CanvasObject{card(rows...)}
		advanced.Refresh()
	}

	// Models: which ones of the catalog appear in the composer.
	var models []agent.ModelChoice
	defaultRows := container.NewVBox()
	showDefaults := func() {
		defaultRows.Objects = []fyne.CanvasObject{ui.defaultModelFields(&draft, models)}
		defaultRows.Refresh()
	}
	search := widget.NewEntry()
	search.SetPlaceHolder("Search the models…")
	modelRows := container.New(layout.NewCustomPaddedVBoxLayout(0))
	allModels := newToggle(draft.AllModels, nil)
	var listModels func()
	listModels = func() {
		const shown = 150 // more rows than anyone reads: the search narrows them
		modelRows.Objects = nil
		query := strings.ToLower(strings.TrimSpace(search.Text))
		count, matching := 0, 0
		for _, model := range models {
			if !strings.Contains(strings.ToLower(model.Name+" "+model.ID), query) {
				continue
			}
			if matching++; count >= shown {
				continue
			}
			count++
			model := model
			include := newToggle(draft.Includes(model.ID), func(on bool) {
				if draft.AllModels { // choosing one model ends "all of them"
					draft.Models = nil
					for _, choice := range models {
						draft.Models = append(draft.Models, choice.ID)
					}
					draft.AllModels = false
					allModels.set(false)
				}
				chosen := []string{}
				for _, id := range draft.Models {
					if id != model.ID {
						chosen = append(chosen, id)
					}
				}
				if on {
					chosen = append(chosen, model.ID)
				}
				draft.Models = chosen
			})
			configure := newRow("Configure", iconChevronRight, styleChip, func() { ui.modelPreferencesEditor(&draft, model, key.Text) })
			configure.iconAfter = true
			if count > 1 {
				rule := canvas.NewRectangle(colBorderSoft)
				rule.SetMinSize(fyne.NewSize(0, 1))
				modelRows.Add(padded(0, 0, 16, 16, rule))
			}
			modelRows.Add(padded(8, 8, 16, 12, container.NewBorder(nil, nil, container.NewCenter(include), configure,
				padded(0, 0, 12, 8, newFitLabel(model.Name, 13.5, colText)))))
		}
		switch {
		case len(models) == 0:
			modelRows.Add(settingRow("No catalog loaded", "Load the models to choose which ones to show.", nil))
		case matching == 0:
			modelRows.Add(settingRow("No model matches", "", nil))
		case matching > count:
			modelRows.Add(settingRow(fmt.Sprintf("%d more", matching-count), "Search to narrow the list.", nil))
		}
		modelRows.Refresh()
	}
	search.OnChanged = func(string) { listModels() }
	allModels.onChange = func(on bool) {
		draft.AllModels = on
		listModels()
	}
	var loadGeneration int
	load := newRow("Load models", nil, styleOutline, nil)
	load.onTap = func() {
		loadGeneration++
		generation := loadGeneration
		snapshot := draft
		snapshot.BaseURL = strings.TrimSpace(base.Text)
		apiKey := key.Text
		say("Loading the model catalog…")
		go func() {
			choices, err := ui.backend.ConnectionModels(ui.ctx, snapshot, apiKey)
			fyne.Do(func() {
				if generation != loadGeneration {
					return
				}
				if err != nil {
					say(err.Error())
					return
				}
				models = nil
				for _, choice := range choices {
					if !choice.IsDefaultPreset() {
						models = append(models, choice)
					}
				}
				showDefaults()
				listModels()
				say(fmt.Sprintf("%d models loaded.", len(models)))
			})
		}()
	}
	loadModels = load.onTap

	typePicker.onChanged = func(label string) {
		previous := draft.Provider
		draft.Provider = ids[label]
		if draft.Provider != "chatgptweb" {
			releaseWebSetup()
		}
		apiFields.Hide()
		oauthFields.Hide()
		claudeFields.Hide()
		agyFields.Hide()
		webFields.Hide()
		switch draft.Provider {
		case "openai":
			oauthFields.Show()
		case "claudecode":
			if !existing && previous != draft.Provider {
				draft.ClaudeLogin = agent.NewClaudeLogin(ui.connectionsList, draft.ID)
				claudeLogin.SetSelected(claudeLoginLabel())
			}
			claudeFields.Show()
			showClaude()
			// Choosing Claude Code without it installed offers to install it.
			if _, err := agent.ClaudeExecutable(); err != nil && previous != draft.Provider && agent.ClaudeInstallProblem() == "" {
				ui.installClaudeCode(showClaude)
			}
		case "antigravity":
			agyFields.Show()
			showAgy()
		case "chatgptweb":
			webFields.Show()
		default:
			apiFields.Show()
		}
		if draft.Provider == "openrouter" {
			base.SetText("https://openrouter.ai/api/v1")
			base.Disable()
		} else {
			base.Enable()
		}
		if draft.Provider == "vllm" && previous != draft.Provider {
			base.SetText(agent.DefaultVLLMURL)
		}
		if previous != draft.Provider { // another service: another catalog
			loadGeneration++
			models = nil
			draft.Models, draft.AllModels, draft.ModelOptions = nil, true, nil
			draft.DefaultModels = nil
			showDefaults()
			allModels.set(true)
			listModels()
		}
		showAdvanced()
		access.Refresh()
	}
	typePicker.SetSelected(agent.ProviderName(draft.Provider))
	typePicker.onChanged(agent.ProviderName(draft.Provider))
	listModels()
	showDefaults()

	title := draft.Name
	if !existing {
		title = "New connection"
	}
	page := container.NewVBox(
		settings.pageTitle(title, "Models & accounts", nil),
		card(
			settingRow("Name", "", sized(340, name)),
			settingRow("Type", "", sized(340, typePicker.row)),
			settingRow("Enabled", "Its models are offered in the composer.", enabled)),
		sectionTitle("Access"),
		access,
		sectionTitle("Models"),
		card(
			settingRow("Show every model", "Turn off to choose them one by one below.", allModels),
			settingRow("Catalog", "Ask the service which models it offers.", load),
			padded(10, 10, 16, 16, search),
			modelRows),
		sectionTitle("Default preset"),
		defaultRows,
		sectionTitle("Advanced"),
		advanced,
	)
	if existing {
		remove := newRow("Remove", iconTrash, styleOutline, func() {
			ui.removeConnection(original, func() { settings.done() })
		})
		remove.danger = true
		page.Add(sectionTitle("Remove"))
		page.Add(card(settingRow("Remove this connection", "Chats are kept; any account here is signed out.", remove)))
	}
	page.Add(spacer(0, 16))

	saveDraft := func() bool {
		if strings.TrimSpace(name.Text) == "" {
			say("Enter a connection name.")
			return false
		}
		params, err := requestParameters(parameters.Text)
		if err != nil {
			say(err.Error())
			return false
		}
		var route map[string]any
		if draft.Provider == "openrouter" {
			if err = json.Unmarshal([]byte(routing.Text), &route); err != nil || route == nil {
				say("Routing must be a JSON object.")
				return false
			}
		}
		draft.Name = strings.TrimSpace(name.Text)
		draft.BaseURL = strings.TrimSpace(base.Text)
		if draft.Provider != "claudecode" {
			draft.ClaudeLogin = ""
		}
		draft.Routing = route
		draft.Parameters = params
		if draft.Provider != "openai" && !agent.CLIHarness(draft.Provider) && draft.Provider != "chatgptweb" {
			cfg := draft.Config("")
			cfg.APIKey = key.Text
			if _, _, err = agent.Endpoint(cfg); err != nil {
				say(err.Error())
				return false
			}
		}
		found := false
		for i, connectionValue := range ui.connectionsList {
			if connectionValue.ID == draft.ID {
				ui.connectionsList[i] = cloneConnection(draft)
				found = true
				break
			}
		}
		if !found {
			ui.connectionsList = append(ui.connectionsList, cloneConnection(draft))
		}
		ui.keys[draft.ID] = key.Text
		platform.Devf("ui", "connection %q saved", draft.Name)
		ui.save()
		ui.refreshModels()
		releaseWebSetup()
		return true
	}
	save := newRow("Save connection", nil, stylePrimary, func() {
		if saveDraft() {
			settings.done()
		}
	})
	initial := cloneConnection(draft)
	fields := func() [5]string { return [5]string{name.Text, base.Text, key.Text, routing.Text, parameters.Text} }
	initialFields := fields()
	beforeLeave := func(next func()) {
		if reflect.DeepEqual(draft, initial) && fields() == initialFields {
			next()
			return
		}
		var warning *sheet
		stay := newRow("Go back", nil, styleOutline, func() { warning.dismiss() })
		discard := newRow("Discard", nil, styleOutline, func() { warning.Hide(); next() })
		discard.danger = true
		keep := newRow("Save", nil, stylePrimary, func() {
			warning.Hide()
			if saveDraft() {
				next()
			}
		})
		warning = ui.modal("Unsaved changes", note("Save your choices to keep them. Do you want to discard your changes?"), 520, nil, stay, discard, keep)
	}
	cancel := newRow("Cancel", nil, styleOutline, settings.back)
	settings.push(settingsFrame{content: page, actions: []fyne.CanvasObject{
		container.NewCenter(container.New(statusWidth, status)),
		cancel,
		save,
	}, beforeLeave: beforeLeave, leave: func() {
		releaseWebSetup()
		loadGeneration++
		if cancelLogin != nil {
			cancelLogin()
		}
	}})
}

// modelPreferencesEditor opens one model's page, over its connection's.
func (ui *desktopUI) modelPreferencesEditor(conn *agent.Connection, model agent.ModelChoice, key string) {
	settings := ui.prefs
	preferences := conn.ModelOptions[model.ID]
	routing := jsonEntry(preferences.Provider)
	parameters := jsonEntry(preferences.Parameters)
	compactAt := widget.NewEntry()
	compactConfig := conn.Config("")
	compactConfig.Model = model.ID
	if target, _ := agent.CompactDefault(compactConfig); target > 0 {
		compactAt.SetPlaceHolder("Default · " + agent.Thousands(target))
	} else {
		compactAt.SetPlaceHolder("Default · the model's window")
	}
	if preferences.CompactAt > 0 {
		compactAt.SetText(strconv.Itoa(preferences.CompactAt))
	}
	if conn.Provider == "chatgptweb" {
		compactAt.Disable()
		compactAt.SetPlaceHolder("Managed by ChatGPT Web")
	}
	status := plainFlow("", 13, colMuted)
	say := func(text string) {
		status.spans, status.cut = []span{{text: text}}, nil
		status.flow(status.Size().Width)
		status.Refresh()
	}
	reasoning := "Not advertised by this model."
	if len(model.Efforts) > 0 {
		labels := make([]string, len(model.Efforts))
		for i, effort := range model.Efforts {
			labels[i] = agent.EffortLabel(effort)
		}
		reasoning = strings.Join(labels, ", ") + " (" + model.Source + ")"
	}
	page := container.NewVBox(
		settings.pageTitle(model.Name, conn.Name, nil),
		card(settingRow("Identifier", model.ID, nil), settingRow("Reasoning effort", reasoning, nil)),
	)
	if conn.Provider == "openrouter" {
		upstream := newPicker(ui.window, "Automatic provider routing", styleField, nil)
		upstream.Options = []string{"Automatic"}
		upstream.SetSelected("Automatic")
		if order := agent.StringsFrom(preferences.Provider["order"]); len(order) > 0 {
			upstream.Options = append(upstream.Options, order[0])
			upstream.SetSelected(order[0])
		}
		value, ok := preferences.Provider["allow_fallbacks"].(bool)
		var fallbacks *toggle
		// The JSON stays the source of truth for every routing option.
		update := func() {
			var route map[string]any
			if json.Unmarshal([]byte(routing.Text), &route) != nil || route == nil {
				route = map[string]any{}
			}
			route["order"] = []string{}
			if upstream.Selected != "Automatic" {
				route["order"] = []string{upstream.Selected}
			}
			route["allow_fallbacks"] = fallbacks.on
			routing.SetText(agent.JSONText(route))
		}
		fallbacks = newToggle(!ok || value, func(bool) { update() })
		upstream.onChanged = func(string) { update() }
		fetch := newRow("Fetch providers", nil, styleOutline, func() {
			snapshot := *conn
			go func() {
				providers, err := ui.backend.UpstreamProviders(ui.ctx, snapshot, key, model.ID)
				fyne.Do(func() {
					if err != nil {
						say(err.Error())
						return
					}
					upstream.Options = append([]string{"Automatic"}, providers...)
					say(fmt.Sprintf("%d providers available.", len(providers)))
				})
			}()
		})
		page.Add(sectionTitle("Routing"))
		page.Add(card(
			settingRow("Upstream provider", "", container.New(layout.NewCustomPaddedHBoxLayout(8), fetch, sized(220, upstream.row))),
			settingRow("Allow fallback providers", "", fallbacks),
			settingStack("Routing overrides (JSON)", "", routing)))
	}
	page.Add(sectionTitle("Context"))
	page.Add(card(settingStack("Compact at (tokens)", agent.CompactHelp(compactConfig), compactAt)))
	page.Add(sectionTitle("Parameters"))
	page.Add(card(settingStack("Request parameter overrides (JSON)",
		"Replace this connection's defaults. Save to apply them.", parameters)))
	page.Add(spacer(0, 16))
	save := newRow("Save model settings", nil, stylePrimary, func() {
		compact := 0
		if value := strings.TrimSpace(compactAt.Text); value != "" {
			var err error
			compact, err = strconv.Atoi(value)
			if err != nil || compact < 1 || compact > 1000000 || conn.Provider == "claudecode" && compact < 100000 {
				say("Enter 1 to 1,000,000 tokens (100,000 for Claude Code), or empty.")
				return
			}
		}
		params, err := requestParameters(parameters.Text)
		if err != nil {
			say(err.Error())
			return
		}
		var route map[string]any
		if conn.Provider == "openrouter" {
			if err = json.Unmarshal([]byte(routing.Text), &route); err != nil || route == nil {
				say("Routing must be a JSON object.")
				return
			}
		}
		if conn.ModelOptions == nil {
			conn.ModelOptions = map[string]agent.ModelPreferences{}
		}
		conn.ModelOptions[model.ID] = agent.ModelPreferences{Provider: route, Parameters: params, CompactAt: compact}
		settings.back()
	})
	settings.push(settingsFrame{content: page, actions: []fyne.CanvasObject{
		container.NewCenter(sized(360, status)),
		newRow("Cancel", nil, styleOutline, settings.back),
		save,
	}})
}

func requestParameters(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("Invalid JSON object: %w", err)
	}
	if params == nil {
		return nil, fmt.Errorf("Expected a JSON object")
	}
	for _, key := range []string{
		"model",
		"messages",
		"input",
		"instructions",
		"tools",
		"tool_choice",
		"stream",
		"store",
		"reasoning",
		"reasoning_effort",
		"provider",
	} {
		if _, ok := params[key]; ok {
			return nil, fmt.Errorf("%s is managed by 1loop; use its dedicated setting", key)
		}
	}
	return params, nil
}

// chatGPTConnected says the sign-in worked, and what is left to do: the
// connection is not saved yet, and its models are not loaded.
func (ui *desktopUI) chatGPTConnected(email string, loadModels func()) {
	who := "Your ChatGPT account is connected."
	if email != "" {
		who = "Your ChatGPT account " + email + " is connected."
	}
	next := textLine("What's next", 13.5, colText)
	next.TextStyle = fyne.TextStyle{Bold: true}
	step := func(number, text string) fyne.CanvasObject {
		digit := textLine(number, 13, colAmber)
		digit.TextStyle = fyne.TextStyle{Bold: true}
		return padded(6, 0, 0, 0, container.NewBorder(nil, nil, container.NewVBox(sized(18, digit)), nil, plainFlow(text, 13, colMuted)))
	}
	body := container.NewVBox(
		plainFlow(who, 13.5, colText),
		padded(16, 2, 0, 0, next),
		step("1", "Load models: 1loop asks this account which models it can use."),
		step("2", "Choose the models to show and, if you like, one for each level."),
		step("3", "Save at the bottom. Its models then show in the model menu."),
	)
	var dialog *sheet
	later := newRow("Later", nil, styleOutline, func() { dialog.dismiss() })
	load := newRow("Load models", nil, stylePrimary, func() {
		dialog.dismiss()
		if loadModels != nil {
			loadModels()
		}
	})
	dialog = ui.modal("ChatGPT account connected", body, 480, nil, later, load)
}
