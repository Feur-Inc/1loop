// SPDX-License-Identifier: MPL-2.0

package ui

// The Loopy space (#352): a personal agent apart from the projects, reached
// from the logo at the top of the sidebar, which switches between 1loop and
// Loopy as OpenAI's app switches between ChatGPT and Codex. Loopy has its
// own sidebar (its face and what it is doing, what waits for the user, the
// coding tasks it handed over, its routines and its memory) and one
// continuous conversation in text-message bubbles, with reactions and cards.
// Its runs, tools and prompt are in loopy_run.go; its cards in
// loopy_cards.go; its face in loopy_face.go; what it keeps in internal/loopy.

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/browser"
	"1loop/internal/loopy"
)

// The user's bubbles: the art direction's deep teal.
var colLoopyMe = color.NRGBA{R: 0x0f, G: 0x7a, B: 0x6e, A: 0xff}

// loopyMode is the Loopy space, built the first time it is opened.
type loopyMode struct {
	ui    *desktopUI
	store *loopy.Store
	chat  *conversation // hidden: what runConfig needs, never in the sidebar

	sidebar, main fyne.CanvasObject
	bigFace       *loopyFace
	status        *canvas.Text
	waiting       *fyne.Container
	tasks         *fyne.Container
	routines      *fyne.Container
	memoryRow     *tapRow
	sideList      *fyne.Container // everything under the face, laid out again when a list changes

	feed        *fyne.Container
	scroll      *kineticScroll
	body        *fyne.Container // the conversation, or the memory page over it
	typing      *fyne.Container
	typeFace    *loopyFace
	typeLabel   *canvas.Text
	prompt      *chatEntry
	sendBtn     *tapRow
	modelChip   *tapRow
	contextChip *tapRow // how full Loopy's context is, compacted as 1loop's chats are
	browserBtn  *tapRow
	browserPane *fyne.Container // Loopy's browser tab, 1loop's own view, beside the conversation
	browserTab  fyne.CanvasObject
	browserOn   bool // the user wants the pane while there is a page
	contextUsed int  // its last request's input tokens
	window      int  // its model's context window

	bubbles map[string]*loopyBubble // Loopy's bubbles on screen, by message
	run     *loopyRun
	events  chan func()  // the run's events, applied in order on the interface's thread
	stream  *loopyBubble // the bubble Loopy is writing
	queued  []string     // messages sent while it was busy
	timer   *time.Ticker
}

// openLoopy is the switch's Loopy: built once, then shown.
func (ui *desktopUI) openLoopy() {
	if ui.loopy == nil {
		ui.loopy = newLoopyMode(ui)
	}
	ui.showSpace(true)
}

// showSpace puts Loopy's sidebar and page in place of 1loop's, or back.
func (ui *desktopUI) showSpace(loopySpace bool) {
	if ui.root == nil || len(ui.root.Objects) < 2 {
		return
	}
	if ui.oneloopSide == nil {
		ui.oneloopSide, ui.oneloopMain = ui.root.Objects[0], ui.root.Objects[1]
	}
	mode := ui.loopy
	if loopySpace && mode != nil {
		ui.root.Objects[0], ui.root.Objects[1] = mode.sidebar, mode.main
		mode.shown(true)
	} else {
		ui.root.Objects[0], ui.root.Objects[1] = ui.oneloopSide, ui.oneloopMain
		if mode != nil {
			mode.shown(false)
		}
	}
	ui.inLoopy = loopySpace && mode != nil
	if mode != nil {
		mode.store.Lock()
		mode.store.Settings.Shown = ui.inLoopy
		mode.store.Unlock()
		go mode.store.Save()
	}
	ui.root.Refresh()
	if ui.inLoopy {
		ui.window.Canvas().Focus(mode.prompt)
	}
}

// spaceSwitch is the logo at the top of the sidebar, with its menu: 1loop
// or Loopy, as ChatGPT or Codex in OpenAI's app.
func (ui *desktopUI) spaceSwitch(mark fyne.CanvasObject, name string) fyne.CanvasObject {
	chevron := canvas.NewImageFromResource(tinted(iconChevron, colMuted))
	chevron.SetMinSize(fyne.NewSquareSize(14))
	var parts []fyne.CanvasObject
	parts = append(parts, container.NewCenter(mark))
	if name != "" {
		title := textLine(name, 18, colText)
		title.TextStyle = fyne.TextStyle{Bold: true}
		parts = append(parts, container.NewCenter(title))
	}
	parts = append(parts, container.NewCenter(chevron))
	row := container.New(layout.NewCustomPaddedHBoxLayout(6), parts...)
	var anchor fyne.CanvasObject
	tap := newTapArea(func() {
		inLoopy := ui.inLoopy
		showMenu(ui.window, anchor, []menuAction{
			{label: "1loop", detail: "Build, debug and ship in your projects", checked: !inLoopy, run: func() { ui.showSpace(false) }},
			{label: "Loopy", detail: "Your personal agent: mail, errands, routines", checked: inLoopy, run: ui.openLoopy},
		})
	})
	anchor = container.NewStack(row, tap)
	return container.NewHBox(anchor)
}

func newLoopyMode(ui *desktopUI) *loopyMode {
	dir := filepath.Dir(ui.statePath)
	mode := &loopyMode{ui: ui, store: loopy.Open(filepath.Join(dir, "loopy.json"))}
	mode.chat = &conversation{ID: "loopy", Title: "Loopy"}
	if len(mode.store.Snapshot()) == 0 {
		mode.greet()
	}
	mode.buildSidebar()
	mode.buildMain()
	mode.renderFeed()
	mode.renderSide()
	mode.startClock()
	return mode
}

// shown starts or stops what only moves on screen.
func (mode *loopyMode) shown(on bool) {
	defer mode.syncBrowser()
	for _, face := range []*loopyFace{mode.bigFace, mode.typeFace} {
		if on {
			face.start()
		} else {
			face.stop()
		}
	}
	if on && mode.run == nil {
		mode.bigFace.set("hello") // it greets the user each time they come
		time.AfterFunc(3200*time.Millisecond, func() { fyne.Do(mode.settle) })
	}
}

// settle is the face at rest: asleep at night, else idle, or waiting while
// a card waits for the user.
func (mode *loopyMode) settle() {
	if mode.run != nil {
		return
	}
	state, text := "idle", "Here when you need me"
	switch hour := time.Now().Hour(); {
	case len(mode.store.Waiting()) > 0:
		state, text = "waiting", "Waiting for you"
	case hour >= 23 || hour < 7:
		state, text = "sleep", "Watching quietly"
	}
	mode.setState(state, text)
}

// setState moves both faces and says what Loopy is doing.
func (mode *loopyMode) setState(state, text string) {
	mode.bigFace.set(state)
	mode.typeFace.set(state)
	mode.status.Text = text
	mode.status.Refresh()
}

func (mode *loopyMode) buildSidebar() {
	ui := mode.ui
	mode.bigFace = newLoopyFace(76, "hello", false, false)
	mode.status = textLine("Here when you need me", 13, colMuted)
	mode.status.Alignment = fyne.TextAlignCenter
	name := textLine("Loopy", 15, colText)
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.Alignment = fyne.TextAlignCenter
	hero := container.NewVBox(spacer(0, 8), container.NewCenter(mode.bigFace.image), spacer(0, 6), name, mode.status, spacer(0, 10))

	mode.waiting = container.NewVBox()
	mode.tasks = container.NewVBox()
	mode.routines = container.NewVBox()
	mode.memoryRow = newRow("What I remember", iconMemory, styleNav, mode.openMemory)
	newConversation := newRow("Start fresh", iconPlus, styleNav, mode.askClear)
	mode.sideList = container.NewVBox(hero,
		sectionHeader("Waiting for you", nil), mode.waiting,
		sectionHeader("Delegated tasks", nil), mode.tasks,
		sectionHeader("Routines", nil), mode.routines,
		sectionHeader("Loopy", nil), mode.memoryRow, newConversation)

	face := newLoopyFace(26, "idle", true, true)
	collapse := newRow("", iconSidebar, styleWindow, ui.toggleSidebar)
	var brand fyne.CanvasObject = padded(10, 12, 0, 0, container.NewBorder(nil, nil, ui.spaceSwitch(face.image, "Loopy"), container.NewCenter(collapse)))
	if ui.chrome {
		brand = container.NewStack(newDragArea(ui.window), brand)
	}
	divider := canvas.NewRectangle(colBorderSoft)
	divider.SetMinSize(fyne.NewSize(0, 1))
	settings := newRow("Settings", theme.SettingsIcon(), styleNav, func() { ui.showSpace(false); ui.settings() })
	bottom := container.NewVBox(divider, padded(6, 8, 0, 0, settings))
	content := padded(0, 0, 12, 12, container.NewBorder(brand, bottom, nil, nil, newKineticVScroll(mode.sideList)))
	mode.sidebar = container.NewStack(canvas.NewRectangle(colSidebar), content)
}

func (mode *loopyMode) buildMain() {
	ui := mode.ui
	mode.feed = container.NewVBox()
	mode.scroll = newKineticVScroll(container.New(columnLayout{width: 720, inset: 24}, padded(18, 18, 0, 0, mode.feed)))
	mode.typeFace = newLoopyFace(22, "idle", false, false)
	mode.typeLabel = textLine("", 13, colMuted)
	mode.typing = container.NewHBox(container.NewCenter(mode.typeFace.image), container.NewCenter(mode.typeLabel))
	mode.typing.Hide()

	mode.prompt = newChatEntry()
	mode.prompt.SetPlaceHolder("Message Loopy…")
	mode.prompt.send = mode.submit
	mode.prompt.paste = ui.pasteFromClipboard
	mode.prompt.resized = func() {
		if mode.main != nil {
			mode.main.Refresh()
		}
	}
	mode.sendBtn = newIconButton(iconSend, colAmber, colInk, mode.submit)
	mode.sendBtn.iconPx = 20
	// The field has no fill of its own: the card is its only background.
	input := container.NewThemeOverride(mode.prompt, bareInputTheme{loopTheme{theme.DefaultTheme()}})
	composer := surface(padded(6, 6, 12, 8, container.NewBorder(nil, nil, nil, container.NewCenter(mode.sendBtn), input)), colSurface, colBorder, 20, 0)
	footer := container.New(columnLayout{width: 720, inset: 24}, container.NewVBox(padded(0, 6, 4, 0, mode.typing), padded(0, 16, 0, 0, composer)))

	mode.body = container.NewStack(mode.scroll)
	mode.modelChip = newRow(mode.modelLabel(), nil, styleChip, mode.chooseModel)
	mode.contextChip = newRow("", nil, styleChip, func() { ui.showSpace(false); ui.settings(); ui.prefs.openOn(loopySettingsPage) })
	mode.contextChip.Hide()
	title := textLine("Loopy", 14, colMuted)
	right := container.New(layout.NewCustomPaddedHBoxLayout(2))
	mode.browserBtn = newRow("Browser", iconBrowser, styleChip, func() {
		mode.browserOn = !mode.browserOn
		mode.syncBrowser()
	})
	mode.browserBtn.Hide()
	right.Add(container.NewCenter(mode.browserBtn))
	right.Add(container.NewCenter(mode.contextChip))
	right.Add(container.NewCenter(mode.modelChip))
	if ui.chrome {
		right.Add(windowControls(ui.window, ui.closeWindow))
	}
	bar := container.NewBorder(nil, nil, container.NewCenter(title), right)
	var header fyne.CanvasObject = padded(14, 8, 24, 24, bar)
	if ui.chrome {
		header = container.NewStack(newDragArea(ui.window), padded(8, 4, 20, 8, bar))
	}
	mode.browserPane = container.NewStack()
	mode.browserPane.Hide()
	conversation := container.NewBorder(nil, footer, nil, nil, mode.body)
	mode.main = container.NewStack(canvas.NewRectangle(colBackground),
		container.NewBorder(header, nil, nil, nil, container.New(&loopySplit{pane: mode.browserPane}, conversation, mode.browserPane)))
}

// ---------------------------------------------------------------------------
// The conversation.

// renderFeed draws the conversation again, its last 200 messages.
func (mode *loopyMode) renderFeed() {
	messages := mode.store.Snapshot()
	if len(messages) > 200 {
		messages = messages[len(messages)-200:]
	}
	var rows []fyne.CanvasObject
	var day time.Time
	mode.bubbles = map[string]*loopyBubble{}
	for _, message := range messages {
		local := message.Time.Local()
		if day.IsZero() || local.YearDay() != day.YearDay() || local.Year() != day.Year() {
			day = local
			rows = append(rows, loopyDay(local))
		}
		rows = append(rows, mode.messageRow(message))
	}
	mode.feed.Objects = rows
	mode.feed.Refresh()
	mode.toEnd()
}

// toEnd scrolls to the latest message once it is laid out.
func (mode *loopyMode) toEnd() {
	fyne.Do(func() {
		mode.scroll.Refresh()
		mode.scroll.ScrollToBottom()
	})
}

// loopyDay is the line between two days: "Tuesday · 12 November".
func loopyDay(day time.Time) fyne.CanvasObject {
	text := day.Format("Monday · 2 January")
	now := time.Now()
	if day.YearDay() == now.YearDay() && day.Year() == now.Year() {
		text = "Today"
	} else if y := now.AddDate(0, 0, -1); day.YearDay() == y.YearDay() && day.Year() == y.Year() {
		text = "Yesterday"
	}
	label := textLine(strings.ToUpper(text), 11, colFaint)
	label.TextStyle = fyne.TextStyle{Monospace: true}
	left, right := canvas.NewRectangle(colBorderSoft), canvas.NewRectangle(colBorderSoft)
	left.SetMinSize(fyne.NewSize(0, 1))
	right.SetMinSize(fyne.NewSize(0, 1))
	return padded(14, 8, 0, 0, container.New(&dayRuleLayout{}, left, label, right))
}

// dayRuleLayout centres a label between two rules.
type dayRuleLayout struct{}

func (dayRuleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(objects[1].MinSize().Width+40, objects[1].MinSize().Height)
}

func (dayRuleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	label := objects[1].MinSize()
	x := (size.Width - label.Width) / 2
	objects[1].Resize(label)
	objects[1].Move(fyne.NewPos(x, (size.Height-label.Height)/2))
	for i, from := range []float32{0, x + label.Width + 12} {
		width := x - 12
		objects[i*2].Resize(fyne.NewSize(width, 1))
		objects[i*2].Move(fyne.NewPos(from, size.Height/2))
	}
}

// messageRow is one message: a bubble, a note or a card.
func (mode *loopyMode) messageRow(message loopy.Message) fyne.CanvasObject {
	switch {
	case message.Card != nil:
		return padded(4, 4, 34, 0, mode.cardView(message))
	case message.Memory != "":
		return mode.memoryNote(message)
	case message.Note != "":
		return padded(2, 6, 34, 0, plainFlow(message.Note, 12, colFaint))
	case message.From == "me":
		bubble := newLoopyBubble(message.Text, true, message.Reaction)
		if message.Pending {
			bubble.dim()
		}
		return padded(3, 3, 0, 0, bubble.row)
	}
	bubble := newLoopyBubble(message.Text, false, message.Reaction)
	bubble.onMenu = func() { mode.reactMenu(message, bubble) }
	bubble.id = message.ID
	mode.bubbles[message.ID] = bubble
	return padded(3, 3, 0, 0, bubble.row)
}

// memoryNote is "📌 I'll remember: …" under Loopy's bubbles, with Forget.
func (mode *loopyMode) memoryNote(message loopy.Message) fyne.CanvasObject {
	pin := canvas.NewImageFromResource(tinted(iconPin, colAmber))
	pin.SetMinSize(fyne.NewSquareSize(14))
	row := container.NewHBox(container.NewCenter(pin), container.NewCenter(textLine(message.Memory, 12.5, colMuted)))
	if message.MemoryID != "" {
		row.Add(container.NewCenter(newRow("Forget", nil, styleChip, func() {
			mode.store.Forget(message.MemoryID, "")
			mode.store.Update(message.ID, func(m *loopy.Message) { m.Memory, m.MemoryID = "I forgot it.", "" })
			go mode.store.Save()
			mode.renderFeed()
			mode.renderSide()
		})))
	}
	return padded(2, 6, 34, 0, row)
}

// reactMenu lets the user put a reaction on one of Loopy's messages.
func (mode *loopyMode) reactMenu(message loopy.Message, bubble *loopyBubble) {
	var actions []menuAction
	for _, name := range loopyReactionNames {
		actions = append(actions, menuAction{label: loopyReactionLabel[name], icon: loopyReactionIcon(name), checked: message.Reaction == name, run: func() {
			mode.store.Update(message.ID, func(m *loopy.Message) {
				if m.Reaction == name {
					m.Reaction = ""
				} else {
					m.Reaction = name
				}
			})
			go mode.store.Save()
			mode.renderFeed()
		}})
	}
	showMenu(mode.ui.window, bubble.box, actions)
}

// greet is Loopy's first message, on the first visit.
func (mode *loopyMode) greet() {
	mode.store.Add(loopy.Message{From: "loopy", Text: "Hi, I'm Loopy. I take care of the everyday things: mail, errands, reminders, and the coding tasks I can hand to an agent in your projects. I always ask before anything that matters."})
	mode.store.Add(loopy.Message{From: "loopy", Text: "Tell me what you need, or ask me to remember something about you."})
	go mode.store.Save()
}

// askClear starts a fresh conversation; what Loopy remembers stays.
func (mode *loopyMode) askClear() {
	var dialog *sheet
	dialog = mode.ui.modal("Start fresh?", plainFlow("Loopy forgets this conversation. What it remembers about you, its tasks and its routines stay.", 13, colTextSoft), 420, nil,
		newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Start fresh", nil, stylePrimary, func() {
			dialog.dismiss()
			if mode.run != nil {
				mode.run.cancel()
			}
			mode.store.Lock()
			mode.store.Messages, mode.store.History = nil, nil
			mode.store.Unlock()
			mode.greet()
			mode.renderFeed()
			mode.renderSide()
		}))
}

// ---------------------------------------------------------------------------
// The sidebar's lists.

// renderSide lists what waits for the user, the tasks and the routines.
func (mode *loopyMode) renderSide() {
	mode.waiting.Objects = nil
	for _, message := range mode.store.Waiting() {
		card := message.Card
		mode.waiting.Add(newRow(card.Title, loopyCardIcon(card.Kind), styleNav, mode.scrollTo))
	}
	if len(mode.waiting.Objects) == 0 {
		mode.waiting.Add(padded(0, 6, 10, 0, textLine("Nothing right now", 12.5, colFaint)))
	}
	mode.waiting.Refresh()

	mode.tasks.Objects = nil
	for _, task := range mode.store.TaskList() {
		row := newRow(task.Title, iconCode, styleNav, func() { mode.openTask(task) })
		row.trailing = map[string]string{"proposed": "proposed", "running": "working", "done": "done", "failed": "failed", "cancelled": "cancelled"}[task.Status]
		mode.tasks.Add(row)
	}
	if len(mode.tasks.Objects) == 0 {
		mode.tasks.Add(padded(0, 6, 10, 0, textLine("None yet", 12.5, colFaint)))
	}
	mode.tasks.Refresh()

	mode.routines.Objects = nil
	for _, routine := range mode.store.RoutineList() {
		row := newRow(routine.Title, iconClock, styleNav, func() { mode.routineMenu(routine) })
		row.trailing = "off"
		if routine.On {
			row.trailing = loopy.Describe(routine.When)
		}
		mode.routines.Add(row)
	}
	if len(mode.routines.Objects) == 0 {
		mode.routines.Add(padded(0, 6, 10, 0, textLine("Ask Loopy to set one up", 12.5, colFaint)))
	}
	mode.routines.Refresh()

	mode.memoryRow.SetTrailing(fmt.Sprint(len(mode.store.Facts())))
	mode.sideList.Refresh()
}

// scrollTo brings the conversation back to its end, where cards wait.
func (mode *loopyMode) scrollTo() {
	mode.closeMemory()
	mode.toEnd()
}

// routineMenu turns a routine on or off, runs it now or removes it.
func (mode *loopyMode) routineMenu(routine loopy.Routine) {
	toggle := "Turn on"
	if routine.On {
		toggle = "Turn off"
	}
	actions := []menuAction{
		{label: toggle, detail: loopy.Describe(routine.When), run: func() { mode.setRoutine(routine.ID, !routine.On) }},
		{label: "Run now", detail: routine.Task, run: func() { mode.runRoutine(routine) }},
		{label: "Remove", danger: true, run: func() {
			mode.store.RemoveRoutine(routine.ID)
			go mode.store.Save()
			mode.renderSide()
		}},
	}
	showMenu(mode.ui.window, mode.routines, actions)
}

func (mode *loopyMode) setRoutine(id string, on bool) {
	mode.store.UpdateRoutine(id, func(r *loopy.Routine) {
		r.On = on
		if on {
			r.LastRun = time.Now() // the schedule counts from now
		}
	})
	go mode.store.Save()
	mode.renderSide()
}

// ---------------------------------------------------------------------------
// The memory page.

func (mode *loopyMode) openMemory() {
	facts := mode.store.Facts()
	page := container.NewVBox(container.NewHBox(newRow("Back to the conversation", iconChevronLeft, styleChip, mode.closeMemory)),
		padded(6, 6, 0, 0, textLine("What Loopy remembers", 24, colText)),
		plainFlow("Loopy's own memory, apart from your projects: facts about you, by kind, and its journal of what it did. Pinned facts are in every conversation; it searches the others, its journal and your past conversation when it needs them. Passwords and card numbers never go here.", 13, colMuted))
	if len(facts) == 0 {
		page.Add(spacer(0, 12))
		page.Add(card(padded(16, 16, 16, 16, note("Nothing yet. Tell Loopy \"remember that…\", or it notes what matters as you talk."))))
	}
	names := map[string]string{"preference": "Preferences", "person": "People", "place": "Places", "work": "Work", "routine": "Habits", "other": "Other"}
	for _, category := range loopy.Categories {
		var rows []fyne.CanvasObject
		for i := len(facts) - 1; i >= 0; i-- {
			fact := facts[i]
			if fact.Category != category && !(category == "other" && fact.Category == "") {
				continue
			}
			detail := "Since " + fact.Time.Local().Format("2 Jan 2006")
			if fact.Pinned {
				detail = "Pinned · " + detail
			}
			pin := "Pin"
			if fact.Pinned {
				pin = "Unpin"
			}
			controls := container.NewHBox(
				newRow(pin, nil, styleChip, func() { mode.changeFact(fact.ID, func(f *loopy.Fact) { f.Pinned = !f.Pinned }) }),
				newRow("Edit", nil, styleChip, func() { mode.editFact(fact) }),
				newRow("Forget", nil, styleChip, func() {
					mode.store.Forget(fact.ID, "")
					mode.store.Log("Forgot: " + fact.Text)
					go mode.store.Save()
					mode.renderSide()
					mode.openMemory()
				}))
			rows = append(rows, settingRow(fact.Text, detail, controls))
		}
		if len(rows) > 0 {
			page.Add(sectionTitle(names[category]))
			page.Add(card(rows...))
		}
	}
	if entries := mode.store.Entries(); len(entries) > 0 {
		page.Add(sectionTitle("Journal"))
		var rows []fyne.CanvasObject
		for i := len(entries) - 1; i >= 0 && i >= len(entries)-60; i-- {
			rows = append(rows, settingRow(entries[i].Text, entries[i].Time.Local().Format("Mon 2 Jan 2006, 15:04"), nil))
		}
		page.Add(card(rows...))
	}
	scroll := newKineticVScroll(container.New(columnLayout{width: 720, inset: 24}, padded(18, 24, 0, 0, page)))
	mode.body.Objects = []fyne.CanvasObject{mode.scroll, container.NewStack(canvas.NewRectangle(colBackground), scroll)}
	mode.body.Refresh()
}

// changeFact changes a fact from the memory page.
func (mode *loopyMode) changeFact(id string, change func(*loopy.Fact)) {
	mode.store.UpdateFact(id, change)
	go mode.store.Save()
	mode.renderSide()
	mode.openMemory()
}

// editFact lets the user correct a fact and its kind.
func (mode *loopyMode) editFact(fact loopy.Fact) {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetText(fact.Text)
	labels := []string{"Preference", "Person", "Place", "Work", "Habit", "Other"}
	kind := widget.NewSelect(labels, nil)
	for i, category := range loopy.Categories {
		if category == fact.Category {
			kind.SetSelected(labels[i])
		}
	}
	if kind.Selected == "" {
		kind.SetSelected("Other")
	}
	var dialog *sheet
	dialog = mode.ui.modal("Edit what Loopy remembers", container.NewVBox(sized(420, entry), container.NewBorder(nil, nil, container.NewCenter(textLine("Kind", 13, colMuted)), nil, kind)), 480, nil,
		newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Save", nil, stylePrimary, func() {
			dialog.dismiss()
			text := strings.TrimSpace(entry.Text)
			if text == "" {
				return
			}
			mode.changeFact(fact.ID, func(f *loopy.Fact) {
				f.Text = text
				for i, label := range labels {
					if label == kind.Selected {
						f.Category = loopy.Categories[i]
					}
				}
			})
		}))
}

// syncContext shows how full Loopy's context is, as the chat's context line
// does in 1loop.
func (mode *loopyMode) syncContext() {
	if mode.window <= 0 || mode.contextUsed <= 0 {
		mode.contextChip.Hide()
		return
	}
	percent := fmt.Sprintf("%d%%", min(100, mode.contextUsed*100/mode.window))
	if mode.contextUsed*100 < mode.window {
		percent = "<1%"
	}
	mode.contextChip.SetText("Context " + percent)
	mode.contextChip.Show()
}

func (mode *loopyMode) closeMemory() {
	mode.body.Objects = []fyne.CanvasObject{mode.scroll}
	mode.body.Refresh()
}

// syncBrowser shows Loopy's active browser tab beside the conversation:
// the same view as in a project chat's side panel, live, which the user
// unlocks to take over.
func (mode *loopyMode) syncBrowser() {
	state := mode.ui.panel.stateOf(mode.chat)
	var view fyne.CanvasObject
	var page *browser.Page
	for i := len(state.tabs) - 1; i >= 0; i-- {
		tab := state.tabs[i]
		if tab.browser != nil && tab.content != nil {
			view, page = tab.content, tab.browser
			if i == state.active {
				break
			}
		}
	}
	// Chromium sends frames only for a page shown, as the side panel does
	// for a project chat's tab (syncBrowsers).
	for _, tab := range state.tabs {
		if tab.browser != nil {
			tab.browser.SetVisible(tab.browser == page && mode.browserOn && mode.ui.inLoopy)
		}
	}
	if view == nil {
		mode.browserBtn.Hide()
		mode.browserPane.Hide()
		return
	}
	mode.browserBtn.Show()
	mode.browserBtn.SetSelected(mode.browserOn)
	if view != mode.browserTab {
		mode.browserTab = view
		frame := canvas.NewRectangle(colSidebar)
		mode.browserPane.Objects = []fyne.CanvasObject{frame, padded(0, 12, 0, 12, view)}
	}
	if mode.browserOn {
		mode.browserPane.Show()
	} else {
		mode.browserPane.Hide()
	}
	mode.browserPane.Refresh()
	mode.main.Refresh()
}

// loopySplit puts the browser pane on the right, about half the width,
// when it shows.
type loopySplit struct{ pane *fyne.Container }

func (s *loopySplit) MinSize(objects []fyne.CanvasObject) fyne.Size { return objects[0].MinSize() }

func (s *loopySplit) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := size.Width
	if s.pane.Visible() {
		pane := fyne.Max(380, size.Width*.48)
		width = size.Width - pane
		objects[1].Resize(fyne.NewSize(pane, size.Height))
		objects[1].Move(fyne.NewPos(width, 0))
	}
	objects[0].Resize(fyne.NewSize(width, size.Height))
	objects[0].Move(fyne.NewPos(0, 0))
}
