package main

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
)

func note(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.Importance = widget.LowImportance
	return label
}

func jsonText(value map[string]any) string {
	if len(value) == 0 {
		return "{}"
	}
	b, _ := json.MarshalIndent(value, "", "  ")
	return string(b)
}

func cloneConnection(c connection) connection {
	b, _ := json.Marshal(c)
	var copy connection
	_ = json.Unmarshal(b, &copy)
	return copy
}

func jsonEntry(value map[string]any) *widget.Entry {
	entry := widget.NewMultiLineEntry()
	entry.TextStyle = fyne.TextStyle{Monospace: true}
	entry.SetMinRowsVisible(5)
	entry.SetText(jsonText(value))
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
	u       *desktopUI
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

func (u *desktopUI) buildSettings() {
	v := &settingsView{u: u, rows: map[string]*tapRow{}, section: "General"}
	back := newRow("Back to 1loop", iconChevronLeft, styleNav, v.close)
	nav := container.New(layout.NewCustomPaddedVBoxLayout(1))
	for _, section := range settingsSections {
		name := section.name
		row := newRow(name, section.icon, styleNav, func() { v.show(name) })
		v.rows[name] = row
		nav.Add(row)
	}
	v.side = container.NewVBox(back, padded(14, 6, 12, 0, textLine("Settings", 13, colFaint)), nav)
	v.side.Hide()

	v.column = container.NewStack()
	v.scroll = newKineticVScroll(container.New(columnLayout{width: 760, inset: 28}, v.column))
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	v.barRow = container.New(layout.NewCustomPaddedHBoxLayout(8))
	v.bar = container.NewVBox(rule, container.New(columnLayout{width: 760, inset: 12}, container.NewHBox(layout.NewSpacer(), v.barRow)))
	v.bar.Hide()
	v.view = container.NewBorder(nil, v.bar, nil, nil, container.NewThemeOverride(v.scroll, flatScrollTheme{loopTheme{theme.DefaultTheme()}}))
	v.view.Hide()
	u.prefs = v
}

// settings opens the page (the sidebar's Settings row).
func (u *desktopUI) settings() { u.prefs.openOn("") }

func (v *settingsView) openOn(section string) {
	u := v.u
	if section == "" {
		section = v.section
	}
	if !v.open {
		devf("ui", "settings opened")
		v.open = true
		u.clearSelection()
		v.panelOn = u.panelToggle.Visible()
		u.panelToggle.Hide()
		u.sideApp.Hide()
		v.side.Show()
		u.split.Hide()
		v.view.Show()
		u.header.SetText("Settings")
	}
	v.show(section)
}

// close goes back to the conversation.
func (v *settingsView) close() {
	if !v.open {
		return
	}
	v.confirmLeave(0, v.closeNow)
}

func (v *settingsView) closeNow() {
	u := v.u
	for len(v.frames) > 0 {
		v.drop()
	}
	v.open = false
	v.side.Hide()
	u.sideApp.Show()
	v.view.Hide()
	u.split.Show()
	if v.panelOn {
		u.panelToggle.Show()
	}
	u.updateHeader()
	u.relayoutColumn()
	u.window.Canvas().Focus(u.prompt)
}

// show opens a section, leaving whatever was open.
func (v *settingsView) show(section string) {
	v.confirmLeave(0, func() { v.showNow(section) })
}

func (v *settingsView) showNow(section string) {
	for len(v.frames) > 0 {
		v.drop()
	}
	v.section = section
	for name, row := range v.rows {
		row.SetSelected(name == section)
	}
	var page fyne.CanvasObject
	switch section {
	case "Connections":
		page = v.u.connectionSettings()
	case "Instructions":
		page = v.u.instructionSettings()
	case "Phone remote":
		page = v.u.remoteSettings()
	default:
		page = v.u.generalSettings()
	}
	v.frames = []settingsFrame{{content: page}}
	v.present()
}

// push opens a page over the current one.
func (v *settingsView) push(frame settingsFrame) {
	v.frames = append(v.frames, frame)
	v.present()
}

// drop leaves the page on top.
func (v *settingsView) drop() {
	if n := len(v.frames); n > 0 {
		if leave := v.frames[n-1].leave; leave != nil {
			leave()
		}
		v.frames = v.frames[:n-1]
	}
}

// back returns to the page below, or to the conversation from a section.
func (v *settingsView) back() {
	if len(v.frames) <= 1 {
		v.close()
		return
	}
	v.confirmLeave(len(v.frames)-1, func() {
		v.drop()
		v.present()
	})
}

func (v *settingsView) confirmLeave(keep int, next func()) {
	for i := len(v.frames) - 1; i >= keep; i-- {
		if ask := v.frames[i].beforeLeave; ask != nil {
			ask(next)
			return
		}
	}
	next()
}

// done leaves the page on top after it saved: the one below is built again
// from what is now stored.
func (v *settingsView) done() {
	if n := len(v.frames); n > 1 {
		v.frames[n-1].leave = nil
		v.frames[n-1].beforeLeave = nil
	}
	if len(v.frames) == 2 {
		v.show(v.section)
		return
	}
	v.back()
}

func (v *settingsView) present() {
	frame := v.frames[len(v.frames)-1]
	v.column.Objects = []fyne.CanvasObject{frame.content}
	v.column.Refresh()
	v.barRow.Objects = frame.actions
	if len(frame.actions) > 0 {
		v.bar.Show()
	} else {
		v.bar.Hide()
	}
	v.view.Refresh()
	v.scroll.ScrollToTop()
}

// ---------------------------------------------------------------------------
// What the pages are made of.

func plainFlow(text string, size float32, ink color.Color) *flowText {
	return newFlowText([]span{{text: text}}, size, ink)
}

// pageTitle heads a page; back, when given, names where the page was opened
// from and returns there.
func (v *settingsView) pageTitle(title, back string, trailing fyne.CanvasObject) fyne.CanvasObject {
	heading := container.NewBorder(nil, nil, nil, trailing, textLine(title, 26, colText))
	if back == "" {
		return padded(8, 14, 0, 0, heading)
	}
	link := newRow(back, iconChevronLeft, styleChip, v.back)
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
	t := &toggle{on: on, onChange: onChange}
	if on {
		t.at = 1
	}
	t.ExtendBaseWidget(t)
	return t
}

func (t *toggle) Cursor() desktop.Cursor { return desktop.PointerCursor }

// set changes the switch without telling onChange.
func (t *toggle) set(on bool) {
	if t.on == on {
		return
	}
	t.on = on
	if t.anim != nil {
		t.anim.Stop()
	}
	from, to := t.at, float32(0)
	if on {
		to = 1
	}
	t.anim = fyne.NewAnimation(130*time.Millisecond, func(p float32) {
		t.at = from + (to-from)*p
		t.Refresh()
	})
	t.anim.Curve = fyne.AnimationEaseOut
	t.anim.Start()
}

func (t *toggle) Tapped(*fyne.PointEvent) {
	t.set(!t.on)
	if t.onChange != nil {
		t.onChange(t.on)
	}
}

func (t *toggle) CreateRenderer() fyne.WidgetRenderer {
	r := &toggleRenderer{t: t, track: canvas.NewRectangle(colBorder), knob: canvas.NewCircle(colMuted)}
	r.track.CornerRadius = 10
	r.Refresh()
	return r
}

type toggleRenderer struct {
	t     *toggle
	track *canvas.Rectangle
	knob  *canvas.Circle
}

func (r *toggleRenderer) MinSize() fyne.Size { return fyne.NewSize(36, 20) }
func (r *toggleRenderer) Layout(fyne.Size) {
	r.track.Resize(fyne.NewSize(36, 20))
	r.knob.Resize(fyne.NewSquareSize(14))
	r.knob.Move(fyne.NewPos(3+16*r.t.at, 3))
}
func (r *toggleRenderer) Refresh() {
	r.track.FillColor = mix(colBorder, colAmber, r.t.at)
	r.knob.FillColor = mix(colMuted, colInk, r.t.at)
	r.Layout(r.t.Size())
	r.track.Refresh()
	r.knob.Refresh()
}
func (r *toggleRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.track, r.knob} }
func (r *toggleRenderer) Destroy()                     {}

// ---------------------------------------------------------------------------
// General.

func (u *desktopUI) generalSettings() fyne.CanvasObject {
	v := u.prefs
	change := newRow("Change…", nil, styleOutline, func() {
		u.pick(pickRequest{title: "Choose the workspace", start: u.cfg.Workspace, folder: true}, func(paths []string) {
			root, err := resolveWorkspace(paths[0])
			if err != nil {
				u.notice("Workspace", err.Error())
				return
			}
			if root != u.cfg.Workspace {
				u.selectProject(root) // shows the project's start screen: come back here
				v.openOn("General")
			}
		})
	})
	mode := newPicker(u.window, "Tool permissions", styleField, nil)
	mode.Options = []string{"Approval required", "YOLO mode", "Auto mode (Claude Code)"}
	modes := []string{"approval", "yolo", "auto"}
	selected := mode.Options[0]
	for i, name := range modes {
		if u.approvalMode == name {
			selected = mode.Options[i]
		}
	}
	mode.SetSelected(selected)
	mode.onChanged = func(label string) {
		for i, option := range mode.Options {
			if label == option {
				u.approvalMode = modes[i]
			}
		}
		devf("ui", "approval mode %s", u.approvalMode)
		u.updateApprovalControl()
		u.save()
	}
	return container.NewVBox(
		v.pageTitle("General", "", nil),
		sectionTitle("Workspace"),
		card(settingRow("Project folder", u.cfg.Workspace, change)),
		sectionTitle("Permissions"),
		card(settingRow("Tool permissions",
			"Approval asks before each shell command or file edit. YOLO runs them at once, with your user's rights. Auto is Claude Code's own mode: it runs what it judges safe; with any other model it asks instead.",
			sized(230, mode.row))),
		sectionTitle("Context"),
		card(settingRow("Dynamic compaction",
			"On: 1loop compacts at the natural breaks of the work (when a prompt is done, between two tasks of the list, at a stopping point the model chooses), lightens a context whose provider cache has expired, and shortens very large tool outputs as they arrive. Off: the classic way, one summary when the context is nearly full, wherever the work is. Claude Code manages its own context either way.",
			newToggle(!u.classicCompaction, func(on bool) {
				u.classicCompaction = !on
				devf("ui", "dynamic compaction %v", on)
				u.save()
			}))),
	)
}

// ---------------------------------------------------------------------------
// Instructions.

func (u *desktopUI) instructionSettings() fyne.CanvasObject {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(14)
	entry.SetText(u.userInstructions)
	entry.SetPlaceHolder("Describe your preferences: language, tone, coding conventions, level of detail…")
	entry.OnChanged = func(text string) {
		u.userInstructions = strings.TrimSpace(text)
		u.save()
	}
	return container.NewVBox(
		u.prefs.pageTitle("Instructions", "", nil),
		card(settingStack("Your instructions",
			"Added to 1loop's own system prompt, whose workspace and tool rules stay in place. Saved as you type; they apply from the next request, in every chat.",
			entry)),
	)
}

// ---------------------------------------------------------------------------
// Connections.

func (u *desktopUI) connectionSettings() fyne.CanvasObject {
	v := u.prefs
	var rows []fyne.CanvasObject
	for _, c := range u.connectionsList {
		c := c
		enabled := newToggle(c.Enabled, func(on bool) {
			for i := range u.connectionsList {
				if u.connectionsList[i].ID == c.ID {
					u.connectionsList[i].Enabled = on
				}
			}
			devf("ui", "connection %q enabled %v", c.Name, on)
			u.save()
			u.refreshModels()
		})
		edit := newRow("Edit", iconChevronRight, styleChip, func() { u.connectionEditor(c) })
		edit.iconAfter = true
		rows = append(rows, settingRow(c.Name, providerName(c.Provider), container.New(layout.NewCustomPaddedHBoxLayout(14), container.NewCenter(enabled), edit)))
	}
	add := newRow("Add connection", iconPlus, stylePrimary, func() {
		id := identifier()
		u.connectionEditor(connection{ID: id, AuthProfile: id, Name: "New connection", Provider: "compatible", Enabled: true, AllModels: true})
	})
	page := container.NewVBox(v.pageTitle("Connections", "", container.NewCenter(add)),
		padded(0, 10, 2, 0, plainFlow("Connect several services and accounts at once. The models of every enabled connection appear together in the composer.", 13, colMuted)))
	if len(rows) == 0 {
		rows = append(rows, settingRow("No connection yet", "Add one to get started.", nil))
	}
	page.Add(card(rows...))
	return page
}

// removeConnection forgets a connection (and signs a ChatGPT account out).
func (u *desktopUI) removeConnection(c connection, done func()) {
	u.confirm("Remove connection?", "Remove “"+c.Name+"” from 1loop? Existing chats are kept. ChatGPT sessions will be disconnected.", "Remove", func() {
		finish := func() {
			kept := []connection{}
			for _, entry := range u.connectionsList {
				if entry.ID != c.ID {
					kept = append(kept, entry)
				}
			}
			u.connectionsList = kept
			delete(u.keys, c.ID)
			u.save()
			u.refreshModels()
			done()
		}
		if c.Provider != "openai" {
			finish()
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(u.ctx, 25*time.Second)
			defer cancel()
			warning, err := u.backend.disconnectOpenAI(ctx, c.AuthProfile)
			fyne.Do(func() {
				if err != nil {
					u.notice("Unable to disconnect", err.Error())
					return
				}
				finish()
				if warning != "" {
					u.notice("Account disconnected", warning)
				}
			})
		}()
	})
}

// connectionEditor opens a connection's page: a draft, saved as a whole.
func (u *desktopUI) connectionEditor(original connection) {
	v := u.prefs
	draft := cloneConnection(original)
	existing := false
	for _, c := range u.connectionsList {
		existing = existing || c.ID == original.ID
	}
	name := widget.NewEntry()
	name.SetText(draft.Name)
	base := widget.NewEntry()
	base.SetText(draft.BaseURL)
	key := widget.NewPasswordEntry()
	key.SetText(u.keys[draft.ID])
	key.SetPlaceHolder("Kept in memory until 1loop closes")
	enabled := newToggle(draft.Enabled, func(on bool) { draft.Enabled = on })
	typePicker := newPicker(u.window, "Connection type", styleField, nil)
	typePicker.Options = []string{"Local vLLM", "OpenRouter", "ChatGPT account", "OpenAI-compatible API", "Claude Code"}
	ids := map[string]string{"Local vLLM": "vllm", "OpenRouter": "openrouter", "ChatGPT account": "openai", "OpenAI-compatible API": "compatible", "Claude Code": "claudecode"}
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
		if auth, err := u.backend.getOpenAI(draft.AuthProfile); err == nil {
			auth.mu.Lock()
			showAccount(auth.Account.Email)
			auth.mu.Unlock()
		}
	}
	var cancelLogin context.CancelFunc
	login := newRow("Continue with ChatGPT", nil, styleOutline, nil)
	login.onTap = func() {
		if cancelLogin != nil {
			cancelLogin()
		}
		ctx, cancel := context.WithTimeout(u.ctx, 5*time.Minute)
		cancelLogin = cancel
		profile := draft.AuthProfile
		login.Disable()
		say("Preparing sign-in…")
		go func() {
			defer cancel()
			raw, done, err := u.backend.beginOpenAI(ctx, profile)
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
				if auth, e := u.backend.getOpenAI(profile); e == nil {
					auth.mu.Lock()
					email := auth.Account.Email
					auth.mu.Unlock()
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
	var models []modelChoice
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
			include := newToggle(draft.includes(model.ID), func(on bool) {
				if draft.AllModels { // choosing one model ends "all of them"
					draft.Models = nil
					for _, m := range models {
						draft.Models = append(draft.Models, m.ID)
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
			configure := newRow("Configure", iconChevronRight, styleChip, func() { u.modelPreferencesEditor(&draft, model, key.Text) })
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
			choices, err := u.backend.connectionModels(u.ctx, snapshot, apiKey)
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
			base.SetText(defaultVLLMURL)
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
	typePicker.SetSelected(providerName(draft.Provider))
	typePicker.onChanged(providerName(draft.Provider))
	listModels()

	title := draft.Name
	if !existing {
		title = "New connection"
	}
	page := container.NewVBox(
		v.pageTitle(title, "Connections", nil),
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
			u.removeConnection(original, func() { v.done() })
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
			cfg := draft.config("")
			cfg.APIKey = key.Text
			if _, _, err = endpoint(cfg); err != nil {
				say(err.Error())
				return false
			}
		}
		found := false
		for i, c := range u.connectionsList {
			if c.ID == draft.ID {
				u.connectionsList[i] = cloneConnection(draft)
				found = true
				break
			}
		}
		if !found {
			u.connectionsList = append(u.connectionsList, cloneConnection(draft))
		}
		u.keys[draft.ID] = key.Text
		devf("ui", "connection %q saved", draft.Name)
		u.save()
		u.refreshModels()
		return true
	}
	save := newRow("Save connection", nil, stylePrimary, func() {
		if saveDraft() {
			v.done()
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
		warning = u.modal("Unsaved changes", note("Save your choices to keep them. Do you want to discard your changes?"), 520, nil, stay, discard, keep)
	}
	cancel := newRow("Cancel", nil, styleOutline, v.back)
	v.push(settingsFrame{content: page, actions: []fyne.CanvasObject{container.NewCenter(sized(360, status)), cancel, save}, beforeLeave: beforeLeave, leave: func() {
		loadGeneration++
		if cancelLogin != nil {
			cancelLogin()
		}
	}})
}

// modelPreferencesEditor opens one model's page, over its connection's.
func (u *desktopUI) modelPreferencesEditor(c *connection, model modelChoice, key string) {
	v := u.prefs
	preferences := c.ModelOptions[model.ID]
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
			labels[i] = effortLabel(effort)
		}
		reasoning = strings.Join(labels, ", ") + " (" + model.Source + ")"
	}
	page := container.NewVBox(
		v.pageTitle(model.Name, c.Name, nil),
		card(settingRow("Identifier", model.ID, nil), settingRow("Reasoning effort", reasoning, nil)),
	)
	if c.Provider == "openrouter" {
		upstream := newPicker(u.window, "Automatic provider routing", styleField, nil)
		upstream.Options = []string{"Automatic"}
		upstream.SetSelected("Automatic")
		if order := stringsFrom(preferences.Provider["order"]); len(order) > 0 {
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
			routing.SetText(jsonText(route))
		}
		fallbacks = newToggle(!ok || value, func(bool) { update() })
		upstream.onChanged = func(string) { update() }
		fetch := newRow("Fetch providers", nil, styleOutline, func() {
			snapshot := *c
			go func() {
				providers, err := u.backend.upstreamProviders(u.ctx, snapshot, key, model.ID)
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
		if c.Provider == "openrouter" {
			if err = json.Unmarshal([]byte(routing.Text), &route); err != nil || route == nil {
				say("Routing must be a JSON object.")
				return
			}
		}
		if c.ModelOptions == nil {
			c.ModelOptions = map[string]modelPreferences{}
		}
		c.ModelOptions[model.ID] = modelPreferences{Provider: route, Parameters: params}
		v.back()
	})
	v.push(settingsFrame{content: page, actions: []fyne.CanvasObject{container.NewCenter(sized(360, status)), newRow("Cancel", nil, styleOutline, v.back), save}})
}
