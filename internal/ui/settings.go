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
	"fmt"
	"image/color"
	"net/url"
	"reflect"
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
	"1loop/internal/browser"
	"1loop/internal/computeruse"
	"1loop/internal/core"
	"1loop/internal/platform"
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

var settingsSections = []struct {
	name string
	icon fyne.Resource
}{
	{"General", iconSliders},
	{"Connections", iconPlug},
	{"Instructions", iconDocument},
	{"Phone remote", iconPhone},
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
	section string
	rows    map[string]*tapRow
	side    fyne.CanvasObject // what the sidebar shows
	view    *fyne.Container   // what the main area shows
	column  *fyne.Container
	scroll  *kineticScroll
	bar     *fyne.Container
	barRow  *fyne.Container
	frames  []settingsFrame // the section, then the pages opened from it
	panelOn bool            // the tool panel's button was showing
}

func (ui *desktopUI) buildSettings() {
	settings := &settingsView{ui: ui, rows: map[string]*tapRow{}, section: "General"}
	back := newRow("Back to 1loop", iconChevronLeft, styleNav, settings.close)
	nav := container.New(layout.NewCustomPaddedVBoxLayout(1))
	for _, section := range settingsSections {
		name := section.name
		row := newRow(name, section.icon, styleNav, func() { settings.show(name) })
		settings.rows[name] = row
		nav.Add(row)
	}
	settings.side = container.NewVBox(back, padded(14, 6, 12, 0, textLine("Settings", 13, colFaint)), nav)
	settings.side.Hide()

	settings.column = container.NewStack()
	settings.scroll = newKineticVScroll(container.New(columnLayout{width: 760, inset: 28}, settings.column))
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	settings.barRow = container.New(layout.NewCustomPaddedHBoxLayout(8))
	settings.bar = container.NewVBox(rule, container.New(columnLayout{width: 760, inset: 12}, container.NewHBox(layout.NewSpacer(), settings.barRow)))
	settings.bar.Hide()
	settings.view = container.NewBorder(nil, settings.bar, nil, nil, container.NewThemeOverride(settings.scroll, flatScrollTheme{loopTheme{theme.DefaultTheme()}}))
	settings.view.Hide()
	ui.prefs = settings
}

// settings opens the page (the sidebar's Settings row).
func (ui *desktopUI) settings() { ui.prefs.openOn("") }

func (settings *settingsView) openOn(section string) {
	ui := settings.ui
	if section == "" {
		section = settings.section
	}
	if !settings.open {
		platform.Devf("ui", "settings opened")
		settings.open = true
		ui.clearSelection()
		settings.panelOn = ui.panelToggle.Visible()
		ui.panelToggle.Hide()
		ui.sideApp.Hide()
		settings.side.Show()
		ui.split.Hide()
		settings.view.Show()
		ui.header.SetText("Settings")
	}
	settings.show(section)
}

// close goes back to the conversation.
func (settings *settingsView) close() {
	if !settings.open {
		return
	}
	settings.confirmLeave(0, settings.closeNow)
}

func (settings *settingsView) closeNow() {
	ui := settings.ui
	for len(settings.frames) > 0 {
		settings.drop()
	}
	settings.open = false
	settings.side.Hide()
	ui.sideApp.Show()
	settings.view.Hide()
	ui.split.Show()
	if settings.panelOn {
		ui.panelToggle.Show()
	}
	ui.updateHeader()
	ui.relayoutColumn()
	ui.window.Canvas().Focus(ui.prompt)
}

// show opens a section, leaving whatever was open.
func (settings *settingsView) show(section string) {
	settings.confirmLeave(0, func() { settings.showNow(section) })
}

func (settings *settingsView) showNow(section string) {
	for len(settings.frames) > 0 {
		settings.drop()
	}
	settings.section = section
	for name, row := range settings.rows {
		row.SetSelected(name == section)
	}
	var page fyne.CanvasObject
	switch section {
	case "Connections":
		page = settings.ui.connectionSettings()
	case "Instructions":
		page = settings.ui.instructionSettings()
	case "Phone remote":
		page = settings.ui.remoteSettings()
	default:
		page = settings.ui.generalSettings()
	}
	settings.frames = []settingsFrame{{content: page}}
	settings.present()
}

// push opens a page over the current one.
func (settings *settingsView) push(frame settingsFrame) {
	settings.frames = append(settings.frames, frame)
	settings.present()
}

// drop leaves the page on top.
func (settings *settingsView) drop() {
	if n := len(settings.frames); n > 0 {
		if leave := settings.frames[n-1].leave; leave != nil {
			leave()
		}
		settings.frames = settings.frames[:n-1]
	}
}

// back returns to the page below, or to the conversation from a section.
func (settings *settingsView) back() {
	if len(settings.frames) <= 1 {
		settings.close()
		return
	}
	settings.confirmLeave(len(settings.frames)-1, func() {
		settings.drop()
		settings.present()
	})
}

func (settings *settingsView) confirmLeave(keep int, next func()) {
	for i := len(settings.frames) - 1; i >= keep; i-- {
		if ask := settings.frames[i].beforeLeave; ask != nil {
			ask(next)
			return
		}
	}
	next()
}

// done leaves the page on top after it saved: the one below is built again
// from what is now stored.
func (settings *settingsView) done() {
	if n := len(settings.frames); n > 1 {
		settings.frames[n-1].leave = nil
		settings.frames[n-1].beforeLeave = nil
	}
	if len(settings.frames) == 2 {
		settings.show(settings.section)
		return
	}
	settings.back()
}

func (settings *settingsView) present() {
	frame := settings.frames[len(settings.frames)-1]
	settings.column.Objects = []fyne.CanvasObject{frame.content}
	settings.column.Refresh()
	settings.barRow.Objects = frame.actions
	if len(frame.actions) > 0 {
		settings.bar.Show()
	} else {
		settings.bar.Hide()
	}
	settings.view.Refresh()
	settings.scroll.ScrollToTop()
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
	text := container.New(layout.NewCustomPaddedVBoxLayout(3), plainFlow(title, 14, colText))
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
	text := container.New(layout.NewCustomPaddedVBoxLayout(3), plainFlow(title, 14, colText))
	if detail != "" {
		text.Add(plainFlow(detail, 12.5, colMuted))
	}
	return padded(13, 14, 16, 16, container.New(layout.NewCustomPaddedVBoxLayout(9), text, below))
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
	switchControl.anim = fyne.NewAnimation(130*time.Millisecond, func(p float32) {
		switchControl.at = from + (to-from)*p
		switchControl.Refresh()
	})
	switchControl.anim.Curve = fyne.AnimationEaseOut
	switchControl.anim.Start()
}

func (switchControl *toggle) Tapped(*fyne.PointEvent) {
	switchControl.set(!switchControl.on)
	if switchControl.onChange != nil {
		switchControl.onChange(switchControl.on)
	}
}

func (switchControl *toggle) CreateRenderer() fyne.WidgetRenderer {
	renderer := &toggleRenderer{toggle: switchControl, track: canvas.NewRectangle(colBorder), knob: canvas.NewCircle(colMuted)}
	renderer.track.CornerRadius = 10
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
	settings := ui.prefs
	change := newRow("Change…", nil, styleOutline, func() {
		ui.pick(pickRequest{title: "Choose the workspace", start: ui.cfg.Workspace, folder: true}, func(paths []string) {
			root, err := agent.ResolveWorkspace(paths[0])
			if err != nil {
				ui.notice("Workspace", err.Error())
				return
			}
			if root != ui.cfg.Workspace {
				ui.selectProject(root) // shows the project's start screen: come back here
				settings.openOn("General")
			}
		})
	})
	mode := newPicker(ui.window, "Tool permissions", styleField, nil)
	mode.Options = []string{"Approval required", "YOLO mode", "Auto mode"}
	modes := []string{"approval", "yolo", "auto"}
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
	return container.NewVBox(
		settings.pageTitle("General", "", nil),
		sectionTitle("Workspace"),
		card(settingRow("Project folder", ui.cfg.Workspace, change)),
		sectionTitle("Remote server"),
		card(settingRow("HTTPS server", "Work on a headless 1loop server with its own models, files, browser and terminals. Approve this device in the server console.", newRow("Connect…", nil, styleOutline, ui.connectServer))),
		sectionTitle("Permissions"),
		card(settingRow("Tool permissions",
			"Approval asks before each shell command or file edit. YOLO runs them at once, with your user's rights. Auto runs what is safe and asks for the rest: a reviewer (the chat's own model, with little reasoning) judges each action against your request; with Claude Code, its own Auto mode does it.",
			sized(230, mode.row))),
		sectionTitle("Browser"),
		card(settingRow("Browsing data",
			"The agents' browser keeps its cookies and logins, cache, site data and history from one session to the next, in "+browser.ProfileDir()+". Clear erases all of it.",
			ui.clearBrowserButton())),
		sectionTitle("Computer Use"),
		card(ui.computerScreenRow()),
		sectionTitle("Context"),
		card(settingRow("Dynamic compaction",
			"On: 1loop compacts at the natural breaks of the work (when a prompt is done, between two tasks of the list, at a stopping point the model chooses), lightens a context whose provider cache has expired, and shortens very large tool outputs as they arrive. Off: the classic way, one summary when the context is nearly full, wherever the work is. Claude Code manages its own context either way.",
			newToggle(!ui.classicCompaction, func(on bool) {
				ui.classicCompaction = !on
				platform.Devf("ui", "dynamic compaction %v", on)
				ui.save()
			}))),
	)
}

func (ui *desktopUI) clearBrowserButton() fyne.CanvasObject {
	var button *tapRow
	button = newRow("Clear…", iconTrash, styleOutline, func() {
		ui.confirm("Clear browsing data?", "Cookies and logins, cache, site data and history of the agents' browser will be erased. Sites will ask to log in again.", "Clear", func() {
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
	detail := "The first use asks which screen to share. Select \u201cRemember this permission\u201d or \u201cRemember this choice\u201d in that dialog and 1loop will not ask again, even after a restart."
	if grant.Token != "" {
		detail = fmt.Sprintf("Shared without asking, even after a restart: the screen of %d \u00d7 %d at %d, %d. Change screen opens the desktop's dialog to pick another; Forget stops sharing and asks next time.",
			grant.Width, grant.Height, grant.X, grant.Y)
	}
	refresh := func() { // the page, if it is still the one on screen
		if ui.prefs.open && ui.prefs.section == "General" {
			ui.prefs.show("General")
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
// Instructions.

func (ui *desktopUI) instructionSettings() fyne.CanvasObject {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(14)
	entry.SetText(ui.userInstructions)
	entry.SetPlaceHolder("Describe your preferences: language, tone, coding conventions, level of detail…")
	entry.OnChanged = func(text string) {
		ui.userInstructions = strings.TrimSpace(text)
		ui.save()
	}
	return container.NewVBox(
		ui.prefs.pageTitle("Instructions", "", nil),
		card(settingStack("Your instructions",
			"Added to 1loop's own system prompt, whose workspace and tool rules stay in place. Saved as you type; they apply from the next request, in every chat.",
			entry)),
	)
}

// ---------------------------------------------------------------------------
// Connections.

func (ui *desktopUI) connectionSettings() fyne.CanvasObject {
	settings := ui.prefs
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
		rows = append(rows, settingRow(connectionValue.Name, agent.ProviderName(connectionValue.Provider), container.New(layout.NewCustomPaddedHBoxLayout(14), container.NewCenter(enabled), edit)))
	}
	add := newRow("Add connection", iconPlus, stylePrimary, func() {
		id := core.Identifier()
		ui.connectionEditor(agent.Connection{ID: id, AuthProfile: id, Name: "New connection", Provider: "compatible", Enabled: true, AllModels: true})
	})
	page := container.NewVBox(settings.pageTitle("Connections", "", container.NewCenter(add)),
		padded(0, 10, 2, 0, plainFlow("Connect several services and accounts at once. The models of every enabled connection appear together in the composer.", 13, colMuted)))
	if len(rows) == 0 {
		rows = append(rows, settingRow("No connection yet", "Add one to get started.", nil))
	}
	page.Add(card(rows...))
	return page
}

// removeConnection forgets a connection (and signs a ChatGPT account out).
func (ui *desktopUI) removeConnection(conn agent.Connection, done func()) {
	ui.confirm("Remove connection?", "Remove “"+conn.Name+"” from 1loop? Existing chats are kept. ChatGPT sessions will be disconnected.", "Remove", func() {
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
	typePicker.Options = []string{"Local vLLM", "OpenRouter", "ChatGPT account", "OpenAI-compatible API", "Claude Code"}
	ids := map[string]string{
		"Local vLLM":            "vllm",
		"OpenRouter":            "openrouter",
		"ChatGPT account":       "openai",
		"OpenAI-compatible API": "compatible",
		"Claude Code":           "claudecode",
	}
	status := plainFlow("", 13, colMuted)
	say := func(text string) {
		status.spans, status.cut = []span{{text: text}}, nil
		status.flow(status.Size().Width)
		status.Refresh()
	}

	// Access: an address and a key, a ChatGPT sign-in, or nothing (Claude Code).
	apiFields := card(
		settingRow("API base URL", "The address of the service's /v1 API.", sized(340, base)),
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
				say("Account connected. Load its models to choose which ones to show.")
				if auth, errorValue := ui.backend.GetOpenAI(profile); errorValue == nil {
					auth.Mu.Lock()
					email := auth.Account.Email
					auth.Mu.Unlock()
					showAccount(email)
				}
			})
		}()
	}
	oauthFields := card(settingStack("ChatGPT account", "Uses your ChatGPT subscription, with a sign-in in your browser (OAuth with PKCE).",
		container.NewVBox(account, container.NewHBox(login), loginLink)))
	claudeFields := card(settingRow("Claude Code on this computer",
		"Uses the Claude Code installed here, unmodified, with its own sign-in (your Claude subscription), tools, permission rules, CLAUDE.md and MCP servers. The work runs in Claude Code's harness: 1loop sends your messages, shows what it does and relays its permission requests. If a run is ever billed outside your subscription, 1loop stops it.", nil))
	access := container.NewStack(apiFields, oauthFields, claudeFields)

	routing := jsonEntry(draft.Routing)
	parameters := jsonEntry(draft.Parameters)
	routingRow := settingStack("OpenRouter routing (JSON)", "Provider order, fallbacks and the other routing options, for every model of this connection.", routing)
	advanced := container.NewVBox()
	showAdvanced := func() {
		rows := []fyne.CanvasObject{}
		if draft.Provider == "openrouter" {
			rows = append(rows, routingRow)
		}
		rows = append(rows, settingStack("Request parameters (JSON)",
			"For instance temperature, top_p, max_tokens, seed, chat_template_kwargs. The model, the tools and streaming are 1loop's own.", parameters))
		advanced.Objects = []fyne.CanvasObject{card(rows...)}
		advanced.Refresh()
	}

	// Models: which ones of the catalog appear in the composer.
	var models []agent.ModelChoice
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
			modelRows.Add(settingRow("No catalog loaded", "Load the models to choose which ones to show and to configure them one by one.", nil))
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
				models = choices
				listModels()
				say(fmt.Sprintf("%d models loaded.", len(models)))
			})
		}()
	}

	typePicker.onChanged = func(label string) {
		previous := draft.Provider
		draft.Provider = ids[label]
		apiFields.Hide()
		oauthFields.Hide()
		claudeFields.Hide()
		switch draft.Provider {
		case "openai":
			oauthFields.Show()
		case "claudecode":
			claudeFields.Show()
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
			allModels.set(true)
			listModels()
		}
		showAdvanced()
		access.Refresh()
	}
	typePicker.SetSelected(agent.ProviderName(draft.Provider))
	typePicker.onChanged(agent.ProviderName(draft.Provider))
	listModels()

	title := draft.Name
	if !existing {
		title = "New connection"
	}
	page := container.NewVBox(
		settings.pageTitle(title, "Connections", nil),
		card(
			settingRow("Name", "As it appears in the settings.", sized(340, name)),
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
		sectionTitle("Advanced"),
		advanced,
	)
	if existing {
		remove := newRow("Remove", iconTrash, styleOutline, func() {
			ui.removeConnection(original, func() { settings.done() })
		})
		remove.danger = true
		page.Add(sectionTitle("Remove"))
		page.Add(card(settingRow("Remove this connection", "Existing chats are kept. A ChatGPT account is signed out.", remove)))
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
		draft.Routing = route
		draft.Parameters = params
		if draft.Provider != "openai" && draft.Provider != "claudecode" {
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
		container.NewCenter(sized(360, status)),
		cancel,
		save,
	}, beforeLeave: beforeLeave, leave: func() {
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
			settingRow("Upstream provider", "Which provider OpenRouter sends this model's requests to.", container.New(layout.NewCustomPaddedHBoxLayout(8), fetch, sized(220, upstream.row))),
			settingRow("Allow fallback providers", "", fallbacks),
			settingStack("Routing overrides (JSON)", "", routing)))
	}
	page.Add(sectionTitle("Parameters"))
	page.Add(card(settingStack("Request parameter overrides (JSON)",
		"They replace the matching defaults of the connection. Save the connection afterwards to apply them.", parameters)))
	page.Add(spacer(0, 16))
	save := newRow("Save model settings", nil, stylePrimary, func() {
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
		conn.ModelOptions[model.ID] = agent.ModelPreferences{Provider: route, Parameters: params}
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
