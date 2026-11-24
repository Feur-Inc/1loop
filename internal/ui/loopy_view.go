// SPDX-License-Identifier: MPL-2.0

package ui

// The Loopy space (#352): a personal agent apart from the projects, reached
// from the logo at the top of the sidebar, which switches between 1loop and
// Loopy as OpenAI's app switches between ChatGPT and Codex. Loopy has its
// own sidebar (its face and what it is doing, what waits for the user, the
// coding tasks it handed over, its routines, its memory and its folder) and
// one continuous conversation in text-message bubbles, with reactions and
// cards (loopy_cards.go). It works in internal/loopy/engine, on this
// computer or on a server (loopy_client.go): this only shows it. Its face is
// in loopy_face.go, its pages in loopy_pages.go.

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
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
	"1loop/internal/remoteapi"
)

// The user's bubbles: the art direction's deep teal.
var colLoopyMe = color.NRGBA{R: 0x0f, G: 0x7a, B: 0x6e, A: 0xff}

// loopyMode is the Loopy space of a window, built the first time it opens.
type loopyMode struct {
	ui     *desktopUI
	view   loopy.View // the last one read, kept up to date by apply
	loaded bool
	load   int // the latest reload: an older answer is dropped

	sidebar, main fyne.CanvasObject
	bigFace       *loopyFace
	status        *canvas.Text
	waiting       *fyne.Container
	tasks         *fyne.Container
	routines      *fyne.Container
	memoryRow     *tapRow
	sideList      *fyne.Container // everything under the face, laid out again when a list changes

	feed        *fyne.Container
	rows        map[string]*loopyRow // what is drawn, by message
	order       []string
	scroll      *kineticScroll
	body        *fyne.Container // the conversation, or a page over it
	page        func()          // draws the page over it again, nil for none
	typing      *fyne.Container
	typeFace    *loopyFace
	brand       *brandMark
	typeLabel   *canvas.Text
	problem     *fyne.Container
	prompt      *chatEntry
	files       []string // names in Loopy's folder, to send with the next message
	fileRow     *fyne.Container
	sendBtn     *tapRow
	stopBtn     *tapRow
	modelChip   *tapRow
	contextChip *tapRow
	browserBtn  *tapRow
	browserPane *fyne.Container
	browserTab  fyne.CanvasObject
	browserOn   bool // the user wants the pane while there is a page
	remoteTab   string
	closeRemote context.CancelFunc
	drafts      map[string]*loopyDraft // what is typed in the cards' fields, by message
}

// loopyRow is a message as drawn.
type loopyRow struct {
	version int64
	task    string // a task card's state when it was drawn
	object  fyne.CanvasObject
	day     fyne.CanvasObject // the line above the first of a day
	bubble  *loopyBubble
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
	mode := ui.loopy
	on := loopySpace && mode != nil
	if on == ui.inLoopy {
		return
	}
	if on {
		ui.oneloopSide, ui.oneloopMain = ui.root.Objects[0], ui.root.Objects[1]
		ui.root.Objects[0], ui.root.Objects[1] = mode.sidebar, mode.main
	} else {
		ui.root.Objects[0], ui.root.Objects[1] = ui.oneloopSide, ui.oneloopMain
	}
	ui.inLoopy = on
	if mode != nil {
		mode.shown(on)
	}
	if on {
		mode.brand.reveal()
	} else if ui.brand != nil {
		ui.brand.reveal()
	}
	ui.loopyShown = on
	ui.save()
	ui.root.Refresh()
	if on {
		ui.window.Canvas().Focus(mode.prompt)
	}
}

// spaceSwitch is the logo at the top of the sidebar, with its menu: 1loop
// or Loopy, as ChatGPT or Codex in OpenAI's app.
func (ui *desktopUI) spaceSwitch(mark fyne.CanvasObject) fyne.CanvasObject {
	chevron := canvas.NewImageFromResource(tinted(iconChevron, colMuted))
	chevron.SetMinSize(fyne.NewSquareSize(14))
	row := container.New(layout.NewCustomPaddedHBoxLayout(6), container.NewCenter(mark), container.NewCenter(chevron))
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
	mode := &loopyMode{ui: ui, rows: map[string]*loopyRow{}, drafts: map[string]*loopyDraft{}}
	mode.buildSidebar()
	mode.buildMain()
	mode.renderSide()
	mode.reload()
	return mode
}

// do asks something of Loopy, off the interface's thread; what goes wrong
// is said over the composer, then is called (nil for none).
func (mode *loopyMode) do(ask func(ctx context.Context, client loopy.Client) error, then func(err error)) {
	at := mode.ui.loopyServer()
	go func() {
		ctx, cancel := context.WithTimeout(mode.ui.ctx, 80*time.Second)
		defer cancel()
		client, err := mode.ui.loopyClient(at)
		if err == nil {
			err = ask(ctx, client)
		}
		if mode.ui.ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if err != nil {
				mode.say(err.Error())
			}
			if then != nil {
				then(err)
			}
		})
	}()
}

// action is a change asked for from a page, after which it is drawn again.
func (mode *loopyMode) action(action loopy.Action, then func(err error)) {
	mode.do(func(ctx context.Context, client loopy.Client) error { return client.Do(ctx, action) }, func(err error) {
		mode.reload()
		if then != nil {
			then(err)
		}
	})
}

// say shows a problem over the composer, or takes it away ("").
func (mode *loopyMode) say(text string) {
	mode.problem.Objects = nil
	if text != "" {
		close := newRow("", iconClose, styleChip, func() { mode.say("") })
		mode.problem.Add(container.NewBorder(nil, nil, nil, container.NewCenter(close), plainFlow(text, 12.5, colDanger)))
	}
	mode.problem.Refresh()
	mode.main.Refresh()
}

// reload reads the whole view again.
func (mode *loopyMode) reload() {
	mode.load++
	ticket := mode.load
	at := mode.ui.loopyServer()
	go func() {
		ctx, cancel := context.WithTimeout(mode.ui.ctx, 30*time.Second)
		defer cancel()
		var view loopy.View
		client, err := mode.ui.loopyClient(at)
		if err == nil {
			view, err = client.View(ctx)
		}
		if mode.ui.ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if ticket != mode.load || at != mode.ui.loopyServer() {
				return
			}
			if err != nil {
				mode.say("Loopy cannot be reached: " + err.Error())
				mode.face("blocked", "Out of reach")
				return
			}
			first := !mode.loaded
			mode.loaded = true
			old := mode.view
			mode.view = view
			if old.Host != view.Host { // another Loopy
				mode.rows, mode.order, mode.drafts = map[string]*loopyRow{}, nil, map[string]*loopyDraft{}
			}
			mode.renderFeed(first || old.Host != view.Host)
			mode.renderSide()
			if !first || !mode.ui.inLoopy {
				mode.show()
			}
			if mode.page != nil {
				mode.page()
			}
			for _, window := range mode.ui.windows { // Settings → Loopy
				if window.prefs != nil && window.prefs.page == loopySettingsPage && window == mode.ui && window.loopySettingsShown != nil {
					window.loopySettingsShown()
				}
			}
		})
	}()
}

// apply shows a change.
func (mode *loopyMode) apply(change loopy.Change) {
	if !mode.loaded || change.Version < 0 {
		mode.reload()
		return
	}
	for _, message := range change.Messages {
		at := slices.IndexFunc(mode.view.Messages, func(m loopy.Message) bool { return m.ID == message.ID })
		if at >= 0 {
			if message.Version >= mode.view.Messages[at].Version {
				mode.view.Messages[at] = message
			}
		} else {
			mode.view.Messages = append(mode.view.Messages, message)
		}
	}
	if len(change.Gone) > 0 {
		mode.view.Messages = slices.DeleteFunc(mode.view.Messages, func(m loopy.Message) bool { return slices.Contains(change.Gone, m.ID) })
	}
	mode.view.Version = max(mode.view.Version, change.Version)
	mode.view.State, mode.view.Label, mode.view.Running = change.State, change.Label, change.Running
	if change.Used > 0 {
		mode.view.Used = change.Used
	}
	if change.Window > 0 {
		mode.view.Window = change.Window
	}
	if len(change.Messages)+len(change.Gone) > 0 {
		mode.renderFeed(false)
	}
	mode.show()
	if change.All {
		mode.reloadSoon()
	}
}

// reloadSoon reads the rest of the view once the changes settle.
func (mode *loopyMode) reloadSoon() {
	mode.load++
	ticket := mode.load
	time.AfterFunc(150*time.Millisecond, func() {
		fyne.Do(func() {
			if ticket == mode.load {
				mode.reload()
			}
		})
	})
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
	if on && !mode.view.Running {
		mode.bigFace.set("hello") // it greets the user each time they come
		time.AfterFunc(3200*time.Millisecond, func() { fyne.Do(mode.show) })
	}
}

// show is the face and the words for what Loopy does.
func (mode *loopyMode) show() {
	state, label := mode.view.State, mode.view.Label
	if state == "" {
		state = "idle"
	}
	if state == "idle" {
		switch hour := time.Now().Hour(); {
		case len(mode.view.Waiting) > 0:
			state, label = "waiting", "Waiting for you"
		case hour >= 23 || hour < 7:
			state, label = "sleep", "Watching quietly"
		default:
			label = "Here when you need me"
		}
	}
	mode.face(state, label)
	if mode.view.Running {
		mode.typing.Show()
		mode.typeLabel.Text = label + "…"
		mode.typeLabel.Refresh()
		mode.stopBtn.Show()
	} else {
		mode.typing.Hide()
		mode.stopBtn.Hide()
	}
	mode.syncContext()
}

// face moves both faces and says what Loopy is doing.
func (mode *loopyMode) face(state, text string) {
	mode.bigFace.set(state)
	mode.typeFace.set(state)
	if mode.status.Text != text {
		mode.status.Text = text
		mode.status.Refresh()
	}
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
	files := newRow("My folder", iconFolder, styleNav, func() { mode.openFiles("/") })
	summary := newRow("Sum up now", iconSun, styleNav, func() { mode.closePage(); mode.action(loopy.Action{Do: "summary"}, nil) })
	fresh := newRow("Start fresh", iconPlus, styleNav, mode.askClear)
	mode.sideList = container.NewVBox(hero,
		sectionHeader("Waiting for you", nil), mode.waiting,
		sectionHeader("Delegated tasks", nil), mode.tasks,
		sectionHeader("Routines", nil), mode.routines,
		sectionHeader("Loopy", nil), mode.memoryRow, files, summary, fresh)

	mode.brand = newBrandMark(true)
	collapse := newRow("", iconSidebar, styleWindow, ui.toggleSidebar)
	var brand fyne.CanvasObject = padded(10, 12, 0, 0, container.NewBorder(nil, nil, ui.spaceSwitch(mode.brand.object()), container.NewCenter(collapse)))
	if ui.chrome {
		brand = container.NewStack(newDragArea(ui.window), brand)
	}
	divider := canvas.NewRectangle(colBorderSoft)
	divider.SetMinSize(fyne.NewSize(0, 1))
	settings := newRow("Settings", theme.SettingsIcon(), styleNav, func() { mode.openSettings() })
	bottom := container.NewVBox(divider, padded(6, 8, 0, 0, settings))
	content := padded(0, 0, 12, 12, container.NewBorder(brand, bottom, nil, nil, newKineticVScroll(mode.sideList)))
	mode.sidebar = container.NewStack(canvas.NewRectangle(colSidebar), content)
}

func (mode *loopyMode) openSettings() {
	mode.ui.showSpace(false)
	mode.ui.settings()
	mode.ui.prefs.openOn(loopySettingsPage)
}

func (mode *loopyMode) buildMain() {
	ui := mode.ui
	mode.feed = container.NewVBox()
	mode.scroll = newKineticVScroll(container.New(columnLayout{width: 720, inset: 24}, padded(18, 18, 0, 0, mode.feed)))
	mode.typeFace = newLoopyFace(22, "idle", false, false)
	mode.typeLabel = textLine("", 13, colMuted)
	mode.typing = container.NewHBox(container.NewCenter(mode.typeFace.image), container.NewCenter(mode.typeLabel))
	mode.typing.Hide()
	mode.problem = container.NewVBox()

	mode.prompt = newChatEntry()
	mode.prompt.SetPlaceHolder("Message Loopy…")
	mode.prompt.send = mode.submit
	mode.prompt.paste = func() bool { return false } // the entry pastes text itself
	mode.prompt.resized = func() {
		if mode.main != nil {
			mode.main.Refresh()
		}
	}
	attach := newIconButton(iconPlus, overlay(0x0d), color.NRGBA{}, mode.attach)
	attach.iconPx, attach.padPx = 18, 6
	mode.sendBtn = newIconButton(iconSend, colAmber, colInk, mode.submit)
	mode.sendBtn.iconPx = 20
	mode.stopBtn = newIconButton(iconStop, colText, colBackground, func() {
		mode.do(func(ctx context.Context, client loopy.Client) error { return client.Stop(ctx) }, nil)
	})
	mode.stopBtn.onHover = func(in bool) {
		if in {
			ui.tips.show(mode.stopBtn, "Stop what Loopy does")
		} else {
			ui.tips.hide()
		}
	}
	mode.stopBtn.Hide()
	// The field has no fill of its own: the card is its only background.
	input := container.NewThemeOverride(mode.prompt, bareInputTheme{loopTheme{theme.DefaultTheme()}})
	mode.fileRow = container.NewHBox()
	mode.fileRow.Hide()
	line := container.NewBorder(nil, nil, container.NewCenter(attach), container.NewHBox(container.NewCenter(mode.stopBtn), container.NewCenter(mode.sendBtn)), input)
	composer := surface(padded(6, 6, 8, 8, container.NewVBox(mode.fileRow, line)), colSurface, colBorder, 20, 0)
	footer := container.New(columnLayout{width: 720, inset: 24}, container.NewVBox(mode.problem, padded(0, 6, 4, 0, mode.typing), padded(0, 16, 0, 0, composer)))

	mode.body = container.NewStack(mode.scroll)
	mode.modelChip = newRow("Model", nil, styleChip, mode.chooseModel)
	mode.contextChip = newRow("", nil, styleChip, mode.openSettings)
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

// submit sends what the user typed.
func (mode *loopyMode) submit() {
	text := strings.TrimSpace(mode.prompt.Text)
	files := mode.files
	if text == "" && len(files) == 0 {
		return
	}
	mode.prompt.SetText("")
	mode.files = nil
	mode.renderFiles()
	mode.closePage()
	mode.say("")
	if !mode.view.Running {
		mode.bigFace.set("ack")
	}
	mode.do(func(ctx context.Context, client loopy.Client) error { return client.Send(ctx, text, files) }, func(err error) {
		if err != nil { // nothing is lost
			if mode.prompt.Text == "" {
				mode.prompt.SetText(text)
			}
			mode.files = append(files, mode.files...)
			mode.renderFiles()
		}
	})
}

// attach puts files in Loopy's inbox, to go with the next message.
func (mode *loopyMode) attach() {
	mode.ui.pick(pickRequest{title: "Give files to Loopy", multiple: true}, func(paths []string) { mode.give(paths) })
}

func (mode *loopyMode) give(paths []string) {
	for _, name := range paths {
		var at string
		mode.do(func(ctx context.Context, client loopy.Client) error {
			info, err := os.Stat(name)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > 32<<20 {
				return fmt.Errorf("%s is not a file of 32 MB at most", filepath.Base(name))
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			at, err = client.Write(ctx, "/inbox", filepath.Base(name), data)
			return err
		}, func(err error) {
			if err == nil {
				mode.files = append(mode.files, at)
				mode.renderFiles()
			}
		})
	}
}

func (mode *loopyMode) renderFiles() {
	mode.fileRow.Objects = nil
	for _, name := range mode.files {
		mode.fileRow.Add(newRow(path.Base(name)+"  ×", iconFolder, styleChip, func() {
			mode.files = slices.DeleteFunc(mode.files, func(f string) bool { return f == name })
			mode.renderFiles()
		}))
	}
	if len(mode.files) > 0 {
		mode.fileRow.Show()
	} else {
		mode.fileRow.Hide()
	}
	mode.main.Refresh()
}

// ---------------------------------------------------------------------------
// The conversation.

// renderFeed draws what changed: a message keeps what is drawn for it
// until it changes.
func (mode *loopyMode) renderFeed(end bool) {
	end = end || mode.atEnd()
	messages := mode.view.Messages
	var objects []fyne.CanvasObject
	if mode.view.More {
		objects = append(objects, container.NewCenter(newRow("Earlier messages", nil, styleChip, mode.earlier)))
	}
	if len(messages) == 0 && mode.loaded {
		objects = append(objects, mode.welcome()...)
	}
	var day time.Time
	seen := map[string]bool{}
	for _, message := range messages {
		seen[message.ID] = true
		local := message.Time.Local()
		newDay := day.IsZero() || local.YearDay() != day.YearDay() || local.Year() != day.Year()
		day = local
		row := mode.rows[message.ID]
		task := mode.taskKey(message)
		switch {
		case row == nil:
			row = &loopyRow{}
			mode.rows[message.ID] = row
			mode.draw(row, message)
		case row.bubble != nil && message.Version != row.version && !message.Pending && row.bubble.mine == (message.From == "me") && row.bubble.plain(message):
			row.bubble.setText(message.Text) // only the bubble being written changes
			row.version = message.Version
		case row.version != message.Version || row.task != task:
			mode.draw(row, message)
		}
		row.task = task
		if newDay {
			if row.day == nil {
				row.day = loopyDay(local)
			}
			objects = append(objects, row.day)
		}
		objects = append(objects, row.object)
	}
	for id := range mode.rows {
		if !seen[id] {
			delete(mode.rows, id)
			delete(mode.drafts, id)
		}
	}
	mode.feed.Objects = objects
	mode.feed.Refresh()
	if end {
		mode.toEnd()
	}
}

// taskKey says what a task's card shows besides its message.
func (mode *loopyMode) taskKey(message loopy.Message) string {
	if message.Card == nil || message.Card.Task == "" || message.Card.Kind != "delegate" {
		return ""
	}
	if task, ok := mode.task(message.Card.Task); ok {
		return fmt.Sprint(task.Status, len(task.Steps), task.PRs)
	}
	return ""
}

func (mode *loopyMode) task(id string) (loopy.Task, bool) {
	for _, task := range mode.view.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return loopy.Task{}, false
}

// welcome is the conversation before its first message.
func (mode *loopyMode) welcome() []fyne.CanvasObject {
	var out []fyne.CanvasObject
	for _, text := range []string{"Hi, I'm Loopy. I take care of the everyday things: mail, errands, reminders, and the coding tasks I can hand to an agent in your projects. I always ask before anything that matters.",
		"Tell me what you need, and how you like things: I learn your ways as we talk."} {
		out = append(out, padded(3, 3, 0, 0, newLoopyBubble(text, false, "").row))
	}
	return out
}

// earlier reads the messages before the first one.
func (mode *loopyMode) earlier() {
	if len(mode.view.Messages) == 0 {
		return
	}
	first := mode.view.Messages[0].ID
	var older []loopy.Message
	var more bool
	mode.do(func(ctx context.Context, client loopy.Client) (err error) {
		older, more, err = client.Before(ctx, first)
		return err
	}, func(err error) {
		if err == nil && len(mode.view.Messages) > 0 && mode.view.Messages[0].ID == first {
			mode.view.Messages, mode.view.More = append(older, mode.view.Messages...), more
			mode.renderFeed(false)
		}
	})
}

// atEnd says whether the latest message is on screen.
func (mode *loopyMode) atEnd() bool {
	return mode.scroll.Content == nil || mode.scroll.Content.Size().Height-mode.scroll.Size().Height-mode.scroll.Offset.Y < 60
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

// draw makes what a message is drawn with: a bubble, a note or a card.
func (mode *loopyMode) draw(row *loopyRow, message loopy.Message) {
	row.version, row.bubble = message.Version, nil
	switch {
	case message.Card != nil:
		row.object = padded(4, 4, 34, 0, mode.cardView(message))
	case message.Memory != "":
		row.object = mode.memoryNote(message)
	case message.Note != "":
		row.object = padded(2, 6, 34, 0, plainFlow(message.Note, 12, colFaint))
	case message.From == "me":
		text := message.Text
		for _, name := range message.Files {
			text = strings.TrimSpace(text + "\n📎 " + path.Base(name))
		}
		row.bubble = newLoopyBubble(text, true, message.Reaction)
		if message.Pending {
			row.bubble.dim()
		}
		row.object = padded(3, 3, 0, 0, row.bubble.row)
	case loopyMarkup(message.Text):
		row.object = padded(3, 3, 34, 0, newMarkdownView(message.Text).box)
	default:
		row.bubble = newLoopyBubble(message.Text, false, message.Reaction)
		row.bubble.onMenu = func() { mode.reactMenu(message, row.bubble) }
		row.object = padded(3, 3, 0, 0, row.bubble.row)
	}
}

// loopyMarkup says whether a text needs more than a bubble.
func loopyMarkup(text string) bool {
	return strings.Contains(text, "```") || strings.Contains(text, "\n- ") || strings.HasPrefix(text, "- ") || strings.Contains(text, "\n|") || strings.Contains(text, "\n1. ") || strings.HasPrefix(text, "1. ") || strings.Contains(text, "**") || strings.Contains(text, "](http")
}

// memoryNote is "I'll remember: …" under Loopy's bubbles, with Forget.
func (mode *loopyMode) memoryNote(message loopy.Message) fyne.CanvasObject {
	pin := canvas.NewImageFromResource(tinted(iconPin, colAmber))
	pin.SetMinSize(fyne.NewSquareSize(14))
	var right fyne.CanvasObject
	if message.MemoryID != "" {
		right = container.NewCenter(newRow("Forget", nil, styleChip, func() {
			mode.action(loopy.Action{Do: "fact.forget", ID: message.MemoryID}, nil)
		}))
	}
	return padded(2, 6, 34, 0, container.New(&maxWidthLayout{width: 600}, container.NewBorder(nil, nil, container.NewCenter(pin), right, plainFlow(message.Memory, 12.5, colMuted))))
}

// reactMenu lets the user put a reaction on one of Loopy's messages.
func (mode *loopyMode) reactMenu(message loopy.Message, bubble *loopyBubble) {
	var actions []menuAction
	for _, reaction := range loopy.Reactions {
		actions = append(actions, menuAction{label: reaction.Emoji + "  " + strings.ReplaceAll(reaction.Name, "_", " "), checked: message.Reaction == reaction.Name, run: func() {
			mode.do(func(ctx context.Context, client loopy.Client) error {
				return client.React(ctx, message.ID, reaction.Name)
			}, nil)
		}})
	}
	actions = append(actions, menuAction{label: "Copy", icon: iconCopy, run: func() { fyne.CurrentApp().Clipboard().SetContent(message.Text) }})
	showMenu(mode.ui.window, bubble.box, actions)
}

// askClear starts a fresh conversation; what Loopy remembers stays.
func (mode *loopyMode) askClear() {
	mode.ui.confirm("Start fresh?", "Loopy forgets this conversation. What it remembers of you, its folder, its tasks and its routines stay.", "Start fresh", func() {
		mode.do(func(ctx context.Context, client loopy.Client) error { return client.Clear(ctx) }, func(error) { mode.reload() })
	})
}

// chooseModel picks Loopy's model among those of where it lives.
func (mode *loopyMode) chooseModel() {
	settings := mode.view.Settings
	set := func(connection, model string) {
		settings.Connection, settings.Model = connection, model
		if model == "" {
			settings.Effort = ""
		}
		mode.set(settings)
	}
	actions := []menuAction{{label: "As in 1loop", detail: "The model chosen for your projects", checked: settings.Model == "", run: func() { set("", "") }}}
	for _, model := range mode.view.Models {
		actions = append(actions, menuAction{label: model.Label, checked: model.ID == settings.Model && model.Connection == settings.Connection, run: func() { set(model.Connection, model.ID) }})
	}
	showMenu(mode.ui.window, mode.modelChip, actions)
}

// set changes Loopy's settings.
func (mode *loopyMode) set(settings loopy.Settings) {
	mode.view.Settings = settings
	mode.do(func(ctx context.Context, client loopy.Client) error { return client.Set(ctx, settings) }, func(error) { mode.reload() })
}

// ---------------------------------------------------------------------------
// The sidebar's lists.

// renderSide lists what waits for the user, the tasks and the routines.
func (mode *loopyMode) renderSide() {
	view := mode.view
	mode.waiting.Objects = nil
	for _, id := range view.Waiting {
		for _, message := range view.Messages {
			if message.ID == id && message.Card != nil {
				mode.waiting.Add(newRow(message.Card.Title, loopyCardIcon(message.Card.Kind), styleNav, func() { mode.scrollTo(id) }))
			}
		}
	}
	if len(mode.waiting.Objects) == 0 {
		mode.waiting.Add(padded(0, 6, 10, 0, textLine("Nothing right now", 12.5, colFaint)))
	}

	mode.tasks.Objects = nil
	for i, task := range view.Tasks {
		if i == 8 {
			break
		}
		row := newRow(task.Title, iconCode, styleNav, func() { mode.openTask(task) })
		row.trailing = map[string]string{"running": "working", "waiting": "needs you"}[task.Status]
		if row.trailing == "" {
			row.trailing = task.Status
		}
		mode.tasks.Add(row)
	}
	if len(mode.tasks.Objects) == 0 {
		mode.tasks.Add(padded(0, 6, 10, 0, textLine("None yet", 12.5, colFaint)))
	}

	mode.routines.Objects = nil
	for _, routine := range view.Routines {
		row := newRow(routine.Title, iconClock, styleNav, func() { mode.openRoutine(routine.ID) })
		row.trailing = "off"
		if routine.On {
			row.trailing = "on"
		}
		mode.routines.Add(row)
	}
	if len(mode.routines.Objects) == 0 {
		mode.routines.Add(padded(0, 6, 10, 0, textLine("Ask Loopy to set one up", 12.5, colFaint)))
	}
	if mode.loaded {
		mode.memoryRow.SetTrailing(fmt.Sprint(view.Facts))
	}
	name := view.Model
	if view.Settings.Model != "" {
		for _, model := range view.Models {
			if model.ID == view.Settings.Model && model.Connection == view.Settings.Connection {
				name = model.Label
			}
		}
	}
	if name == "" {
		name = "Choose a model"
	}
	mode.modelChip.SetText(name)
	mode.sideList.Refresh()
	mode.syncContext()
	mode.syncBrowser()
}

// scrollTo brings a message on screen.
func (mode *loopyMode) scrollTo(id string) {
	mode.closePage()
	row := mode.rows[id]
	if row == nil {
		mode.toEnd()
		return
	}
	fyne.Do(func() {
		mode.scroll.ScrollToOffset(fyne.NewPos(0, max(0, row.object.Position().Y-60)))
	})
}

// openTask shows the task's chat in 1loop.
func (mode *loopyMode) openTask(task loopy.Task) {
	ui := mode.ui
	at := ui.loopyServer()
	if task.Chat != "" && at == "" {
		for _, chat := range ui.chats {
			if chat.ID == task.Chat {
				ui.showSpace(false)
				ui.openChat(chat)
				return
			}
		}
	}
	if task.Chat != "" && at != "" {
		for _, address := range loadServerAddresses() {
			if address.URL == at {
				ui.showSpace(false)
				ui.openServerChat(address, task.Chat)
			}
		}
		return
	}
	for _, message := range mode.view.Messages {
		if message.Card != nil && message.Card.Task == task.ID {
			mode.scrollTo(message.ID)
			return
		}
	}
}

// openPR shows a pull request in 1loop's panel.
func (mode *loopyMode) openPR(ref string) {
	repo, number, ok := strings.Cut(ref, "#")
	if !ok {
		return
	}
	if address, err := url.Parse("https://github.com/" + repo + "/pull/" + number); err == nil {
		fyne.CurrentApp().OpenURL(address)
	}
}

// closePage takes a page off the conversation.
func (mode *loopyMode) closePage() {
	if mode.page == nil {
		return
	}
	mode.page = nil
	mode.body.Objects = []fyne.CanvasObject{mode.scroll}
	mode.body.Refresh()
}

// openPage puts a page over the conversation; draw is called again when
// what it shows changes.
func (mode *loopyMode) openPage(draw func()) {
	mode.page = draw
	draw()
}

// putPage shows a page's content.
func (mode *loopyMode) putPage(content fyne.CanvasObject) {
	offset := fyne.NewPos(0, 0)
	if len(mode.body.Objects) == 2 {
		if stack, ok := mode.body.Objects[1].(*fyne.Container); ok && len(stack.Objects) == 2 {
			if old, ok := stack.Objects[1].(*kineticScroll); ok {
				offset = old.Offset
			}
		}
	}
	scroll := newKineticVScroll(container.New(columnLayout{width: 720, inset: 24}, padded(18, 24, 0, 0, content)))
	mode.body.Objects = []fyne.CanvasObject{mode.scroll, container.NewStack(canvas.NewRectangle(colBackground), scroll)}
	mode.body.Refresh()
	if offset.Y > 0 {
		scroll.ScrollToOffset(offset)
	}
}

// syncContext says how full Loopy's context is.
func (mode *loopyMode) syncContext() {
	if mode.view.Window <= 0 || mode.view.Used <= 0 {
		mode.contextChip.Hide()
		return
	}
	mode.contextChip.SetText(fmt.Sprintf("Context %d%%", min(100, mode.view.Used*100/mode.view.Window)))
	mode.contextChip.Show()
}

// syncBrowser shows Loopy's browser beside the conversation, live: the view
// a project chat's side panel has, which the user takes over there.
func (mode *loopyMode) syncBrowser() {
	if mode.ui.loopyServer() != "" {
		mode.syncRemoteBrowser()
		return
	}
	var view fyne.CanvasObject
	var page *browser.Page
	if chat := mode.ui.loopyHome.chat; chat != nil && mode.ui.panel != nil {
		state := mode.ui.panel.stateOf(chat)
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
	}
	mode.showBrowser(view)
}

// showBrowser puts a browser's view in the pane, nil for none.
func (mode *loopyMode) showBrowser(view fyne.CanvasObject) {
	if view == nil {
		mode.browserBtn.Hide()
		mode.browserPane.Hide()
		mode.browserTab = nil
		return
	}
	if mode.browserTab == nil {
		mode.browserOn = true // the first page opens the pane; the user may close it
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

// syncRemoteBrowser is the same for a Loopy on a server: its page is a tab
// of the server's hidden "loopy" chat.
func (mode *loopyMode) syncRemoteBrowser() {
	if !mode.view.Browser {
		if mode.closeRemote != nil {
			mode.closeRemote()
			mode.closeRemote, mode.remoteTab = nil, ""
		}
		mode.showBrowser(nil)
		return
	}
	at := mode.ui.loopyServer()
	go func() {
		client, err := mode.ui.loopyRemote(at)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(mode.ui.ctx, 15*time.Second)
		defer cancel()
		var panel struct{ Tabs []remoteapi.Tab }
		if client.json(ctx, "GET", "/api/chats/"+loopyChatID+"/panel", nil, &panel) != nil {
			return
		}
		fyne.Do(func() {
			for _, tab := range slices.Backward(panel.Tabs) {
				if tab.Kind != "browser" {
					continue
				}
				if tab.Key != mode.remoteTab {
					if mode.closeRemote != nil {
						mode.closeRemote()
					}
					status := widget.NewLabel("")
					remote := &remoteDesktop{parent: mode.ui, client: client, ctx: mode.ui.ctx, window: mode.ui.window, current: loopyChatID, status: status}
					content, closeTab, active := remote.panelContent(tab)
					active(true)
					mode.remoteTab, mode.closeRemote = tab.Key, closeTab
					mode.showBrowser(content)
				} else {
					mode.showBrowser(mode.browserTab)
				}
				return
			}
			mode.showBrowser(nil)
		})
	}()
}

// loopySplit puts the browser pane on the right, about half the width, when
// it shows.
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
