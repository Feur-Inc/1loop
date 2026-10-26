package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/platform"
)

// upgradeItems rewrites tool rows saved by earlier versions, which stored an
// edit as two rows ("View changes", then "edit_file · result") and every
// other call as a bare "<tool> · result" row.
func upgradeItems(items []agent.ChatItem) []agent.ChatItem {
	out := make([]agent.ChatItem, 0, len(items))
	for i := 0; i < len(items); i++ {
		item := items[i]
		switch {
		case item.Kind == "diff":
			item = agent.ChatItem{Kind: "tool", Tool: "edit_file", Diff: item.Text}
			if i+1 < len(items) && items[i+1].Kind == "tool" && items[i+1].Title == "edit_file · result" {
				item.Text = items[i+1].Text
				i++
			}
		case item.Kind == "tool" && item.Tool == "" && item.Title == "Context compacted":
			item = agent.ChatItem{Kind: "note", Text: item.Text}
		case item.Kind == "tool" && item.Tool == "":
			item.Tool = strings.TrimSuffix(item.Title, " · result")
			item.Title = ""
		}
		if item.ID == "" {
			item.ID = core.Identifier()[:16]
		}
		out = append(out, item)
	}
	return out
}

// toolArgs reads the arguments every tool shares.
func toolGlyph(tool string) fyne.Resource {
	if strings.HasPrefix(tool, "browser_") {
		return iconBrowser
	}
	if strings.HasPrefix(tool, "terminal_") {
		return iconTerminal
	}
	if tool == "computer_use" {
		return theme.ComputerIcon()
	}
	switch tool {
	case "shell":
		return iconTerminal
	case "edit_file", "apply_patch":
		return iconPencil
	case "list_directory":
		return iconFolder
	case "update_todo", "TodoWrite", "mcp__1loop_todo__update_todo":
		return iconCheck
	case "recall":
		return iconDocument
	case "checkpoint":
		return iconCompact
	case "memory":
		return iconBookmark
	}
	return iconDocument
}

// describeTool is the one-line summary of a call: "Ran go test ./..." or
// "Editing main.go". The target is the command's first line or the path.
// ---------------------------------------------------------------------------
// toolRow is one tool call in the transcript: what was done and its target
// at a glance (a shell command shows its first line), with the outcome at
// the end. A click opens the call in the side panel.

type toolRow struct {
	widget.BaseWidget
	chat     *conversation
	index    int // of the item in chat.Items
	item     agent.ChatItem
	state    string // "pending" (awaiting approval), "running" or "done"
	selected bool
	onTap    func()
	hover    float32
	anim     *fyne.Animation
	below    *fyne.Container // a citation's card, once the call is done
}

func newToolRow(item agent.ChatItem, state string, onTap func()) *toolRow {
	row := &toolRow{item: item, state: state, onTap: onTap}
	row.ExtendBaseWidget(row)
	return row
}

func (row *toolRow) update(item agent.ChatItem, state string) {
	row.item, row.state = item, state
	row.Refresh()
}

func (row *toolRow) Tapped(*fyne.PointEvent) {
	if row.onTap != nil {
		row.onTap()
	}
}

func (row *toolRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (row *toolRow) MouseIn(*desktop.MouseEvent)    { row.fade(1) }
func (row *toolRow) MouseMoved(*desktop.MouseEvent) {}
func (row *toolRow) MouseOut()                      { row.fade(0) }

func (row *toolRow) fade(to float32) {
	if row.anim != nil {
		row.anim.Stop()
	}
	from := row.hover
	row.anim = fyne.NewAnimation(130*time.Millisecond, func(p float32) {
		row.hover = from + (to-from)*p
		row.Refresh()
	})
	row.anim.Curve = fyne.AnimationEaseOut
	row.anim.Start()
}

func (row *toolRow) CreateRenderer() fyne.WidgetRenderer {
	renderer := &toolRowRenderer{row: row, bg: canvas.NewRectangle(color.Transparent), icon: canvas.NewImageFromResource(nil),
		verb: canvas.NewText("", colMuted), target: canvas.NewText("", colText),
		meta: canvas.NewText("", colFaint), minus: canvas.NewText("", colDanger)}
	renderer.bg.CornerRadius = 8
	renderer.icon.FillMode = canvas.ImageFillContain
	for _, t := range []*canvas.Text{renderer.verb, renderer.target, renderer.meta, renderer.minus} {
		t.TextSize = 13.5
	}
	renderer.meta.TextSize, renderer.minus.TextSize = 12.5, 12.5
	renderer.Refresh()
	return renderer
}

type toolRowRenderer struct {
	row                       *toolRow
	bg                        *canvas.Rectangle
	icon                      *canvas.Image
	verb, target, meta, minus *canvas.Text
	glyph                     fyne.Resource
	full                      string // untruncated target
}

const toolRowPadX, toolRowIcon = float32(8), float32(16)

func (renderer *toolRowRenderer) MinSize() fyne.Size {
	height := textSize("Ag", 13.5, fyne.TextStyle{}).Height + 12
	return fyne.NewSize(120, height)
}

func (renderer *toolRowRenderer) Layout(size fyne.Size) {
	renderer.bg.Resize(size)
	middle := func(t *canvas.Text) float32 { return (size.Height - t.MinSize().Height) / 2 }
	renderer.icon.Resize(fyne.NewSquareSize(toolRowIcon))
	renderer.icon.Move(fyne.NewPos(toolRowPadX, (size.Height-toolRowIcon)/2))
	x := toolRowPadX + toolRowIcon + 8
	renderer.verb.Move(fyne.NewPos(x, middle(renderer.verb)))
	x += renderer.verb.MinSize().Width + 6
	metaWidth := float32(0)
	if renderer.meta.Text != "" {
		metaWidth = renderer.meta.MinSize().Width + 10
	}
	if renderer.minus.Text != "" {
		metaWidth += renderer.minus.MinSize().Width + 5
	}
	renderer.target.Text = fitText(renderer.full, renderer.target.TextSize, renderer.target.TextStyle, size.Width-x-metaWidth-toolRowPadX)
	renderer.target.Move(fyne.NewPos(x, middle(renderer.target)))
	x += renderer.target.MinSize().Width + 10
	renderer.meta.Move(fyne.NewPos(x, middle(renderer.meta)))
	renderer.minus.Move(fyne.NewPos(x+renderer.meta.MinSize().Width+5, middle(renderer.minus)))
}

func (renderer *toolRowRenderer) Refresh() {
	row := renderer.row
	item := row.item
	running := row.state == "pending" || row.state == "running"
	verb, target, mono := agent.DescribeTool(item, running)
	renderer.verb.Text = verb
	renderer.full = target
	renderer.target.TextStyle = fyne.TextStyle{Monospace: mono}
	renderer.target.TextSize = 13.5
	if mono {
		renderer.target.TextSize = 12.5
	}
	renderer.meta.Text, renderer.minus.Text = "", ""
	renderer.meta.Color = colFaint
	switch {
	case row.state == "pending":
		renderer.meta.Text = "Awaiting approval"
		renderer.meta.Color = colAmber
	case running:
	case item.Failed == stoppedCall:
		renderer.meta.Text = "Stopped"
	case item.Failed != "":
		renderer.meta.Text = shortError(item.Failed)
		renderer.meta.Color = colDanger
	case item.Diff != "": // edit_file and apply_patch
		added, removed := diffStat(item.Diff)
		renderer.meta.Text = fmt.Sprintf("+%d", added)
		renderer.meta.Color = colAdded
		renderer.minus.Text = fmt.Sprintf("−%d", removed)
	case item.Tool == "shell" && item.Millis > 0:
		renderer.meta.Text = elapsed(item.Millis)
	}
	h := quantize(row.hover, 4)
	renderer.verb.Color = mix(colMuted, colText, h*0.5)
	renderer.bg.FillColor = mix(color.NRGBA{}, colHover, row.hover)
	if row.selected {
		renderer.bg.FillColor = mix(colSelected, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}, row.hover)
	}
	if glyph := toolGlyph(item.Tool); glyph != renderer.glyph {
		renderer.glyph = glyph
		renderer.icon.Resource = tinted(glyph, colMuted)
		renderer.icon.Refresh()
	}
	renderer.Layout(row.Size())
	for _, object := range renderer.Objects() {
		object.Refresh()
	}
}

// stoppedCall marks a call the user stopped before it returned.
const stoppedCall = "Stopped"

// shortError keeps the transcript row readable; the panel shows it all.
func shortError(text string) string {
	switch {
	case strings.HasSuffix(text, "denied"), strings.Contains(text, "denied this action"):
		return "Denied"
	case strings.HasPrefix(text, "exit status "):
		return "Exit " + strings.TrimPrefix(text, "exit status ")
	case strings.Contains(text, "deadline exceeded"):
		return "Timed out"
	}
	return "Failed"
}

func (renderer *toolRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.bg, renderer.icon, renderer.verb, renderer.target, renderer.meta, renderer.minus}
}

func (renderer *toolRowRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// Transcript

// bubbleLayout sizes a user message to its text and pins it to the right.
// The text's natural width is measured once: a long message made every
// layout measure each of its lines again.
type bubbleLayout struct {
	text    *flowText // nil for a message of images only
	chrom   float32
	natural float32
	known   bool
}

func (layoutSpec *bubbleLayout) width(available float32) float32 {
	if !layoutSpec.known {
		layoutSpec.known = true
		if layoutSpec.text != nil {
			layoutSpec.natural, _ = layoutSpec.text.extent()
		}
	}
	return fyne.Min(layoutSpec.natural+layoutSpec.chrom+2, available*0.78)
}

func (layoutSpec *bubbleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := layoutSpec.width(size.Width)
	objects[0].Resize(fyne.NewSize(width, size.Height))
	objects[0].Resize(fyne.NewSize(width, objects[0].MinSize().Height))
	objects[0].Move(fyne.NewPos(size.Width-width, 0))
}

func (layoutSpec *bubbleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, objects[0].MinSize().Height)
}

// plainSpans is text as written: its line breaks kept, no Markdown read.
func plainSpans(text string) []span {
	var out []span
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			out = append(out, span{hard: true})
		}
		if line != "" {
			out = append(out, span{text: line})
		}
	}
	return out
}

// appendItem draws chat.Items[index] at the end of the transcript. Answers
// and tool rows are remembered in u.views, so a running task can update the
// one it is working on while its chat is on screen.
func (ui *desktopUI) appendItem(chat *conversation, index int) {
	item := chat.Items[index]
	var currentTurn *turn
	if chat.run != nil {
		currentTurn = chat.run.turn
	}
	switch item.Kind {
	case "assistant":
		view := newMarkdownView(item.Text)
		copyRow := ui.responseControls(chat, index)
		copyRow.Hide() // see syncCopies
		ui.views[index] = view
		ui.copyRows[index] = copyRow
		view.layer = newSelectLayer(ui, view.box)
		ui.place(container.NewVBox(container.NewStack(view.box, view.layer), copyRow))
	case "user":
		content := container.NewVBox()
		var pics []fyne.CanvasObject
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
			view.SetMinSize(fyne.NewSize(110, 110))
			pics = append(pics, surface(view, colCode, colBorderSoft, 8, 0))
		}
		if len(pics) > 0 {
			content.Add(container.New(layout.NewCustomPaddedHBoxLayout(6), pics...))
		}
		var text *flowText
		if item.Text != "" {
			// Fyne's wrapped label measured the text again for every
			// break it tried: a chat full of messages took seconds to open.
			text = newFlowText(plainSpans(item.Text), theme.TextSize(), colText)
			text.tail, text.tight = "", true
			content.Add(padded(theme.InnerPadding(), theme.InnerPadding(), theme.InnerPadding(), theme.InnerPadding(),
				container.NewStack(text, newSelectLayer(ui, text))))
		}
		bubble := container.New(&bubbleLayout{text: text, chrom: 2*theme.InnerPadding() + 4}, surface(content, colRaised, nil, 14, 2))
		copyRow := container.NewHBox(layout.NewSpacer(), copyChip(func() string { return item.Text }),
			newRow("Edit", iconPencil, styleChip, func() { ui.editMessage(chat, index) }), ui.versionControls(chat, item.ID))
		ui.place(container.New(layout.NewCustomPaddedVBoxLayout(2), bubble, copyRow))
	case "error":
		text := newFlowText(plainSpans(item.Text), theme.TextSize(), colDanger)
		text.tail, text.tight = "", true
		ui.place(container.NewVBox(padded(theme.InnerPadding(), theme.InnerPadding(), theme.InnerPadding(), theme.InnerPadding(),
			container.NewStack(text, newSelectLayer(ui, text))), ui.responseControls(chat, index)))
	case "note":
		icon := canvas.NewImageFromResource(tinted(iconCompact, colFaint))
		icon.SetMinSize(fyne.NewSquareSize(15))
		ui.place(container.NewBorder(nil, nil, container.NewCenter(icon), nil, newFlowText([]span{{text: item.Text}}, 12.5, colFaint)))
	case "tool":
		state := "done"
		if currentTurn != nil && currentTurn.states[index] != "" {
			state = currentTurn.states[index]
		}
		row := newToolRow(item, state, nil)
		row.chat, row.index = chat, index
		row.onTap = func() { ui.panel.open(chat, index) }
		row.selected = ui.panel.showing(chat, index)
		ui.views[index] = row
		if item.Tool == "cite" {
			row.below = container.NewVBox()
			if state == "done" {
				ui.showCitation(row.below, item)
			}
			ui.place(container.NewVBox(row, row.below))
			return
		}
		ui.place(row)
	}
}

// place adds an item's view to the conversation. While a whole chat is
// being built (openChat) nothing is laid out before the last one:
// Container.Add lays everything out each time, which made opening a chat
// quadratic (0.7 s for 240 items).
func (ui *desktopUI) place(view fyne.CanvasObject) {
	if ui.building {
		ui.transcript.Objects = append(ui.transcript.Objects, view)
		return
	}
	ui.transcript.Add(view)
}

// transcriptLayout stacks the conversation's items like a VBox, but keeps
// only those around the viewport visible. Fyne measures every visible
// object on each frame it draws, so in a long conversation every frame of
// an animation, every scroll step and every streamed word paid for the
// whole history. A hidden item costs nothing: it keeps its place with the
// size it last measured, and is measured again when it comes back in view.
type transcriptLayout struct {
	ui    *desktopUI
	sizes map[fyne.CanvasObject]fyne.Size // each item's last measured minimum size
	width float32                         // the width they were measured at
}

const transcriptGap = 14

func (layoutSpec *transcriptLayout) reset() { layoutSpec.sizes = map[fyne.CanvasObject]fyne.Size{} }

// changed forgets the size of the item that holds part: it changed while
// perhaps out of sight, where nothing measures it.
func (layoutSpec *transcriptLayout) changed(part fyne.CanvasObject) {
	for item := range layoutSpec.sizes {
		if _, holds := offsetIn(item, part); holds {
			delete(layoutSpec.sizes, item)
			return
		}
	}
}

// measure sizes an item at the column's width: wrapped text only knows its
// height once it has its width.
func (layoutSpec *transcriptLayout) measure(object fyne.CanvasObject, width float32) fyne.Size {
	if object.Size().Width != width {
		object.Resize(fyne.NewSize(width, object.Size().Height))
	}
	min := object.MinSize()
	layoutSpec.sizes[object] = min
	return min
}

// view is the band of the column worth keeping alive, in the transcript's
// coordinates: the viewport and a screen on each side, so a scroll step
// always lands on items that are already there.
func (layoutSpec *transcriptLayout) view() (top, bottom float32) {
	scroll := layoutSpec.ui.scroll
	if scroll == nil {
		return -1e9, 1e9
	}
	origin, found := offsetIn(scroll.Content, layoutSpec.ui.transcript)
	if !found {
		return -1e9, 1e9
	}
	height := scroll.Size().Height
	margin := fyne.Max(height, 600)
	offset := scroll.Offset.Y - origin.Y
	return offset - margin, offset + height + margin
}

func (layoutSpec *transcriptLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(120, 0)
	for i, object := range objects {
		min, known := layoutSpec.sizes[object]
		if object.Visible() || !known {
			min = object.MinSize()
		}
		size.Width = fyne.Max(size.Width, min.Width)
		size.Height += min.Height
		if i > 0 {
			size.Height += transcriptGap
		}
	}
	return size
}

func (layoutSpec *transcriptLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if size.Width <= 0 {
		return // not on screen yet: nothing can be measured
	}
	resized := size.Width != layoutSpec.width
	layoutSpec.width = size.Width
	top, bottom := layoutSpec.view()
	y := float32(0)
	for _, object := range objects {
		min, known := layoutSpec.sizes[object]
		if object.Visible() || !known || resized {
			min = layoutSpec.measure(object, size.Width)
		}
		near := y+min.Height >= top && y <= bottom
		switch {
		case near && !object.Visible():
			object.Show()
			min = layoutSpec.measure(object, size.Width) // it may have changed while out of sight
		case !near && object.Visible():
			object.Hide()
		}
		object.Move(fyne.NewPos(0, y))
		if near {
			object.Resize(fyne.NewSize(size.Width, min.Height))
		}
		y += min.Height + transcriptGap
	}
}

// syncCopies shows one Copy button per finished answer: under the last
// text of each turn, never between the texts and tool calls of a turn, and
// not while the turn is still running.
func (ui *desktopUI) syncCopies() {
	chat := ui.current
	if chat == nil {
		return
	}
	lastUser := -1
	for i := len(chat.Items) - 1; i >= 0; i-- {
		if chat.Items[i].Kind == "user" {
			lastUser = i
			break
		}
	}
	for index, row := range ui.copyRows {
		show := !(chat.run != nil && index > lastUser)
		for next := index + 1; show && next < len(chat.Items) && chat.Items[next].Kind != "user"; next++ {
			show = chat.Items[next].Kind != "assistant"
		}
		if show != row.Visible() {
			if show {
				row.Show()
			} else {
				row.Hide()
			}
			ui.items.changed(row)
		}
	}
}

// answerText is the whole answer of the turn ending at index: every text
// the model wrote since the user's message.
func answerText(chat *conversation, index int) string {
	var parts []string
	for i := index; i >= 0 && chat.Items[i].Kind != "user"; i-- {
		if chat.Items[i].Kind == "assistant" && strings.TrimSpace(chat.Items[i].Text) != "" {
			parts = append([]string{strings.TrimSpace(chat.Items[i].Text)}, parts...)
		}
	}
	return strings.Join(parts, "\n\n")
}

// copyChip copies text given at click time (an answer may still grow).
func copyChip(text func() string) *tapRow {
	var chip *tapRow
	chip = newRow("Copy", iconCopy, styleChip, func() {
		fyne.CurrentApp().Clipboard().SetContent(text())
		chip.SetText("Copied")
		time.AfterFunc(1400*time.Millisecond, func() { fyne.Do(func() { chip.SetText("Copy") }) })
	})
	return chip
}

// addItem appends an item to a chat, and to the transcript when that chat
// is the one on screen.
func (ui *desktopUI) addItem(chat *conversation, item agent.ChatItem) int {
	if item.ID == "" {
		item.ID = core.Identifier()[:16]
	}
	chat.Items = append(chat.Items, item)
	index := len(chat.Items) - 1
	ui.remote.itemChanged(chat, index)
	if chat == ui.current {
		ui.appendItem(chat, index)
		ui.relayoutColumn()
	}
	return index
}

// liveView is the widget showing chat.Items[index], if it is on screen.
func (ui *desktopUI) liveView(chat *conversation, index int) any {
	if chat != ui.current {
		return nil
	}
	return ui.views[index]
}

// relayoutColumn lays the conversation out again after messages were added
// or grew, and keeps the newest line in view when the reader follows it.
// Each level of the column is laid out on its own: Container.Refresh would
// redraw every message, and a resize alone does nothing while the chat is
// shorter than the window (the activity line then stayed over the answer).
func (ui *desktopUI) relayoutColumn() {
	defer platform.DevSlow("laying the conversation out", 6*time.Millisecond, time.Now())
	content := ui.scroll.Content.(*fyne.Container)
	relayout(ui.transcript) // first: it measures the items the sizes below are made of
	content.Resize(content.MinSize().Max(ui.scroll.Size()))
	for _, c := range []*fyne.Container{content, ui.column, ui.transcript} {
		c.Layout.Layout(c.Objects, c.Size())
	}
	if ui.followTail {
		ui.scroll.ScrollToBottom()
	} else {
		ui.scroll.ScrollToOffset(ui.scroll.Offset) // the bars follow the new height
	}
	relayout(ui.transcript) // the view moved without a scroll event: bring its items in
}

// ---------------------------------------------------------------------------
// Live turn: the agent's events reach the interface in batches. The stream
// never waits for the window, and a burst of tokens costs one update.

type uiEvent struct {
	kind string
	data any
}

type eventPump struct {
	mu        sync.Mutex
	events    []uiEvent
	scheduled bool
	handle    func(uiEvent)
}

func (pump *eventPump) emit(kind string, data any) {
	pump.mu.Lock()
	merged := false
	if n := len(pump.events); n > 0 {
		last := &pump.events[n-1]
		switch {
		case kind == "delta" && last.kind == "delta":
			last.data = last.data.(string) + data.(string)
			merged = true
		case kind == "activity" && last.kind == "activity":
			last.data = data // only the newest activity matters
			merged = true
		}
	}
	if !merged {
		pump.events = append(pump.events, uiEvent{kind, data})
	}
	schedule := !pump.scheduled
	pump.scheduled = true
	pump.mu.Unlock()
	if schedule {
		fyne.Do(pump.flush)
	}
}

// flush handles everything received so far, on the UI thread.
func (pump *eventPump) flush() {
	pump.mu.Lock()
	events := pump.events
	pump.events, pump.scheduled = nil, false
	pump.mu.Unlock()
	started := time.Now()
	for _, uiEvent := range events {
		devEvent(uiEvent)
		pump.handle(uiEvent)
	}
	if platform.Dev != nil {
		if took := time.Since(started); took > 8*time.Millisecond {
			platform.Devf("perf", "slow: handling %d agent events took %s (first: %s)", len(events), took.Round(100*time.Microsecond), events[0].kind)
		}
	}
}

// devEvent logs one event of an agent loop as the interface receives it.
func devEvent(uiEventValue uiEvent) {
	if platform.Dev == nil {
		return
	}
	switch data := uiEventValue.data.(type) {
	case string:
		if uiEventValue.kind == "delta" {
			platform.Devf("run", "delta, %d characters", len(data))
		} else {
			platform.Devf("run", "%s %s", uiEventValue.kind, platform.DevText(data, 120))
		}
	case agent.Activity:
		platform.Devf("run", "activity %s %s %s", data.State, data.Tool, platform.DevText(data.Detail, 80))
	case agent.ToolOutcome:
		failure := ""
		if data.Err != nil {
			failure = ", error: " + platform.DevText(data.Err.Error(), 80)
		}
		platform.Devf("run", "%s %s %s, output %d bytes, %s%s", uiEventValue.kind, data.Name, platform.DevText(data.Args, 100), len(data.Output), data.Elapsed.Round(time.Millisecond), failure)
	case agent.Message:
		platform.Devf("run", "%s %s: %s", uiEventValue.kind, data.Role, platform.DevText(data.Content, 100))
	case []agent.Message:
		platform.Devf("run", "%s, %d messages", uiEventValue.kind, len(data))
	case map[string]any:
		platform.Devf("run", "%s %v", uiEventValue.kind, platform.DevText(fmt.Sprint(data["title"]), 100))
	default:
		platform.Devf("run", "%s %v", uiEventValue.kind, data)
	}
}

// turn follows one run of the agent loop. It works on the chat's data and
// touches widgets only while that chat is on screen, so the user can go
// elsewhere while the agent works.
type turn struct {
	ui        *desktopUI
	chat      *conversation
	cfg       agent.Config
	index     int // the streaming answer's item, -1 between answers
	lastParse time.Time
	parseDue  bool
	calls     map[string]int // tool call id -> item index
	states    map[int]string // tool row state while the call runs
	lastCall  string         // the call an approval request is about
	waiting   int            // bumped to cancel the wait counter of a long command
	usage     int            // context tokens last reported during this run
	label     string         // what the activity line says
	mark      string         // what the ring shows
}

func (ui *desktopUI) newTurn(chat *conversation, cfg agent.Config) *turn {
	return &turn{ui: ui, chat: chat, cfg: cfg, index: -1, calls: map[string]int{}, states: map[int]string{}}
}

// render re-parses the streaming answer at most every 60 ms.
func (currentTurn *turn) render(force bool) {
	if currentTurn.index < 0 {
		return
	}
	if !force && time.Since(currentTurn.lastParse) < 60*time.Millisecond {
		if !currentTurn.parseDue {
			currentTurn.parseDue = true
			index := currentTurn.index
			time.AfterFunc(60*time.Millisecond, func() {
				fyne.Do(func() {
					currentTurn.parseDue = false
					if currentTurn.ui.ctx.Err() == nil && currentTurn.index == index {
						currentTurn.render(true)
					}
				})
			})
		}
		return
	}
	currentTurn.lastParse = time.Now()
	currentTurn.ui.remote.itemChanged(currentTurn.chat, currentTurn.index)
	if view, ok := currentTurn.ui.liveView(currentTurn.chat, currentTurn.index).(*markdownView); ok {
		view.ParseMarkdown(currentTurn.chat.Items[currentTurn.index].Text)
		currentTurn.ui.items.changed(view.box) // measured again even if the reader is elsewhere in the chat
		currentTurn.ui.relayoutColumn()
	}
}

// setRow changes a tool row's state, on screen too when it is shown.
func (currentTurn *turn) setRow(index int, state string) {
	currentTurn.states[index] = state
	currentTurn.ui.remote.itemChanged(currentTurn.chat, index)
	if row, ok := currentTurn.ui.liveView(currentTurn.chat, index).(*toolRow); ok {
		row.update(currentTurn.chat.Items[index], state)
		if row.below != nil && state == "done" {
			currentTurn.ui.showCitation(row.below, currentTurn.chat.Items[index])
			currentTurn.ui.relayoutColumn()
		}
	}
}

func (currentTurn *turn) handle(uiEventValue uiEvent) {
	ui := currentTurn.ui
	if ui.ctx.Err() != nil {
		return
	}
	switch uiEventValue.kind {
	case "delta":
		if currentTurn.index < 0 {
			currentTurn.index = len(currentTurn.chat.Items) // set first: the new view must know it streams
			ui.addItem(currentTurn.chat, agent.ChatItem{Kind: "assistant"})
		}
		currentTurn.chat.Items[currentTurn.index].Text += uiEventValue.data.(string)
		currentTurn.render(false)
		currentTurn.show(agent.Activity{State: "write"})
	case "text": // the streaming answer rewritten as a whole (ChatGPT (Web))
		if currentTurn.index < 0 {
			currentTurn.index = len(currentTurn.chat.Items)
			ui.addItem(currentTurn.chat, agent.ChatItem{Kind: "assistant"})
		}
		currentTurn.chat.Items[currentTurn.index].Text = uiEventValue.data.(string)
		currentTurn.render(true)
	case "message_end":
		currentTurn.render(true)
		currentTurn.index = -1
	case "activity":
		currentTurn.show(uiEventValue.data.(agent.Activity))
	case "tool":
		outcome := uiEventValue.data.(agent.ToolOutcome)
		index := ui.addItem(currentTurn.chat, agent.ChatItem{ID: outcome.RecallID, Kind: "tool", Tool: outcome.Name, Args: outcome.Args})
		currentTurn.calls[outcome.ID], currentTurn.lastCall = index, outcome.ID
		currentTurn.setRow(index, "running")
	case "approval":
		p := uiEventValue.data.(map[string]any)
		if index, ok := currentTurn.calls[currentTurn.lastCall]; ok {
			currentTurn.setRow(index, "pending")
		}
		ui.showApproval(currentTurn.chat, p)
		currentTurn.show(agent.Activity{State: "approval", Detail: approvalSubject(p)})
	case "question":
		request := uiEventValue.data.(agent.QuestionRequest)
		ui.showQuestions(currentTurn.chat, request)
		currentTurn.show(agent.Activity{State: "question"})
	case "tool_exec":
		toolOutcomeValue := uiEventValue.data.(agent.ToolOutcome)
		if index, ok := currentTurn.calls[toolOutcomeValue.ID]; ok {
			currentTurn.setRow(index, "running")
		}
		currentTurn.showRun(toolOutcomeValue)
	case "tool_output": // a running command's output so far
		o := uiEventValue.data.(agent.ToolOutcome)
		if index, ok := currentTurn.calls[o.ID]; ok {
			currentTurn.chat.Items[index].Text = o.Output
			ui.remote.itemChanged(currentTurn.chat, index)
			ui.panel.changed(currentTurn.chat, index)
		}
	case "tool_result":
		o := uiEventValue.data.(agent.ToolOutcome)
		index, ok := currentTurn.calls[o.ID]
		if !ok {
			return
		}
		item := &currentTurn.chat.Items[index]
		item.Text, item.Diff, item.Millis, item.Images = o.Output, o.Diff, o.Elapsed.Milliseconds(), o.Images
		if o.Err != nil {
			item.Failed = o.Err.Error()
		}
		currentTurn.setRow(index, "done")
		delete(currentTurn.states, index)
		ui.panel.changed(currentTurn.chat, index)
		currentTurn.show(agent.Activity{State: "wait"})
	case "injected":
		messageValue := uiEventValue.data.(agent.Message)
		if messageValue.Internal {
			ui.addItem(currentTurn.chat, agent.ChatItem{Kind: "note", Text: agent.WakeSummary(messageValue)})
		} else {
			ui.addItem(currentTurn.chat, agent.ChatItem{Kind: "user", Text: messageValue.Content, Images: messageValue.Images})
		}
		if currentTurn.chat == ui.current { // the answer before it is now complete
			ui.syncCopies()
			ui.relayoutColumn()
		}
		ui.save()
	case "history":
		currentTurn.chat.History = uiEventValue.data.([]agent.Message)
	case "cache":
		ui.save()
	case "context_note", "note":
		ui.addItem(currentTurn.chat, agent.ChatItem{Kind: "note", Text: uiEventValue.data.(string)})
		ui.save()
	case "todo":
		ui.todoUpdated(currentTurn.chat)
		ui.save()
	case "usage":
		currentTurn.usage = uiEventValue.data.(int)
		ui.remote.metaChanged(currentTurn.chat)
		if currentTurn.chat == ui.current {
			ui.setContextUsage(uiEventValue.data.(int), agent.ModelContextWindow(currentTurn.cfg))
		}
	case "notice":
		currentTurn.label = uiEventValue.data.(string)
		ui.applyActivity(currentTurn)
	case "web_session":
		value := uiEventValue.data.(agent.WebConversation)
		currentTurn.chat.Config.WebConversation = &value
		if currentTurn.chat == ui.current {
			ui.cfg.WebConversation = &value
		}
		ui.save()
	case "session": // Claude Code named the chat's session: later turns resume it
		currentTurn.chat.Config.Session = uiEventValue.data.(string)
		if currentTurn.chat == ui.current {
			ui.cfg.Session = currentTurn.chat.Config.Session
		}
		ui.save()
	case "session_synced": // the session has seen everything shown so far
		if currentTurn.chat.Config.Session != "" {
			currentTurn.chat.Config.SessionMark = len(currentTurn.chat.Items)
			if currentTurn.chat == ui.current {
				ui.cfg.SessionMark = currentTurn.chat.Config.SessionMark
			}
			ui.save()
		}
	case "plan":
		ui.plan = uiEventValue.data.(agent.PlanUsage)
		if currentTurn.chat == ui.current {
			ui.contextRing.plan = ui.plan
		}
	}
}

// finish closes the turn: calls left without a result were stopped.
func (currentTurn *turn) finish() {
	currentTurn.waiting++
	currentTurn.render(true)
	currentTurn.index = -1
	for index, state := range currentTurn.states {
		if state != "done" {
			currentTurn.chat.Items[index].Failed = stoppedCall
			currentTurn.setRow(index, "done")
			currentTurn.ui.panel.changed(currentTurn.chat, index)
		}
	}
	currentTurn.states = map[int]string{}
}

// state is how far a call of this turn has got, for the side panel.
func (currentTurn *turn) state(chat *conversation, index int) string {
	if currentTurn == nil || currentTurn.chat != chat || currentTurn.states[index] == "" {
		return "done"
	}
	return currentTurn.states[index]
}

func approvalSubject(p map[string]any) string {
	title, _ := p["title"].(string)
	return strings.TrimSuffix(title, "?")
}

// show tells, under the conversation, what the agent is doing.
func (currentTurn *turn) show(activityValue agent.Activity) {
	currentTurn.waiting++ // the command is over: no more counting
	label := map[string]string{
		"wait":    "Waiting for the model",
		"think":   "Thinking",
		"write":   "Writing",
		"compact": "Compacting the conversation",
	}[activityValue.State]
	switch activityValue.State {
	case "call":
		detail := agent.FirstLine(activityValue.Detail)
		switch activityValue.Tool {
		case "shell":
			label = "Writing a command"
		case "edit_file":
			label = "Preparing an edit"
			if detail != "" {
				label, detail = label+" to "+detail, ""
			}
		case "read_file":
			label = "Choosing a file to read"
			if detail != "" {
				label, detail = "Preparing to read "+detail, ""
			}
		case "list_directory":
			label = "Preparing to list a directory"
		case "web_search":
			label = "Searching the web"
		case "tool_search":
			label = "Loading tools"
		default:
			label = "Preparing " + activityValue.Tool
		}
		if detail != "" {
			label += ": " + detail
		}
	case "question":
		label = "Waiting for your answer"
	case "approval":
		label = "Waiting for your approval"
		if activityValue.Detail != "" {
			label += ": " + activityValue.Detail
		}
	case "background":
		// The answer is written; the agent now waits for a task it left
		// running (Claude Code's background commands and monitors).
		what := "the background task"
		if activityValue.Detail != "" {
			what += " “" + agent.FirstLine(activityValue.Detail) + "”"
		}
		if strings.HasSuffix(activityValue.Detail, "background tasks") {
			what = activityValue.Detail
		}
		currentTurn.countWait(what, 0)
		return
	}
	currentTurn.label, currentTurn.mark = label+"…", activityValue.State
	if activityValue.State == "question" {
		currentTurn.mark = "approval" // the ring's waiting-for-you glyph
	}
	currentTurn.ui.applyActivity(currentTurn)
}

// showRun reports a tool that has started to run.
func (currentTurn *turn) showRun(outcome agent.ToolOutcome) {
	path, command, _ := agent.ToolArgs(outcome.Args)
	switch outcome.Name {
	case "shell":
		currentTurn.label, currentTurn.mark = "Running "+agent.FirstLine(command)+"…", "bash"
		currentTurn.countWait(agent.FirstLine(command), longCommand)
	case "read_file":
		currentTurn.label, currentTurn.mark = "Reading "+path+"…", "read"
	case "list_directory":
		currentTurn.label, currentTurn.mark = "Listing "+path+"…", "list"
	case "edit_file":
		currentTurn.label, currentTurn.mark = "Applying the edit to "+path+"…", "edit"
	case "apply_patch":
		currentTurn.label, currentTurn.mark = "Applying the patch…", "edit"
	default: // a tool of another harness (Claude Code's Grep, WebFetch, Agent…)
		currentTurn.label, currentTurn.mark = "Using "+outcome.Name+"…", "call"
	}
	currentTurn.ui.applyActivity(currentTurn)
}

// applyActivity shows a turn's activity when its chat is on screen.
func (ui *desktopUI) applyActivity(currentTurn *turn) {
	ui.remote.metaChanged(currentTurn.chat)
	if currentTurn.chat != ui.current || currentTurn.mark == "" {
		return
	}
	label := currentTurn.label
	if run := currentTurn.chat.run; run != nil && run.questions != nil && !run.questions.open {
		label = "Waiting for your answer: click here to see the questions"
	}
	ui.thinkLabel.SetText(label)
	ui.workMark.setState(currentTurn.mark)
}

// runState is how far a tool call has got: "done" unless its chat's run is
// still working on it.
func runState(chat *conversation, index int) string {
	if chat.run == nil {
		return "done"
	}
	return chat.run.turn.state(chat, index)
}

// longCommand is how long a command runs before the ring shows that the
// agent is waiting for it: the clock, with the time it has taken so far.
const longCommand = 2 * time.Second

// countWait shows the clock for something the agent waits for (a command
// that lasts, a background task) from the given delay on, and counts the
// time until any new activity.
func (currentTurn *turn) countWait(what string, after time.Duration) {
	currentTurn.waiting++
	turn, started := currentTurn.waiting, time.Now()
	var tick func()
	tick = func() {
		fyne.Do(func() {
			if currentTurn.waiting != turn || currentTurn.ui.ctx.Err() != nil {
				return
			}
			seconds := int(time.Since(started).Seconds())
			spent := fmt.Sprintf("%d s", seconds)
			if seconds >= 60 {
				spent = fmt.Sprintf("%d min %02d s", seconds/60, seconds%60)
			}
			currentTurn.label = fmt.Sprintf("Waiting for %s… %s", what, spent)
			currentTurn.mark = "wait"
			currentTurn.ui.applyActivity(currentTurn)
			time.AfterFunc(time.Second, tick)
		})
	}
	if after == 0 { // shown at once: the line must not keep saying "Writing"
		currentTurn.label, currentTurn.mark = "Waiting for "+what+"…", "wait"
		currentTurn.ui.applyActivity(currentTurn)
		after = time.Second
	}
	time.AfterFunc(after, tick)
}

// diffStat counts added and removed lines of a unified diff.
func diffStat(diff string) (added, removed int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return
}

func elapsed(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%d ms", ms)
	case ms < 60000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(ms)/1000), ".0") + " s"
	}
	return fmt.Sprintf("%dm %02ds", ms/60000, ms/1000%60)
}
