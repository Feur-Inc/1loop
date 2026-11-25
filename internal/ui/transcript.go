// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"image/color"
	"sort"
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
	if agent.PhoneTool(tool) {
		return iconPhone
	}
	switch tool {
	case "shell", "adb_shell":
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
	case "settings":
		return iconSliders
	case "subchat":
		return iconSubchat
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
	settle   float32         // how far its outcome (+3 −1, 2 s, Failed) has come in, once the call ends
	settling *fyne.Animation // its slide
	preview  *generatedImagePreview
}

func newToolRow(item agent.ChatItem, state string, onTap func()) *toolRow {
	row := &toolRow{item: item, state: state, onTap: onTap, settle: 1}
	row.ExtendBaseWidget(row)
	return row
}

func (row *toolRow) update(item agent.ChatItem, state string) {
	if row.state != state && state == "done" && row.Visible() {
		// The call ended while seen: its outcome slides in beside it.
		stopMotion(&row.settling)
		row.settle = 0
		row.settling = motion(180*time.Millisecond, fyne.AnimationEaseOut, func(p float32) {
			row.settle = p
			row.Refresh()
		})
	}
	row.item, row.state = item, state
	if row.preview != nil {
		row.preview.update(item, state)
	}
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

// The row's height does not depend on its text or width. Measuring an
// off-screen call must not create its renderer and shape its full target.
func (row *toolRow) MinSize() fyne.Size {
	height := textSize("Ag", 13.5, fyne.TextStyle{}).Height + 12
	return fyne.NewSize(120, height)
}

func (row *toolRow) fade(to float32) {
	if row.anim != nil {
		row.anim.Stop()
	}
	from := row.hover
	row.anim = motion(130*time.Millisecond, fyne.AnimationEaseOut, func(p float32) {
		row.hover = from + (to-from)*p
		row.Refresh()
	})
}

func (row *toolRow) CreateRenderer() fyne.WidgetRenderer {
	renderer := &toolRowRenderer{row: row, bg: canvas.NewRectangle(color.Transparent), icon: canvas.NewImageFromResource(nil),
		verb: canvas.NewText("", colMuted), target: canvas.NewText("", colText),
		meta: canvas.NewText("", colFaint), minus: canvas.NewText("", colDanger)}
	renderer.bg.CornerRadius = rounded(8)
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
	return renderer.row.MinSize()
}

func (renderer *toolRowRenderer) Layout(size fyne.Size) {
	if size.Width <= 0 || size.Height <= 0 {
		return
	}
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
	x += renderer.target.MinSize().Width + 10 - (1-renderer.row.settle)*6
	renderer.meta.Move(fyne.NewPos(x, middle(renderer.meta)))
	renderer.minus.Move(fyne.NewPos(x+renderer.meta.MinSize().Width+5, middle(renderer.minus)))
}

func (renderer *toolRowRenderer) Refresh() {
	row := renderer.row
	if !row.Visible() {
		return // Show refreshes the current labels before the row is drawn.
	}
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
	if row.settle < 1 { // fading in, in steps: each colour of a text is a texture
		renderer.meta.Color = withAlpha(color.NRGBAModel.Convert(renderer.meta.Color).(color.NRGBA), quantize(row.settle, 4))
		renderer.minus.Color = withAlpha(colDanger, quantize(row.settle, 4))
	} else {
		renderer.minus.Color = colDanger
	}
	h := quantize(row.hover, 4)
	renderer.verb.Color = mix(colMuted, colText, h*0.5)
	renderer.bg.FillColor = mix(color.NRGBA{}, colHover, row.hover)
	if row.selected {
		renderer.bg.FillColor = mix(colSelected, overlay(0x18), row.hover)
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
	text     *flowText       // nil for a message of images only
	pictures *fyne.Container // its pictures (pictureStrip), nil without
	picChrom float32         // the bubble's width around them
	chrom    float32
	natural  float32
	known    bool
}

func (layoutSpec *bubbleLayout) width(available float32) float32 {
	if !layoutSpec.known {
		layoutSpec.known = true
		if layoutSpec.text != nil {
			layoutSpec.natural, _ = layoutSpec.text.extent()
		}
	}
	// As wide as its text or its pictures on one line: pictures wrap, and
	// shrink, only when the bubble cannot be that wide (#435).
	natural := layoutSpec.natural + layoutSpec.chrom + 2
	if pictures := pictureStripWidth(layoutSpec.pictures); pictures > 0 {
		natural = fyne.Max(natural, pictures+layoutSpec.picChrom)
	}
	return fyne.Min(natural, available*0.78)
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
	if ui.loadingChat != nil && ui.loadingChat.chat == chat && !ui.building {
		return // the loader picks up newly appended items on its next frame
	}
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
		row := container.NewVBox(container.NewStack(view.box, view.layer), copyRow)
		ui.items.own(view.box, row)
		ui.items.own(copyRow, row)
		ui.place(row)
	case "user":
		if item.From != nil { // another chat's message, not the user's
			ui.appendChatMessage(chat, item)
			return
		}
		view := &userImageView{pictures: newPictureStrip(4)} // 4 inside the bubble's round corners
		ui.setUserImages(view, item.Images)
		content := container.NewVBox(view.pictures)
		if chips := fileChips(item.Files); chips != nil {
			content.Add(chips)
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
		bubble := container.New(&bubbleLayout{text: text, pictures: view.pictures, picChrom: 2 * 2, chrom: 2*theme.InnerPadding() + 4},
			surface(content, colRaised, nil, 14, 2))
		copyRow := container.NewHBox(layout.NewSpacer(), copyChip(func() string { return item.Text }),
			newRow("Edit", iconPencil, styleChip, func() { ui.editMessage(chat, index) }), ui.versionControls(chat, item.ID))
		view.box = container.New(layout.NewCustomPaddedVBoxLayout(2), bubble, copyRow)
		if wrap, ok := answeredWrapUp(chat.Items, index); ok { // the question this message answers
			view.box = container.NewVBox(padded(0, transcriptGap-4, 0, 0, nextStepCard(wrap, nil, item.Text)), view.box)
		}
		ui.views[index] = view
		ui.items.alone(view.box)
		ui.place(view.box)
	case "error":
		text := newFlowText(plainSpans(item.Text), theme.TextSize(), colDanger)
		text.tail, text.tight = "", true
		ui.place(container.NewVBox(padded(theme.InnerPadding(), theme.InnerPadding(), theme.InnerPadding(), theme.InnerPadding(),
			container.NewStack(text, newSelectLayer(ui, text))), ui.responseControls(chat, index)))
	case "note":
		glyph := iconCompact
		if strings.HasPrefix(item.Text, "Background task ") { // agent.WakeSummary
			glyph = iconTaskEnded
		}
		if item.From != nil || strings.HasPrefix(item.Text, "Subchat ") || strings.HasPrefix(item.Text, "Forked from ") {
			glyph = iconSubchat
		}
		icon := canvas.NewImageFromResource(tinted(glyph, colFaint))
		icon.SetMinSize(fyne.NewSquareSize(15))
		var compare fyne.CanvasObject
		if compactionNote(item) {
			compare = newRow("Compare", nil, styleChip, func() { ui.compareCompaction(chat, item.Text) })
		}
		ui.place(container.NewBorder(nil, nil, container.NewCenter(icon), compare, newFlowText([]span{{text: item.Text}}, 12.5, colFaint)))
	case "tool":
		if agent.WrapUpTool(item.Tool) {
			return // drawn as the next-step card
		}
		state := "done"
		if currentTurn != nil && currentTurn.states[index] != "" {
			state = currentTurn.states[index]
		}
		if chat.server != nil {
			state = runState(chat, index)
		}
		row := newToolRow(item, state, nil)
		row.chat, row.index = chat, index
		row.onTap = func() { ui.panel.open(chat, index) }
		if id := startedSubchat(item); id != "" { // the subchat itself, rather than the call
			row.onTap = func() { ui.openSubchat(chat, id) }
		}
		row.selected = ui.panel.showing(chat, index)
		ui.views[index] = row
		if item.Tool == "imagegen" {
			row.preview = newGeneratedImagePreview(item, state, row.onTap)
			ui.place(container.NewVBox(row, row.preview.box))
		} else {
			ui.place(row)
		}
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
	ui.items.arrive(view)
	ui.transcript.Add(view)
}

// transcriptLayout stacks the conversation's items like a VBox, but keeps
// only those around the viewport visible. Fyne measures every visible
// object on each frame it draws, so in a long conversation every frame of
// an animation, every scroll step and every streamed word paid for the
// whole history. A hidden item costs nothing: it keeps its place with the
// size it last measured, and is measured again when it comes back in view.
type transcriptLayout struct {
	ui *desktopUI
	// Each item's last measured size, in the transcript's order, and the
	// top of each (tops[i]; tops[len] is the bottom). Only the items near
	// the view are measured at a frame: a long chat costs as little as a
	// short one to lay out, scroll and stream into.
	rows    []transcriptRow
	tops    []float32
	dirty   int                                     // tops from this row on are out of date
	index   map[fyne.CanvasObject]int               // row -> its place in rows
	owner   map[fyne.CanvasObject]fyne.CanvasObject // part -> the row that holds it (changed)
	early   map[fyne.CanvasObject]transcriptRow     // measured before being laid out (chat_loading.go)
	lo, hi  int                                     // the rows shown at the last layout
	added   int                                     // rows from here on came since: shown, perhaps
	widest  float32                                 // the widest row
	pending bool                                    // some rows were never measured, or changed
	width   float32                                 // the column's width
	stale   bool                                    // items out of sight keep an earlier width's size (remeasure)
	shift   float32                                 // how much the items above the view grew since the scroll last moved
	// Items that just came in rise a few points into place while a veil of
	// the background over them clears: Fyne can't fade a whole message, but
	// it can fade one rectangle over it.
	arrivals map[fyne.CanvasObject]*arrival
	veils    *fyne.Container // over the transcript, in its coordinates
	// Behind each run of the agent's rows (its answers, tool rows, notes)
	// one rounded card, the theme's backdrop over a picture or shader:
	// not behind the user's messages, nor the gaps between turns (#503).
	cards   *fyne.Container            // under the transcript, in its coordinates
	cardSet []fyne.CanvasObject        // the cards made so far, reused
	apart   map[fyne.CanvasObject]bool // rows that are in no card (the user's messages)
}

// transcriptRow is an item of the transcript and its measured size.
type transcriptRow struct {
	object fyne.CanvasObject
	size   fyne.Size
	width  float32 // the width it was measured at
	known  bool
}

// arrival is an item coming into the conversation.
type arrival struct {
	progress float32
	y        float32 // its place, once laid out
	laid     bool
	veil     *canvas.Rectangle
	anim     *fyne.Animation
}

const arrivalRise = 8

// arrive lets a new item rise and come out of the background as it is
// placed. A whole chat being built does not: only what arrives while it
// is on screen.
func (layoutSpec *transcriptLayout) arrive(object fyne.CanvasObject) {
	if !motionOn() || layoutSpec.veils == nil {
		return
	}
	if layoutSpec.arrivals == nil {
		layoutSpec.arrivals = map[fyne.CanvasObject]*arrival{}
	}
	coming := &arrival{veil: canvas.NewRectangle(colBackground)}
	coming.veil.Hide()
	layoutSpec.arrivals[object] = coming
	layoutSpec.veils.Add(coming.veil)
	coming.anim = motion(200*time.Millisecond, fyne.AnimationEaseOut, func(p float32) {
		coming.progress = p
		if p >= 1 {
			delete(layoutSpec.arrivals, object)
			layoutSpec.veils.Remove(coming.veil)
		}
		layoutSpec.placeArrival(object, coming)
	})
}

// placeArrival draws an arriving item where it is in its rise.
func (layoutSpec *transcriptLayout) placeArrival(object fyne.CanvasObject, coming *arrival) {
	if !coming.laid {
		return
	}
	at := fyne.NewPos(0, coming.y+(1-coming.progress)*arrivalRise)
	object.Move(at)
	if coming.progress >= 1 || !object.Visible() {
		coming.veil.Hide()
		return
	}
	coming.veil.FillColor = withAlpha(colBackground, 1-coming.progress)
	coming.veil.Move(at)
	coming.veil.Resize(object.Size())
	coming.veil.Show()
	coming.veil.Refresh()
}

// settle ends the arrivals at once (a chat cleared or another opened).
func (layoutSpec *transcriptLayout) settle() {
	for object, coming := range layoutSpec.arrivals {
		stopMotion(&coming.anim)
		delete(layoutSpec.arrivals, object)
	}
	if layoutSpec.veils != nil {
		layoutSpec.veils.RemoveAll()
	}
}

const transcriptGap = 14

func (layoutSpec *transcriptLayout) reset() {
	layoutSpec.rows, layoutSpec.tops, layoutSpec.dirty = nil, nil, 0
	layoutSpec.index = map[fyne.CanvasObject]int{}
	layoutSpec.owner = map[fyne.CanvasObject]fyne.CanvasObject{}
	layoutSpec.early = map[fyne.CanvasObject]transcriptRow{}
	layoutSpec.apart = map[fyne.CanvasObject]bool{}
	layoutSpec.lo, layoutSpec.hi, layoutSpec.added, layoutSpec.widest, layoutSpec.pending = 0, 0, 0, 0, false
	layoutSpec.stale, layoutSpec.shift = false, 0
	layoutSpec.settle()
}

// transcriptState is what a cached chat view keeps of the layout.
type transcriptState struct {
	rows  []transcriptRow
	index map[fyne.CanvasObject]int
	owner map[fyne.CanvasObject]fyne.CanvasObject
	early map[fyne.CanvasObject]transcriptRow
	apart map[fyne.CanvasObject]bool
	width float32
}

func (layoutSpec *transcriptLayout) save() transcriptState {
	return transcriptState{layoutSpec.rows, layoutSpec.index, layoutSpec.owner, layoutSpec.early, layoutSpec.apart, layoutSpec.width}
}

func (layoutSpec *transcriptLayout) restore(state transcriptState) {
	layoutSpec.reset()
	layoutSpec.rows, layoutSpec.index, layoutSpec.owner, layoutSpec.early, layoutSpec.apart, layoutSpec.width =
		state.rows, state.index, state.owner, state.early, state.apart, state.width
	layoutSpec.hi = len(layoutSpec.rows) // any of them may be shown: the next layout looks at each once
	layoutSpec.pending = true
	for _, row := range layoutSpec.rows {
		layoutSpec.widest = fyne.Max(layoutSpec.widest, row.size.Width)
	}
}

// alone keeps a row out of the agent's cards: a message of the user's.
func (layoutSpec *transcriptLayout) alone(row fyne.CanvasObject) {
	if layoutSpec.apart == nil {
		layoutSpec.apart = map[fyne.CanvasObject]bool{}
	}
	layoutSpec.apart[row] = true
}

// own tells which row holds part, so that changed finds it at once.
func (layoutSpec *transcriptLayout) own(part, row fyne.CanvasObject) {
	layoutSpec.owner[part] = row
}

// sync follows the transcript's objects: rows added at the end are the
// usual change; anything else builds the list again, keeping the sizes
// of the rows still there.
func (layoutSpec *transcriptLayout) sync(objects []fyne.CanvasObject) {
	rows := layoutSpec.rows
	same := len(objects) >= len(rows)
	for i := range rows {
		if !same {
			break
		}
		same = objects[i] == rows[i].object
	}
	if same && len(objects) == len(rows) {
		return
	}
	start := len(rows)
	if !same {
		old := rows
		known := make(map[fyne.CanvasObject]transcriptRow, len(old))
		for _, row := range old {
			known[row.object] = row
		}
		rows, start = make([]transcriptRow, 0, len(objects)), 0
		clear(layoutSpec.index)
		for _, object := range objects {
			row, ok := known[object]
			if !ok {
				row = transcriptRow{object: object}
			}
			rows = append(rows, row)
		}
		layoutSpec.lo, layoutSpec.hi = 0, len(rows) // look at each once: some may still be shown
		layoutSpec.widest = 0
		for _, row := range rows {
			layoutSpec.widest = fyne.Max(layoutSpec.widest, row.size.Width)
		}
		layoutSpec.dirty = 0
	}
	for i := start; i < len(objects); i++ {
		if i >= len(rows) {
			rows = append(rows, transcriptRow{object: objects[i]})
		}
		if early, ok := layoutSpec.early[objects[i]]; ok && !rows[i].known {
			early.object = objects[i]
			rows[i] = early
			layoutSpec.widest = fyne.Max(layoutSpec.widest, early.size.Width)
		}
		delete(layoutSpec.early, objects[i])
		layoutSpec.index[objects[i]] = i
		if !rows[i].known {
			layoutSpec.pending = true
		}
	}
	layoutSpec.rows = rows
	layoutSpec.dirty = min(layoutSpec.dirty, start)
	layoutSpec.added = min(layoutSpec.added, start)
	if layoutSpec.hi > len(rows) {
		layoutSpec.hi = len(rows)
	}
}

// changed forgets the size of the item that holds part: it changed while
// perhaps out of sight, where nothing measures it.
func (layoutSpec *transcriptLayout) changed(part fyne.CanvasObject) {
	row, ok := layoutSpec.owner[part]
	if !ok {
		if _, isRow := layoutSpec.index[part]; isRow {
			row, ok = part, true
		}
	}
	if !ok { // not told: look from the end, where the parts that change are
		items := layoutSpec.ui.transcript.Objects
		for i := len(items) - 1; i >= 0; i-- {
			if _, holds := offsetIn(items[i], part); holds {
				row, ok = items[i], true
				layoutSpec.owner[part] = row
				break
			}
		}
	}
	if !ok {
		return
	}
	layoutSpec.forget(row)
}

// forget makes the row be measured again at the next layout.
func (layoutSpec *transcriptLayout) forget(row fyne.CanvasObject) {
	delete(layoutSpec.early, row)
	if i, ok := layoutSpec.index[row]; ok && i < len(layoutSpec.rows) && layoutSpec.rows[i].object == row {
		layoutSpec.rows[i].known = false
		layoutSpec.pending = true
	}
}

// known tells whether a row was measured.
func (layoutSpec *transcriptLayout) known(object fyne.CanvasObject) bool {
	if _, ok := layoutSpec.early[object]; ok {
		return true
	}
	i, ok := layoutSpec.index[object]
	return ok && i < len(layoutSpec.rows) && layoutSpec.rows[i].object == object && layoutSpec.rows[i].known
}

// heightOf is the measured height of row i.
func (layoutSpec *transcriptLayout) heightOf(i int) float32 { return layoutSpec.rows[i].size.Height }

// measure sizes an item at the column's width: wrapped text only knows its
// height once it has its width. An item not laid out yet (a chat being
// built) keeps its size until it is.
func (layoutSpec *transcriptLayout) measure(object fyne.CanvasObject, width float32) fyne.Size {
	min := measureRow(object, width)
	if i, ok := layoutSpec.index[object]; ok && i < len(layoutSpec.rows) && layoutSpec.rows[i].object == object {
		layoutSpec.store(i, min, width)
	} else {
		layoutSpec.early[object] = transcriptRow{object: object, size: min, width: width, known: true}
	}
	return min
}

func (layoutSpec *transcriptLayout) measureAt(i int, width float32) fyne.Size {
	min := measureRow(layoutSpec.rows[i].object, width)
	layoutSpec.store(i, min, width)
	return min
}

func (layoutSpec *transcriptLayout) store(i int, measured fyne.Size, width float32) {
	row := &layoutSpec.rows[i]
	if !row.known || row.size.Height != measured.Height {
		layoutSpec.dirty = min(layoutSpec.dirty, i+1)
	}
	row.size, row.width, row.known = measured, width, true
	layoutSpec.widest = fyne.Max(layoutSpec.widest, measured.Width)
}

func measureRow(object fyne.CanvasObject, width float32) fyne.Size {
	// Hidden paragraphs still need their exact height, but not new drawing
	// objects, selection geometry or link hitboxes for every transient width.
	hidden := !object.Visible()
	if hidden {
		measureTranscriptText(object, true)
		defer measureTranscriptText(object, false)
	}
	row, fixed := object.(*toolRow)
	// Plain calls have a fixed height. Resize only when they enter the
	// viewport, otherwise BaseWidget.Resize creates every hidden renderer.
	if !fixed || row.preview != nil {
		if object.Size().Width != width {
			object.Resize(fyne.NewSize(width, object.Size().Height))
		}
		// A box lays its parts out with their heights from before the new
		// width wrapped them: lay it out again, once its text knows its
		// height, or its parts overlap or leave gaps (#440).
		if box, ok := object.(*fyne.Container); ok && !hidden {
			settleBox(box)
		}
	}
	return object.MinSize()
}

// settleBox lays a box and the boxes in it out again at their sizes.
func settleBox(box *fyne.Container) {
	if box.Layout != nil {
		box.Layout.Layout(box.Objects, box.Size())
	}
	for _, child := range box.Objects {
		if inner, ok := child.(*fyne.Container); ok && inner.Visible() {
			settleBox(inner)
		}
	}
}

// measureTranscriptText marks the paragraphs of an item as measured only
// (no drawing objects), and when that ends for an item out of sight, lets
// them drop their words and lines: only its height is kept.
func measureTranscriptText(object fyne.CanvasObject, measuring bool) {
	switch object := object.(type) {
	case *flowText:
		object.measuring = measuring
		if !measuring && !object.Visible() {
			object.release()
		}
	case *fyne.Container:
		for _, child := range object.Objects {
			measureTranscriptText(child, measuring)
		}
	}
}

// releaseTranscriptText lets the paragraphs of an item gone out of sight
// drop their words and lines: they are made again when it comes back.
func releaseTranscriptText(object fyne.CanvasObject) {
	switch object := object.(type) {
	case *flowText:
		object.release()
	case *fyne.Container:
		for _, child := range object.Objects {
			releaseTranscriptText(child)
		}
	}
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

// prepare brings the rows up to date with the objects: the rows never
// measured are, and the tops below a change are summed again. Rows out of
// sight move to their new places only then.
func (layoutSpec *transcriptLayout) prepare(objects []fyne.CanvasObject, width float32) {
	layoutSpec.sync(objects)
	if layoutSpec.pending {
		layoutSpec.pending = false
		for i := range layoutSpec.rows {
			if !layoutSpec.rows[i].known {
				if width > 0 {
					layoutSpec.measureAt(i, width)
				} else {
					layoutSpec.store(i, layoutSpec.rows[i].object.MinSize(), 0)
				}
			}
		}
	}
	layoutSpec.sum()
}

// sum makes the tops from the first row whose top is out of date, and
// moves the rows out of sight there.
func (layoutSpec *transcriptLayout) sum() {
	rows := layoutSpec.rows
	if len(layoutSpec.tops) != len(rows)+1 {
		layoutSpec.tops = append(layoutSpec.tops[:0], make([]float32, len(rows)+1)...)
		layoutSpec.dirty = 0
	}
	if layoutSpec.dirty > len(rows) {
		return
	}
	y := float32(0)
	from := layoutSpec.dirty
	if from > 0 {
		y = layoutSpec.tops[from-1] + rows[from-1].size.Height + transcriptGap
	}
	for i := from; i < len(rows); i++ {
		layoutSpec.tops[i] = y
		if (i < layoutSpec.lo || i >= layoutSpec.hi) && rows[i].object.Position().Y != y {
			rows[i].object.Move(fyne.NewPos(0, y))
		}
		y += rows[i].size.Height + transcriptGap
	}
	layoutSpec.tops[len(rows)] = y
	layoutSpec.dirty = len(rows) + 1
}

// height is the whole transcript's.
func (layoutSpec *transcriptLayout) height() float32 {
	n := len(layoutSpec.rows)
	if n == 0 {
		return 0
	}
	return layoutSpec.tops[n] - transcriptGap
}

// first is the first row whose bottom is at or below y.
func (layoutSpec *transcriptLayout) first(y float32) int {
	rows, tops := layoutSpec.rows, layoutSpec.tops
	return sort.Search(len(rows), func(i int) bool { return tops[i]+rows[i].size.Height >= y })
}

func (layoutSpec *transcriptLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	layoutSpec.prepare(objects, layoutSpec.width)
	// What is shown may have changed without telling (a row's state).
	for i := layoutSpec.lo; i < layoutSpec.hi && i < len(layoutSpec.rows); i++ {
		if object := layoutSpec.rows[i].object; object.Visible() {
			layoutSpec.store(i, object.MinSize(), layoutSpec.rows[i].width)
		}
	}
	layoutSpec.sum()
	return fyne.NewSize(fyne.Max(120, layoutSpec.widest), layoutSpec.height())
}

func (layoutSpec *transcriptLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if size.Width <= 0 {
		return // not on screen yet: nothing can be measured
	}
	if size.Width != layoutSpec.width {
		layoutSpec.stale = true
	}
	layoutSpec.width = size.Width
	layoutSpec.prepare(objects, size.Width)
	rows := layoutSpec.rows
	top, bottom := layoutSpec.view()
	shown := top // the view's top: view adds a margin above it
	if scroll := layoutSpec.ui.scroll; scroll != nil {
		shown += fyne.Max(scroll.Size().Height, 600)
	}
	lo := layoutSpec.first(top)
	i, y := lo, float32(0)
	if lo < len(rows) {
		y = layoutSpec.tops[lo]
	}
	for ; i < len(rows) && y <= bottom; i++ {
		row := &rows[i]
		object := row.object
		before := row.size.Height
		resized := row.width != size.Width
		min := row.size
		// A row already shown at this width was measured by MinSize, or
		// by prepare when it changed: only the others are measured here.
		revived := !object.Visible()
		if revived || resized {
			object.Show() // it may have changed while out of sight
			min = layoutSpec.measureAt(i, size.Width)
		}
		if resized && y+before <= shown {
			layoutSpec.shift += min.Height - before
		}
		object.Move(fyne.NewPos(0, y))
		object.Resize(fyne.NewSize(size.Width, min.Height))
		row.size = min
		if revived {
			// Its text was only measured (or released) while hidden, and a
			// Resize to the size it already has lays nothing out: draw it
			// again, or it keeps its height and shows nothing (#497).
			object.Refresh()
		}
		y += min.Height + transcriptGap
	}
	hi := i
	hide := func(from, to int) {
		for j := from; j < to && j < len(rows); j++ {
			if (j < lo || j >= hi) && rows[j].object.Visible() {
				rows[j].object.Hide()
				releaseTranscriptText(rows[j].object)
			}
		}
	}
	hide(layoutSpec.lo, layoutSpec.hi)
	hide(layoutSpec.added, len(rows))
	layoutSpec.lo, layoutSpec.hi, layoutSpec.added = lo, hi, len(rows)
	layoutSpec.sum()
	layoutSpec.placeCards(lo, hi, size.Width)
	for object, coming := range layoutSpec.arrivals {
		if at, ok := layoutSpec.index[object]; ok && at < len(rows) && rows[at].object == object {
			coming.y, coming.laid = layoutSpec.tops[at], true
			layoutSpec.placeArrival(object, coming)
		}
	}
}

// Cards reach a little past their rows: the text keeps a margin inside.
const cardPadX, cardPadY = 14, 10

// placeCards puts one card behind each run of the agent's rows among the
// rows shown (lo to hi), from the top of its first row to the bottom of
// its last, the gaps between them included.
func (layoutSpec *transcriptLayout) placeCards(lo, hi int, width float32) {
	if layoutSpec.cards == nil {
		return
	}
	used := 0
	if !shaderBehind { // the plain background: the turns need no card
		lo = hi
	}
	card := func(top, bottom float32) {
		if used == len(layoutSpec.cardSet) {
			fill := canvas.NewRectangle(colBackground)
			fill.CornerRadius = rounded(14)
			fill.StrokeColor, fill.StrokeWidth = colBorderSoft, 1
			made := blurBehind("responses", fill, &colBackground)
			layoutSpec.cardSet = append(layoutSpec.cardSet, made)
			layoutSpec.cards.Add(made)
		}
		made := layoutSpec.cardSet[used]
		used++
		made.Move(fyne.NewPos(-cardPadX, top-cardPadY))
		made.Resize(fyne.NewSize(width+2*cardPadX, bottom-top+2*cardPadY))
		made.Show()
	}
	start, end := -1, -1
	for i := lo; i < hi && i < len(layoutSpec.rows); i++ {
		row := layoutSpec.rows[i]
		if layoutSpec.apart[row.object] || !row.object.Visible() || row.size.Height <= 0 {
			if start >= 0 {
				card(layoutSpec.tops[start], layoutSpec.tops[end]+layoutSpec.rows[end].size.Height)
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
		end = i
	}
	if start >= 0 {
		card(layoutSpec.tops[start], layoutSpec.tops[end]+layoutSpec.rows[end].size.Height)
	}
	for _, extra := range layoutSpec.cardSet[used:] {
		extra.Hide()
	}
}

// remeasure measures, a few milliseconds a frame, the items a resize left
// at an earlier width, and keeps what is on screen in place: the growth of
// the items above the view moves the scroll by as much.
func (ui *desktopUI) remeasure() {
	layoutSpec := ui.items
	if layoutSpec == nil || (!layoutSpec.stale && layoutSpec.shift == 0) || ui.loadingChat != nil || ui.scroll == nil {
		return
	}
	shown := float32(0)
	if origin, found := offsetIn(ui.scroll.Content, ui.transcript); found {
		shown = ui.scroll.Offset.Y - origin.Y
	}
	deadline := time.Now().Add(4 * time.Millisecond)
	width, y, done := layoutSpec.width, float32(0), true
	for i := range layoutSpec.rows {
		row := &layoutSpec.rows[i]
		if row.width != width && layoutSpec.stale {
			if time.Now().After(deadline) {
				done = false
				break
			}
			before := row.size.Height
			min := layoutSpec.measureAt(i, width)
			if y+before <= shown {
				layoutSpec.shift += min.Height - before
			}
		}
		y += row.size.Height + transcriptGap
	}
	layoutSpec.stale = layoutSpec.stale && !done
	if !ui.followTail {
		ui.scroll.Offset.Y += layoutSpec.shift
	}
	layoutSpec.shift = 0
	ui.relayoutColumn()
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
	ui = ui.viewOf(chat) // the window showing it (windows.go)
	if ui.cachedChat != nil && ui.cachedChat.chat == chat {
		ui.cachedChat = nil
	}
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
	ui = ui.viewOf(chat) // the window showing it (windows.go)
	if chat != ui.current {
		return nil
	}
	if load := ui.loadingChat; load != nil {
		load.checked = min(load.checked, index)
		return nil // changing messages are reconciled before the chat is shown
	}
	return ui.views[index]
}

// relayoutColumn lays the conversation out again after messages were added
// or grew, and keeps the newest line in view when the reader follows it.
// Each level of the column is laid out on its own: Container.Refresh would
// redraw every message, and a resize alone does nothing while the chat is
// shorter than the window (the activity line then stayed over the answer).
func (ui *desktopUI) relayoutColumn() {
	if ui.loadingChat != nil {
		return
	}
	defer platform.DevSlow("laying the conversation out", 6*time.Millisecond, time.Now())
	ui.syncNextStep()
	content := ui.scroll.Content.(*fyne.Container)
	// The column, over the selectCatcher.
	column := content.Objects[len(content.Objects)-1].(*fyne.Container)
	relayout(ui.transcript) // first: it measures the items the sizes below are made of
	content.Resize(content.MinSize().Max(ui.scroll.Size()))
	for _, c := range []*fyne.Container{content, column, ui.column, ui.transcriptBox, ui.transcript} {
		if c == nil {
			continue
		}
		c.Layout.Layout(c.Objects, c.Size())
	}
	if ui.followTail {
		ui.scroll.ScrollToBottom()
	} else {
		ui.scroll.ScrollToOffset(ui.scroll.Offset) // the bars follow the new height
	}
	ui.syncFooterFade()     // the height changed without a scroll event
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
	home      *desktopUI // the window it started in; view() is where its chat is now
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

// view is the window showing the turn's chat, else the one it started in:
// the chat may have been opened in another window since.
func (currentTurn *turn) view() *desktopUI { return currentTurn.home.viewOf(currentTurn.chat) }

func (ui *desktopUI) newTurn(chat *conversation, cfg agent.Config) *turn {
	return &turn{home: ui, chat: chat, cfg: cfg, index: -1, calls: map[string]int{}, states: map[int]string{}}
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
					if currentTurn.view().ctx.Err() == nil && currentTurn.index == index {
						currentTurn.render(true)
					}
				})
			})
		}
		return
	}
	currentTurn.lastParse = time.Now()
	currentTurn.view().remote.itemChanged(currentTurn.chat, currentTurn.index)
	if view, ok := currentTurn.view().liveView(currentTurn.chat, currentTurn.index).(*markdownView); ok {
		view.ParseMarkdown(currentTurn.chat.Items[currentTurn.index].Text)
		currentTurn.view().items.changed(view.box) // measured again even if the reader is elsewhere in the chat
		currentTurn.view().relayoutColumn()
	}
}

// setRow changes a tool row's state, on screen too when it is shown.
func (currentTurn *turn) setRow(index int, state string) {
	currentTurn.states[index] = state
	currentTurn.view().remote.itemChanged(currentTurn.chat, index)
	if row, ok := currentTurn.view().liveView(currentTurn.chat, index).(*toolRow); ok {
		row.update(currentTurn.chat.Items[index], state)
		if row.preview != nil {
			currentTurn.view().items.changed(row)
			currentTurn.view().relayoutColumn()
		}
	}
}

func (currentTurn *turn) handle(uiEventValue uiEvent) {
	ui := currentTurn.view()
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
		// Another call still running keeps its words: the model only
		// takes over once every call it sent has its result.
		for _, state := range currentTurn.states {
			if state == "running" {
				return
			}
		}
		currentTurn.show(agent.Activity{State: "wait"})
	case "injected":
		messageValue := uiEventValue.data.(agent.Message)
		currentTurn.chat.hops = agent.TurnHops(currentTurn.chat.hops, true, messageValue)
		ui.addItem(currentTurn.chat, agent.MessageItem("", messageValue))
		if currentTurn.chat == ui.current { // the answer before it is now complete
			ui.syncCopies()
			ui.relayoutColumn()
		}
		ui.save()
	case "history":
		currentTurn.chat.History = uiEventValue.data.([]agent.Message)
	case "cache", "compaction":
		ui.save()
	case "context_note", "note":
		ui.addItem(currentTurn.chat, agent.ChatItem{Kind: "note", Text: uiEventValue.data.(string)})
		ui.save()
	case "todo":
		ui.todoUpdated(currentTurn.chat)
		ui.save()
	case "goal": // its state, time or tokens changed (goal.go)
		ui.goalUpdated(currentTurn.chat)
	case "usage":
		currentTurn.usage = uiEventValue.data.(int)
		if currentTurn.cfg.Provider == "claudecode" { // its own count comes when the turn ends
			currentTurn.chat.ContextUsage = currentTurn.chat.ContextUsage.At(currentTurn.usage)
		}
		ui.remote.metaChanged(currentTurn.chat)
		if currentTurn.chat == ui.current {
			ui.setContextUsage(uiEventValue.data.(int), agent.ModelContextWindow(currentTurn.cfg))
		}
	case "context_usage":
		usage := uiEventValue.data.(agent.ContextUsage)
		currentTurn.chat.ContextUsage = &usage
		ui.remote.metaChanged(currentTurn.chat)
		if currentTurn.chat == ui.current && ui.panel != nil {
			ui.panel.refreshTodoStatus()
		}
		ui.save()
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
		currentTurn.chat.Config.SetClaudeSession(uiEventValue.data.(string))
		if currentTurn.chat == ui.current {
			ui.cfg.InheritSession(currentTurn.chat.Config)
		}
		ui.save()
	case "session_synced": // the session has seen everything shown so far
		currentTurn.chat.Config.SyncClaudeSession(len(currentTurn.chat.Items))
		if currentTurn.chat == ui.current {
			ui.cfg.InheritSession(currentTurn.chat.Config)
		}
		ui.save()
	case "plan":
		ui.plan = uiEventValue.data.(agent.PlanUsage)
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
			currentTurn.view().panel.changed(currentTurn.chat, index)
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
		label = agent.CallLabel(activityValue.Tool, activityValue.Detail)
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
	if activityValue.State == "call" {
		currentTurn.mark = agent.CallMark(activityValue.Tool)
	}
	if activityValue.State == "question" {
		currentTurn.mark = "approval" // the ring's waiting-for-you glyph
	}
	currentTurn.view().applyActivity(currentTurn)
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
	default: // what the conversation's row says (agent.DescribeTool)
		verb, target, _ := agent.DescribeTool(agent.ChatItem{Tool: outcome.Name, Args: outcome.Args}, true)
		currentTurn.label, currentTurn.mark = strings.TrimSpace(verb+" "+target)+"…", agent.CallMark(outcome.Name)
	}
	currentTurn.view().applyActivity(currentTurn)
}

// applyActivity shows a turn's activity when its chat is on screen.
func (ui *desktopUI) applyActivity(currentTurn *turn) {
	ui = ui.viewOf(currentTurn.chat)
	ui.remote.metaChanged(currentTurn.chat)
	ui.syncPresence()
	if currentTurn.chat != ui.current || currentTurn.mark == "" {
		return
	}
	label := currentTurn.label
	if run := currentTurn.chat.run; run != nil && run.questions != nil && !run.questions.shown() {
		label = "Waiting for your answer: click here to see the questions"
	}
	ui.thinkLabel.SetText(label)
	ui.workMark.setState(currentTurn.mark)
}

// runState is how far a tool call has got: "done" unless its chat's run is
// still working on it.
func runState(chat *conversation, index int) string {
	if chat.server != nil && chat.states[index] != "" {
		return chat.states[index]
	}
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
			if currentTurn.waiting != turn || currentTurn.view().ctx.Err() != nil {
				return
			}
			seconds := int(time.Since(started).Seconds())
			spent := fmt.Sprintf("%d s", seconds)
			if seconds >= 60 {
				spent = fmt.Sprintf("%d min %02d s", seconds/60, seconds%60)
			}
			currentTurn.label = fmt.Sprintf("Waiting for %s… %s", what, spent)
			currentTurn.mark = "wait"
			currentTurn.view().applyActivity(currentTurn)
			time.AfterFunc(time.Second, tick)
		})
	}
	if after == 0 { // shown at once: the line must not keep saying "Writing"
		currentTurn.label, currentTurn.mark = "Waiting for "+what+"…", "wait"
		currentTurn.view().applyActivity(currentTurn)
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
