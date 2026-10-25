package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/browser"
)

// toolPanel is the foldable panel on the right of the conversation. Each
// tool call opened from the transcript gets a tab showing the call itself
// (command, path, diff), its full output and how long it ran.
type toolPanel struct {
	ui     *desktopUI
	view   fyne.CanvasObject
	strip  *fyne.Container
	body   *kineticScroll
	page   *fyne.Container
	texts  []*outputView // the code wells of the call on display
	tabs   []panelTab
	active int
	owner  *conversation // the chat whose panel this is now; nil on the start screen
	home   panelState    // the start screen's own panel
	width  float32       // this chat's width, 0 for the default one
	shown  float32       // 0 folded .. 1 open, eased by slide
	target float32       // where the slide is going
	snap   bool          // the conversation is too long to follow the slide frame by frame
	anim   *fyne.Animation
}

type panelTab struct {
	kind            string // "tool", "todo", "changes", "files", "terminal" or "browser": what to open again with the chat
	chat            *conversation
	index           int
	title           string
	icon            fyne.Resource
	content         fyne.CanvasObject
	browser         *browser.Page
	terminal        *panelTerminal
	cancel          func()
	todoChat        *conversation
	todoView        *fyne.Container
	todoContext     *fyne.Container
	todoContextText string
	todoTimes       map[string]*canvas.Text
	todoMarks       []*taskMark // the rings of the list as drawn, stopped when it is drawn again
	web             bool
	webKey          string // the ChatGPT page shown: its chat's
	webAccount      *agent.ChatGPTWeb
	workspace       string // manual tabs belong to a workspace, not a transcript item
}

const panelWidth = 840

func newToolPanel(ui *desktopUI) *toolPanel {
	panel := &toolPanel{ui: ui, active: -1}
	go func() {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ui.ctx.Done():
				return
			case <-timer.C:
				fyne.Do(func() {
					if ui.ctx.Err() == nil {
						panel.refreshTodoStatus()
					}
				})
			}
		}
	}()
	panel.strip = container.New(layout.NewCustomPaddedHBoxLayout(4))
	add := newRow("", iconPlus, styleWindow, nil)
	add.onTap = func() {
		showMenu(ui.window, add, []menuAction{
			{label: "Browser", icon: iconBrowser, run: panel.openBrowser},
			{label: "Terminal", icon: toolGlyph("shell"), run: panel.openTerminal},
			{label: "Git changes", icon: toolGlyph("edit_file"), run: panel.openChanges},
			{label: "Files", icon: iconFolder, run: panel.openFiles},
		})
	}
	fold := newRow("", iconPanel, styleWindow, panel.fold)
	todo := newRow("Tasks", iconCheck, styleChip, panel.openTodo)
	header := container.NewBorder(nil, nil, nil, container.NewHBox(todo, add, fold), container.NewHScroll(panel.strip))
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	panel.body = newKineticVScroll(spacer(0, 0))
	panel.body.OnScrolled = func(fyne.Position) { panel.place() }
	panel.page = container.NewStack(panel.body)
	card := canvas.NewRectangle(colSurface)
	card.CornerRadius = 12
	card.StrokeColor = colBorderSoft
	card.StrokeWidth = 1
	content := container.NewBorder(container.NewVBox(padded(8, 8, 8, 6, header), rule), nil, nil, nil, panel.page)
	// The panel goes nearly to the window's edges: only a thin margin, which
	// also leaves the window's own resize border within reach.
	panel.view = padded(0, 6, 0, 6, container.NewStack(card, content))
	panel.render()
	return panel
}

// splitLayout gives the panel its slice of the width as it slides in.
type splitLayout struct{ toolPanel *toolPanel }

func (layoutSpec splitLayout) width(total float32) float32 {
	width := layoutSpec.toolPanel.width
	if width <= 0 {
		width = layoutSpec.toolPanel.ui.panelWidth // the last width chosen anywhere
	}
	if width <= 0 {
		width = fyne.Min(panelWidth, total*0.58)
	}
	return panelWidthWithin(width, total)
}

func panelWidthWithin(width, total float32) float32 {
	return fyne.Max(320, fyne.Min(width, total-360))
}

func (layoutSpec splitLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (layoutSpec splitLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	full := layoutSpec.width(size.Width)
	width := full * layoutSpec.toolPanel.shown
	conversationWidth := size.Width - width
	if layoutSpec.toolPanel.snap {
		conversationWidth = size.Width - full*layoutSpec.toolPanel.target
	}
	layoutSpec.toolPanel.ui.controls.Layout.(*composerControlsLayout).available = fyne.Min(760, conversationWidth-48) - 20
	if layoutSpec.toolPanel.snap {
		// Wrapping a long conversation again on every frame is what made
		// the slide stutter: it takes its final width at once instead.
		objects[0].Resize(fyne.NewSize(size.Width-full*layoutSpec.toolPanel.target, size.Height))
	} else {
		objects[0].Resize(fyne.NewSize(size.Width-width, size.Height))
	}
	// Folded, the panel waits just past the right edge of the window.
	objects[1].Move(fyne.NewPos(size.Width-width, 0))
	objects[1].Resize(fyne.NewSize(full, size.Height))
	objects[2].Move(fyne.NewPos(size.Width-width-4, 0))
	objects[2].Resize(fyne.NewSize(8, size.Height-6))
	if layoutSpec.toolPanel.target > 0 {
		objects[2].Show()
	} else {
		objects[2].Hide()
	}
}

type panelResizeHandle struct {
	widget.BaseWidget
	panel *toolPanel
	grip  *canvas.Rectangle
}

func newPanelResizeHandle(panel *toolPanel) *panelResizeHandle {
	handle := &panelResizeHandle{panel: panel, grip: canvas.NewRectangle(colBorderSoft)}
	handle.grip.CornerRadius = 1
	handle.grip.SetMinSize(fyne.NewSize(2, 40))
	handle.ExtendBaseWidget(handle)
	return handle
}

func (handle *panelResizeHandle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewCenter(handle.grip))
}

func (handle *panelResizeHandle) Cursor() desktop.Cursor { return desktop.HResizeCursor }
func (handle *panelResizeHandle) MouseIn(*desktop.MouseEvent) {
	handle.grip.FillColor = colAmber
	handle.grip.Refresh()
}

func (handle *panelResizeHandle) MouseMoved(*desktop.MouseEvent) {}
func (handle *panelResizeHandle) MouseOut() {
	handle.grip.FillColor = colBorderSoft
	handle.grip.Refresh()
}

func (handle *panelResizeHandle) Dragged(e *fyne.DragEvent) {
	panel := handle.panel
	if panel.anim != nil {
		panel.anim.Stop()
	}
	panel.shown, panel.target, panel.snap = 1, 1, false
	total := panel.ui.split.Size().Width
	panel.width = panelWidthWithin((splitLayout{panel}).width(total)-e.Dragged.DX, total)
	panel.ui.panelWidth = panel.width // what a chat without a width of its own takes
	relayout(panel.ui.split)
}

func (handle *panelResizeHandle) DragEnd() { handle.panel.ui.save() }

func (panel *toolPanel) slide(to float32) {
	if panel.anim != nil {
		panel.anim.Stop()
	}
	from := panel.shown
	panel.target, panel.snap = to, false
	panel.syncBrowsers()
	panel.anim = fyne.NewAnimation(240*time.Millisecond, func(t float32) {
		panel.shown = from + (to-from)*t
		started := time.Now()
		relayout(panel.ui.split) // never Refresh: it would redraw the whole conversation on every frame
		if time.Since(started) > 8*time.Millisecond {
			panel.snap = true
		}
		if t >= 1 {
			panel.snap = false
		}
	})
	panel.anim.Curve = easeOutQuint
	panel.anim.Start()
	panel.ui.panelToggle.SetSelected(to > 0)
	panel.syncRows()
	panel.ui.save() // open or folded is part of what the chat remembers
}

func (panel *toolPanel) fold()   { panel.slide(0) }
func (panel *toolPanel) unfold() { panel.slide(1) }

func (panel *toolPanel) find(chat *conversation, index int) int {
	for i, tab := range panel.tabs {
		if tab.chat == chat && tab.index == index {
			return i
		}
	}
	return -1
}

// showing reports whether a call is the one the panel shows.
func (panel *toolPanel) showing(chat *conversation, index int) bool {
	return panel.shown > 0 && panel.active >= 0 && panel.find(chat, index) == panel.active
}

// open shows a call, in its existing tab or a new one.
func (panel *toolPanel) open(chat *conversation, index int) {
	i := panel.find(chat, index)
	if i < 0 {
		panel.tabs = append(panel.tabs, panelTab{kind: "tool", chat: chat, index: index})
		i = len(panel.tabs) - 1
	}
	panel.choose(i)
	if panel.shown < 1 {
		panel.unfold()
	}
}

func (panel *toolPanel) choose(i int) {
	panel.active = i
	panel.render()
	panel.body.ScrollToTop()
	if terminal := panel.tabs[i].terminal; terminal != nil {
		panel.ui.window.Canvas().Focus(terminal.widget)
	}
	panel.ui.save()
}

func (panel *toolPanel) close(i int) {
	if panel.tabs[i].web && panel.ui.cfg.Provider == "chatgptweb" {
		return
	}
	panel.tabs[i].dispose()
	panel.tabs = append(panel.tabs[:i], panel.tabs[i+1:]...)
	switch {
	case len(panel.tabs) == 0:
		panel.active = -1
		panel.render()
		panel.fold()
		return
	case panel.active >= len(panel.tabs) || panel.active > i:
		panel.active--
	}
	panel.render()
	panel.ui.save()
}

func (tab panelTab) dispose() {
	if tab.browser != nil {
		tab.browser.Close()
	}
	if tab.terminal != nil {
		tab.terminal.close()
	}
	if tab.cancel != nil {
		tab.cancel()
	}
}

// dispose ends what every chat's panel runs (terminals, loads), on closing.
func (panel *toolPanel) dispose() {
	panel.stash()
	for _, tab := range panel.home.tabs {
		tab.dispose()
	}
	for _, chat := range panel.ui.chats {
		chat.disposePanel()
	}
	if panel.ui.browser != nil {
		panel.ui.browser.Cancel()
	}
}

// changed refreshes the panel when a call it shows gets its result.
func (panel *toolPanel) changed(chat *conversation, index int) {
	if i := panel.find(chat, index); i >= 0 {
		panel.render()
	}
}

func (panel *toolPanel) renderStrip() {
	panel.strip.RemoveAll()
	for i, tab := range panel.tabs {
		panel.strip.Add(panel.tabView(i, tab))
	}
	panel.strip.Refresh()
}

func (panel *toolPanel) render() {
	panel.renderStrip()
	panel.syncBrowsers()
	panel.texts = nil
	if panel.active >= 0 {
		tab := panel.tabs[panel.active]
		if tab.content != nil {
			panel.page.Objects = []fyne.CanvasObject{tab.content}
		} else {
			item := tab.chat.Items[tab.index]
			if item.Diff != "" {
				panel.page.Objects = []fyne.CanvasObject{container.NewBorder(padded(12, 8, 12, 12, toolDetailHeading(item, runState(tab.chat, tab.index))), nil, nil, nil, newDiffView(item.Diff))}
			} else {
				panel.body.Content = padded(16, 20, 18, 18, panel.details(item, runState(tab.chat, tab.index)))
				panel.page.Objects = []fyne.CanvasObject{panel.body}
			}
		}
	} else {
		panel.page.Objects = []fyne.CanvasObject{padded(28, 20, 20, 20, container.NewVBox(textLine("Side panel", 18, colText), note("Open a browser, terminal or Git changes with the + button, or click a tool call in the conversation.")))}
	}
	panel.page.Refresh()
	panel.body.Refresh()
	panel.place()
	panel.syncRows()
}

// place tells each code well which of its rows the panel shows.
func (panel *toolPanel) place() {
	for _, text := range panel.texts {
		if offset, ok := offsetIn(panel.body.Content, text); ok {
			top := panel.body.Offset.Y - offset.Y
			text.show(top, top+panel.body.Size().Height)
		}
	}
}

// offsetIn is where target sits inside root, through nested containers.
func offsetIn(root, target fyne.CanvasObject) (fyne.Position, bool) {
	if root == target {
		return fyne.Position{}, true
	}
	if c, ok := root.(*fyne.Container); ok {
		for _, object := range c.Objects {
			if pos, found := offsetIn(object, target); found {
				return object.Position().Add(pos), true
			}
		}
	}
	return fyne.Position{}, false
}

// placedLayout lays out like the layout it wraps, then reports it: the
// wells below a section that changed height have moved.
type placedLayout struct {
	inner fyne.Layout
	after func()
}

func (layoutSpec placedLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return layoutSpec.inner.MinSize(objects)
}

func (layoutSpec placedLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	layoutSpec.inner.Layout(objects, size)
	layoutSpec.after()
}

// syncRows highlights the transcript row of the call on display.
func (panel *toolPanel) syncRows() {
	for _, object := range panel.ui.transcript.Objects {
		if row, ok := object.(*toolRow); ok {
			if selected := panel.showing(row.chat, row.index); selected != row.selected {
				row.selected = selected
				row.Refresh()
			}
		}
	}
}

func (panel *toolPanel) tabView(i int, tab panelTab) fyne.CanvasObject {
	target, icon := tab.title, tab.icon
	if tab.chat != nil {
		item := tab.chat.Items[tab.index]
		_, target, _ = agent.DescribeTool(item, false)
		if item.Tool != "shell" {
			target = filepath.Base(target)
		}
		icon = toolGlyph(item.Tool)
	}
	label := newRow(fitText(target, 13, fyne.TextStyle{}, 190), icon, styleChip, func() { panel.choose(i) })
	label.selected = i == panel.active
	label.ink = colText
	closeTab := newIconButton(iconClose, color.NRGBA{}, color.NRGBA{}, func() { panel.close(i) })
	closeTab.iconPx, closeTab.padPx = 14, 6
	if tab.web && panel.ui.cfg.Provider == "chatgptweb" {
		closeTab.Hide()
	}
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 10
	if i == panel.active {
		bg.FillColor = colRaised
		bg.StrokeColor, bg.StrokeWidth = colBorder, 1
	}
	return container.NewStack(bg, padded(4, 4, 5, 4, container.New(layout.NewCustomPaddedHBoxLayout(2), label, container.NewCenter(closeTab))))
}

// details lays out one call: a heading with its outcome, then the call and
// what it returned.
func toolDetailHeading(item agent.ChatItem, state string) fyne.CanvasObject {
	running := state == "pending" || state == "running"
	verb, target, _ := agent.DescribeTool(item, running)
	icon := canvas.NewImageFromResource(tinted(toolGlyph(item.Tool), colText))
	icon.SetMinSize(fyne.NewSquareSize(20))
	title := widget.NewLabel(verb + " " + target)
	if item.Tool == "shell" {
		title.SetText(verb + " a command")
	}
	title.Wrapping = fyne.TextWrapWord
	title.TextStyle = fyne.TextStyle{Bold: true}

	// Outcome line: status, size of the change, timing.
	var facts []fyne.CanvasObject
	fact := func(text string, ink color.NRGBA) { facts = append(facts, textLine(text, 12.5, ink)) }
	switch {
	case state == "pending":
		fact("Awaiting your approval", colAmber)
	case running:
		fact("Running…", colAmber)
	case item.Failed == stoppedCall:
		fact("Stopped before it finished", colMuted)
	case item.Failed != "":
		fact(shortError(item.Failed), colDanger)
	case item.Tool == "shell":
		fact("Exit 0", colAdded)
	default:
		fact("Done", colAdded)
	}
	if item.Diff != "" {
		added, removed := diffStat(item.Diff)
		fact(fmt.Sprintf("+%d", added), colAdded)
		fact(fmt.Sprintf("−%d", removed), colDanger)
	}
	if item.Millis > 0 {
		fact(elapsed(item.Millis), colMuted)
	}
	line := container.New(layout.NewCustomPaddedHBoxLayout(10), facts...)
	heading := container.NewBorder(nil, nil, container.NewVBox(padded(9, 0, 0, 0, icon)), nil,
		container.NewVBox(title, padded(0, 0, 8, 0, line)))

	return heading
}

func (panel *toolPanel) details(item agent.ChatItem, state string) fyne.CanvasObject {
	running := state == "pending" || state == "running"
	_, command, _ := agent.ToolArgs(item.Args)
	sections := container.New(placedLayout{layout.NewVBoxLayout(), panel.place}, toolDetailHeading(item, state))
	add := func(name, text, empty string) {
		sections.Add(spacer(0, 10))
		sections.Add(panel.section(name, text, empty))
	}
	switch item.Tool {
	case "shell":
		add("Command", command, "")
		add("Output", item.Text, outputPlaceholder(running, "No output"))
	case "edit_file", "apply_patch":
		add("Changes", item.Diff, outputPlaceholder(running, "No diff recorded"))
	case "read_file":
		add("Content", item.Text, outputPlaceholder(running, "Empty file"))
	case "list_directory":
		add("Entries", item.Text, outputPlaceholder(running, "Empty directory"))
	default:
		if item.Args != "" {
			add("Arguments", item.Args, "")
		}
		add("Output", item.Text, outputPlaceholder(running, "No output"))
	}
	if item.Failed != "" && item.Failed != stoppedCall {
		add("Error", item.Failed, "")
	}
	for _, part := range item.Images {
		raw, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			continue
		}
		view := canvas.NewImageFromImage(img)
		view.FillMode = canvas.ImageFillContain
		view.SetMinSize(fyne.NewSize(320, 240))
		sections.Add(padded(8, 8, 0, 0, view))
	}
	return sections
}

func outputPlaceholder(running bool, empty string) string {
	if running {
		return "Waiting for the result…"
	}
	return empty
}

// section is a captioned code well with its own copy button.
func (panel *toolPanel) section(name, text, empty string) fyne.CanvasObject {
	caption := textLine(strings.ToUpper(name), 11.5, colFaint)
	caption.TextStyle = fyne.TextStyle{Bold: true}
	if text == "" {
		return container.NewVBox(padded(0, 4, 2, 0, caption), padded(0, 0, 2, 0, textLine(empty, 13, colFaint)))
	}
	var copyButton *tapRow
	copyButton = newRow("Copy", iconCopy, styleChip, func() {
		fyne.CurrentApp().Clipboard().SetContent(text)
		copyButton.SetText("Copied")
		time.AfterFunc(1400*time.Millisecond, func() { fyne.Do(func() { copyButton.SetText("Copy") }) })
	})
	header := container.NewBorder(nil, nil, container.NewCenter(caption), copyButton)
	well := newOutputView(strings.TrimRight(text, "\n"))
	panel.texts = append(panel.texts, well)
	return container.NewVBox(padded(0, 2, 2, 0, header), surface(well, colCode, colBorderSoft, 10, 12))
}

// outputView shows a command, a diff or an output of any length at the cost
// of one screenful. The font is monospace, so lines wrap by column count,
// and only the rows inside the panel's viewport exist as canvas objects.
// (A Label laid every row of a long log out again on each refresh: a 30 000
// character output froze the window while the panel slid in.)
type outputView struct {
	widget.BaseWidget
	lines       []string
	diff        bool
	cols        int
	rows        []outputRow // lines wrapped to cols
	top, bottom float32     // the part of the view on screen
	render      *outputRenderer
}

type outputRow struct {
	text string
	ink  color.Color
}

const outputTextSize = 12.5

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func newOutputView(text string) *outputView {
	text = ansiCodes.ReplaceAllString(strings.ReplaceAll(text, "\r", ""), "")
	view := &outputView{lines: strings.Split(text, "\n"), bottom: 2000,
		diff: strings.HasPrefix(text, "--- ") || strings.HasPrefix(text, "diff --git ")}
	view.wrap(52) // about the panel's width, until the first layout says
	view.ExtendBaseWidget(view)
	return view
}

func outputCell() fyne.Size {
	return textSize("M", outputTextSize, fyne.TextStyle{Monospace: true})
}

func outputRowHeight() float32 { return outputCell().Height + 3 }

// wrap cuts the lines into rows of at most cols characters.
func (view *outputView) wrap(cols int) {
	view.cols = cols
	view.rows = view.rows[:0]
	for _, line := range view.lines {
		var ink color.Color = colText
		if view.diff {
			switch {
			case strings.HasPrefix(line, "+"):
				ink = colAdded
			case strings.HasPrefix(line, "-"):
				ink = colDanger
			case strings.HasPrefix(line, "@@"):
				ink = colFaint
			}
		}
		runes := []rune(expandTabs(line))
		for len(runes) > cols {
			view.rows = append(view.rows, outputRow{string(runes[:cols]), ink})
			runes = runes[cols:]
		}
		view.rows = append(view.rows, outputRow{string(runes), ink})
	}
}

func expandTabs(line string) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	var builder strings.Builder
	column := 0
	for _, r := range line {
		if r != '\t' {
			builder.WriteRune(r)
			column++
			continue
		}
		for n := 4 - column%4; n > 0; n-- {
			builder.WriteByte(' ')
			column++
		}
	}
	return builder.String()
}

// show sets which part of the view is on screen (in its own coordinates).
func (view *outputView) show(top, bottom float32) {
	view.top, view.bottom = top, bottom
	if view.render != nil {
		view.render.place()
	}
}

func (view *outputView) CreateRenderer() fyne.WidgetRenderer {
	view.render = &outputRenderer{view: view}
	return view.render
}

type outputRenderer struct {
	view  *outputView
	texts []*canvas.Text
	owner []int // the row each text shows, -1 when hidden
}

func (renderer *outputRenderer) MinSize() fyne.Size {
	return fyne.NewSize(outputCell().Width*12, float32(len(renderer.view.rows))*outputRowHeight())
}

func (renderer *outputRenderer) Layout(size fyne.Size) {
	if cols := max(12, int(size.Width/outputCell().Width)); cols != renderer.view.cols {
		renderer.view.wrap(cols)
		renderer.clear()
	}
	renderer.place()
}

// clear takes every row off its text: they are all written again.
func (renderer *outputRenderer) clear() {
	for slot := range renderer.owner {
		renderer.owner[slot] = -1
		renderer.texts[slot].Hide()
	}
}

// place gives the rows on screen (and a few around) a text each. A text
// keeps its row while that row stays in range, so scrolling only writes
// the rows that come in.
func (renderer *outputRenderer) place() {
	view, height := renderer.view, outputRowHeight()
	first := max(0, int(view.top/height)-6)
	last := min(len(view.rows)-1, int(view.bottom/height)+6)
	if need := last - first + 1; need > len(renderer.texts) {
		for len(renderer.texts) < need+12 {
			text := canvas.NewText("", colText)
			text.TextSize, text.TextStyle = outputTextSize, fyne.TextStyle{Monospace: true}
			text.Hide()
			renderer.texts = append(renderer.texts, text)
		}
		renderer.owner = make([]int, len(renderer.texts))
		renderer.clear()
	}
	for slot, row := range renderer.owner {
		if row >= 0 && (row < first || row > last) {
			renderer.owner[slot] = -1
			renderer.texts[slot].Hide()
		}
	}
	for row := first; row <= last; row++ {
		slot := row % len(renderer.texts)
		if renderer.owner[slot] == row {
			continue
		}
		renderer.owner[slot] = row
		text := renderer.texts[slot]
		text.Text, text.Color = view.rows[row].text, view.rows[row].ink
		text.Move(fyne.NewPos(0, float32(row)*height))
		text.Resize(fyne.NewSize(view.Size().Width, height))
		text.Show()
		text.Refresh()
	}
}

func (renderer *outputRenderer) Refresh() {
	renderer.clear()
	renderer.place()
}

func (renderer *outputRenderer) Objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, len(renderer.texts))
	for i, text := range renderer.texts {
		objects[i] = text
	}
	return objects
}

func (renderer *outputRenderer) Destroy() {}
