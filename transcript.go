package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
)

type chatItem struct {
	ID     string      `json:"id,omitempty"`    // stable prompt identity across edited versions
	Kind   string      `json:"kind"`            // user, assistant, error, note or tool
	Title  string      `json:"title,omitempty"` // tool rows saved by earlier versions
	Text   string      `json:"text"`            // the message, or a tool's output
	Images []ImagePart `json:"images,omitempty"`
	Tool   string      `json:"tool,omitempty"`   // tool rows: the tool's name
	Args   string      `json:"args,omitempty"`   // tool rows: the call's JSON arguments
	Diff   string      `json:"diff,omitempty"`   // edit_file rows: the diff, once shown for approval
	Millis int64       `json:"millis,omitempty"` // tool rows: execution time
	Failed string      `json:"failed,omitempty"` // tool rows: the error, when the call failed
}

// upgradeItems rewrites tool rows saved by earlier versions, which stored an
// edit as two rows ("View changes", then "edit_file · result") and every
// other call as a bare "<tool> · result" row.
func upgradeItems(items []chatItem) []chatItem {
	out := make([]chatItem, 0, len(items))
	for i := 0; i < len(items); i++ {
		item := items[i]
		if item.Kind == "user" && item.ID == "" {
			item.ID = identifier()[:16]
		}
		switch {
		case item.Kind == "diff":
			item = chatItem{Kind: "tool", Tool: "edit_file", Diff: item.Text}
			if i+1 < len(items) && items[i+1].Kind == "tool" && items[i+1].Title == "edit_file · result" {
				item.Text = items[i+1].Text
				i++
			}
		case item.Kind == "tool" && item.Tool == "" && item.Title == "Context compacted":
			item = chatItem{Kind: "note", Text: item.Text}
		case item.Kind == "tool" && item.Tool == "":
			item.Tool = strings.TrimSuffix(item.Title, " · result")
			item.Title = ""
		}
		out = append(out, item)
	}
	return out
}

// toolArgs reads the arguments every tool shares.
func toolArgs(raw string) (path, command string, creates bool) {
	var args struct {
		Path    string  `json:"path"`
		Command string  `json:"command"`
		Old     *string `json:"old_text"`
	}
	_ = json.Unmarshal([]byte(raw), &args)
	return args.Path, args.Command, args.Old != nil && *args.Old == ""
}

func toolGlyph(tool string) fyne.Resource {
	switch tool {
	case "shell":
		return iconTerminal
	case "edit_file":
		return iconPencil
	case "list_directory":
		return iconFolder
	case "update_todo", "TodoWrite", "mcp__1loop_todo__update_todo":
		return iconCheck
	}
	return iconDocument
}

// describeTool is the one-line summary of a call: "Ran go test ./..." or
// "Editing main.go". The target is the command's first line or the path.
func describeTool(item chatItem, running bool) (verb, target string, mono bool) {
	path, command, creates := toolArgs(item.Args)
	pick := func(done, doing string) string {
		if running {
			return doing
		}
		return done
	}
	switch item.Tool {
	case "shell":
		first, _, multi := strings.Cut(strings.TrimSpace(command), "\n")
		if multi {
			first += " …"
		}
		return pick("Ran", "Running"), first, true
	case "read_file":
		return pick("Read", "Reading"), path, false
	case "list_directory":
		if path == "" || path == "." {
			path = "workspace root"
		}
		return pick("Listed", "Listing"), path, false
	case "edit_file":
		if path == "" { // rows saved before arguments were kept
			path = diffPath(item.Diff)
		}
		if creates {
			return pick("Created", "Creating"), path, false
		}
		return pick("Edited", "Editing"), path, false
	}
	// A tool of another harness (Claude Code's Grep, WebFetch, Agent…):
	// its name and the argument that says what it is about.
	var args map[string]any
	_ = json.Unmarshal([]byte(item.Args), &args)
	for _, key := range []string{"pattern", "query", "url", "description", "skill", "subject", "prompt"} {
		if text, ok := args[key].(string); ok && text != "" {
			target = firstLine(text)
			break
		}
	}
	switch item.Tool {
	case "Grep", "Glob":
		return pick("Searched", "Searching"), target, true
	case "WebSearch":
		return pick("Searched the web for", "Searching the web for"), target, false
	case "WebFetch":
		return pick("Fetched", "Fetching"), target, false
	case "Task", "Agent":
		return pick("Ran an agent:", "Running an agent:"), target, false
	case "TodoWrite", "update_todo", "mcp__1loop_todo__update_todo":
		return pick("Updated", "Updating"), "the task list", false
	}
	if target == "" {
		return pick("Used", "Using"), item.Tool, false
	}
	return pick("Used", "Using") + " " + item.Tool + ":", target, false
}

func diffPath(diff string) string {
	for _, line := range strings.SplitN(diff, "\n", 3) {
		if strings.HasPrefix(line, "+++ b/") {
			return strings.TrimPrefix(line, "+++ b/")
		}
	}
	return "file"
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

// ---------------------------------------------------------------------------
// toolRow is one tool call in the transcript: what was done and its target
// at a glance (a shell command shows its first line), with the outcome at
// the end. A click opens the call in the side panel.

type toolRow struct {
	widget.BaseWidget
	chat     *conversation
	index    int // of the item in chat.Items
	item     chatItem
	state    string // "pending" (awaiting approval), "running" or "done"
	selected bool
	onTap    func()
	hover    float32
	anim     *fyne.Animation
}

func newToolRow(item chatItem, state string, onTap func()) *toolRow {
	r := &toolRow{item: item, state: state, onTap: onTap}
	r.ExtendBaseWidget(r)
	return r
}

func (r *toolRow) update(item chatItem, state string) {
	r.item, r.state = item, state
	r.Refresh()
}

func (r *toolRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}
func (r *toolRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (r *toolRow) MouseIn(*desktop.MouseEvent)    { r.fade(1) }
func (r *toolRow) MouseMoved(*desktop.MouseEvent) {}
func (r *toolRow) MouseOut()                      { r.fade(0) }

func (r *toolRow) fade(to float32) {
	if r.anim != nil {
		r.anim.Stop()
	}
	from := r.hover
	r.anim = fyne.NewAnimation(130*time.Millisecond, func(p float32) {
		r.hover = from + (to-from)*p
		r.Refresh()
	})
	r.anim.Curve = fyne.AnimationEaseOut
	r.anim.Start()
}

func (r *toolRow) CreateRenderer() fyne.WidgetRenderer {
	rr := &toolRowRenderer{row: r, bg: canvas.NewRectangle(color.Transparent), icon: canvas.NewImageFromResource(nil),
		verb: canvas.NewText("", colMuted), target: canvas.NewText("", colText),
		meta: canvas.NewText("", colFaint), minus: canvas.NewText("", colDanger)}
	rr.bg.CornerRadius = 8
	rr.icon.FillMode = canvas.ImageFillContain
	for _, t := range []*canvas.Text{rr.verb, rr.target, rr.meta, rr.minus} {
		t.TextSize = 13.5
	}
	rr.meta.TextSize, rr.minus.TextSize = 12.5, 12.5
	rr.Refresh()
	return rr
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

func (rr *toolRowRenderer) MinSize() fyne.Size {
	height := fyne.MeasureText("Ag", 13.5, fyne.TextStyle{}).Height + 12
	return fyne.NewSize(120, height)
}

func (rr *toolRowRenderer) Layout(size fyne.Size) {
	rr.bg.Resize(size)
	middle := func(t *canvas.Text) float32 { return (size.Height - t.MinSize().Height) / 2 }
	rr.icon.Resize(fyne.NewSquareSize(toolRowIcon))
	rr.icon.Move(fyne.NewPos(toolRowPadX, (size.Height-toolRowIcon)/2))
	x := toolRowPadX + toolRowIcon + 8
	rr.verb.Move(fyne.NewPos(x, middle(rr.verb)))
	x += rr.verb.MinSize().Width + 6
	metaWidth := float32(0)
	if rr.meta.Text != "" {
		metaWidth = rr.meta.MinSize().Width + 10
	}
	if rr.minus.Text != "" {
		metaWidth += rr.minus.MinSize().Width + 5
	}
	rr.target.Text = fitText(rr.full, rr.target.TextSize, rr.target.TextStyle, size.Width-x-metaWidth-toolRowPadX)
	rr.target.Move(fyne.NewPos(x, middle(rr.target)))
	x += rr.target.MinSize().Width + 10
	rr.meta.Move(fyne.NewPos(x, middle(rr.meta)))
	rr.minus.Move(fyne.NewPos(x+rr.meta.MinSize().Width+5, middle(rr.minus)))
}

func (rr *toolRowRenderer) Refresh() {
	r := rr.row
	item := r.item
	running := r.state == "pending" || r.state == "running"
	verb, target, mono := describeTool(item, running)
	rr.verb.Text = verb
	rr.full = target
	rr.target.TextStyle = fyne.TextStyle{Monospace: mono}
	rr.target.TextSize = 13.5
	if mono {
		rr.target.TextSize = 12.5
	}
	rr.meta.Text, rr.minus.Text = "", ""
	rr.meta.Color = colFaint
	switch {
	case r.state == "pending":
		rr.meta.Text = "Awaiting approval"
		rr.meta.Color = colAmber
	case running:
	case item.Failed == stoppedCall:
		rr.meta.Text = "Stopped"
	case item.Failed != "":
		rr.meta.Text = shortError(item.Failed)
		rr.meta.Color = colDanger
	case item.Tool == "edit_file" && item.Diff != "":
		added, removed := diffStat(item.Diff)
		rr.meta.Text = fmt.Sprintf("+%d", added)
		rr.meta.Color = colAdded
		rr.minus.Text = fmt.Sprintf("−%d", removed)
	case item.Tool == "shell" && item.Millis > 0:
		rr.meta.Text = elapsed(item.Millis)
	}
	h := quantize(r.hover, 4)
	rr.verb.Color = mix(colMuted, colText, h*0.5)
	rr.bg.FillColor = mix(color.NRGBA{}, colHover, r.hover)
	if r.selected {
		rr.bg.FillColor = mix(colSelected, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x18}, r.hover)
	}
	if glyph := toolGlyph(item.Tool); glyph != rr.glyph {
		rr.glyph = glyph
		rr.icon.Resource = tinted(glyph, colMuted)
		rr.icon.Refresh()
	}
	rr.Layout(r.Size())
	for _, o := range rr.Objects() {
		o.Refresh()
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

func (rr *toolRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.bg, rr.icon, rr.verb, rr.target, rr.meta, rr.minus}
}
func (rr *toolRowRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// Transcript

// bubbleLayout sizes a user message to its text and pins it to the right.
type bubbleLayout struct {
	text  string
	chrom float32
}

func (l bubbleLayout) width(available float32) float32 {
	natural := float32(0)
	for _, line := range strings.Split(l.text, "\n") {
		natural = fyne.Max(natural, fyne.MeasureText(line, theme.TextSize(), fyne.TextStyle{}).Width)
	}
	return fyne.Min(natural+l.chrom+2, available*0.78)
}

func (l bubbleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := l.width(size.Width)
	objects[0].Resize(fyne.NewSize(width, size.Height))
	objects[0].Resize(fyne.NewSize(width, objects[0].MinSize().Height))
	objects[0].Move(fyne.NewPos(size.Width-width, 0))
}

func (l bubbleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, objects[0].MinSize().Height)
}

// appendItem draws chat.Items[index] at the end of the transcript. Answers
// and tool rows are remembered in u.views, so a running task can update the
// one it is working on while its chat is on screen.
func (u *desktopUI) appendItem(chat *conversation, index int) {
	item := chat.Items[index]
	var t *turn
	if chat.run != nil {
		t = chat.run.turn
	}
	switch item.Kind {
	case "assistant":
		view := newMarkdownView(item.Text)
		copyRow := u.responseControls(chat, index)
		copyRow.Hide() // see syncCopies
		u.views[index] = view
		u.copyRows[index] = copyRow
		view.layer = newSelectLayer(u, view.box)
		u.place(container.NewVBox(container.NewStack(view.box, view.layer), copyRow))
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
		if item.Text != "" {
			label := widget.NewLabel(item.Text)
			label.Wrapping = fyne.TextWrapWord
			label.Selectable = true
			content.Add(label)
		}
		bubble := container.New(bubbleLayout{text: item.Text, chrom: 2*theme.InnerPadding() + 4}, surface(content, colRaised, nil, 14, 2))
		copyRow := container.NewHBox(layout.NewSpacer(), copyChip(func() string { return item.Text }),
			newRow("Edit", iconPencil, styleChip, func() { u.editMessage(chat, index) }), u.versionControls(chat, item.ID))
		u.place(container.New(layout.NewCustomPaddedVBoxLayout(2), bubble, copyRow))
	case "error":
		label := widget.NewLabel(item.Text)
		label.Wrapping = fyne.TextWrapWord
		label.Importance = widget.DangerImportance
		u.place(container.NewVBox(label, u.responseControls(chat, index)))
	case "note":
		icon := canvas.NewImageFromResource(tinted(iconCompact, colFaint))
		icon.SetMinSize(fyne.NewSquareSize(15))
		u.place(container.NewHBox(layout.NewSpacer(), icon, textLine(item.Text, 12.5, colFaint), layout.NewSpacer()))
	case "tool":
		state := "done"
		if t != nil && t.states[index] != "" {
			state = t.states[index]
		}
		row := newToolRow(item, state, nil)
		row.chat, row.index = chat, index
		row.onTap = func() { u.panel.open(chat, index) }
		row.selected = u.panel.showing(chat, index)
		u.views[index] = row
		u.place(row)
	}
}

// place adds an item's view to the conversation. While a whole chat is
// being built (openChat) nothing is laid out before the last one:
// Container.Add lays everything out each time, which made opening a chat
// quadratic (0.7 s for 240 items).
func (u *desktopUI) place(view fyne.CanvasObject) {
	if u.building {
		u.transcript.Objects = append(u.transcript.Objects, view)
		return
	}
	u.transcript.Add(view)
}

// transcriptLayout stacks the conversation's items like a VBox, but keeps
// only those around the viewport visible. Fyne measures every visible
// object on each frame it draws, so in a long conversation every frame of
// an animation, every scroll step and every streamed word paid for the
// whole history. A hidden item costs nothing: it keeps its place with the
// size it last measured, and is measured again when it comes back in view.
type transcriptLayout struct {
	u     *desktopUI
	sizes map[fyne.CanvasObject]fyne.Size // each item's last measured minimum size
	width float32                         // the width they were measured at
}

const transcriptGap = 14

func (l *transcriptLayout) reset() { l.sizes = map[fyne.CanvasObject]fyne.Size{} }

// changed forgets the size of the item that holds part: it changed while
// perhaps out of sight, where nothing measures it.
func (l *transcriptLayout) changed(part fyne.CanvasObject) {
	for item := range l.sizes {
		if _, holds := offsetIn(item, part); holds {
			delete(l.sizes, item)
			return
		}
	}
}

// measure sizes an item at the column's width: wrapped text only knows its
// height once it has its width.
func (l *transcriptLayout) measure(o fyne.CanvasObject, width float32) fyne.Size {
	if o.Size().Width != width {
		o.Resize(fyne.NewSize(width, o.Size().Height))
	}
	min := o.MinSize()
	l.sizes[o] = min
	return min
}

// view is the band of the column worth keeping alive, in the transcript's
// coordinates: the viewport and a screen on each side, so a scroll step
// always lands on items that are already there.
func (l *transcriptLayout) view() (top, bottom float32) {
	scroll := l.u.scroll
	if scroll == nil {
		return -1e9, 1e9
	}
	origin, found := offsetIn(scroll.Content, l.u.transcript)
	if !found {
		return -1e9, 1e9
	}
	height := scroll.Size().Height
	margin := fyne.Max(height, 600)
	offset := scroll.Offset.Y - origin.Y
	return offset - margin, offset + height + margin
}

func (l *transcriptLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(120, 0)
	for i, o := range objects {
		min, known := l.sizes[o]
		if o.Visible() || !known {
			min = o.MinSize()
		}
		size.Width = fyne.Max(size.Width, min.Width)
		size.Height += min.Height
		if i > 0 {
			size.Height += transcriptGap
		}
	}
	return size
}

func (l *transcriptLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if size.Width <= 0 {
		return // not on screen yet: nothing can be measured
	}
	resized := size.Width != l.width
	l.width = size.Width
	top, bottom := l.view()
	y := float32(0)
	for _, o := range objects {
		min, known := l.sizes[o]
		if o.Visible() || !known || resized {
			min = l.measure(o, size.Width)
		}
		near := y+min.Height >= top && y <= bottom
		switch {
		case near && !o.Visible():
			o.Show()
			min = l.measure(o, size.Width) // it may have changed while out of sight
		case !near && o.Visible():
			o.Hide()
		}
		o.Move(fyne.NewPos(0, y))
		if near {
			o.Resize(fyne.NewSize(size.Width, min.Height))
		}
		y += min.Height + transcriptGap
	}
}

// syncCopies shows one Copy button per finished answer: under the last
// text of each turn, never between the texts and tool calls of a turn, and
// not while the turn is still running.
func (u *desktopUI) syncCopies() {
	chat := u.current
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
	for index, row := range u.copyRows {
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
			u.items.changed(row)
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
func (u *desktopUI) addItem(chat *conversation, item chatItem) int {
	if item.Kind == "user" && item.ID == "" {
		item.ID = identifier()[:16]
	}
	chat.Items = append(chat.Items, item)
	index := len(chat.Items) - 1
	u.remote.itemChanged(chat, index)
	if chat == u.current {
		u.appendItem(chat, index)
		u.relayoutColumn()
	}
	return index
}

// liveView is the widget showing chat.Items[index], if it is on screen.
func (u *desktopUI) liveView(chat *conversation, index int) any {
	if chat != u.current {
		return nil
	}
	return u.views[index]
}

// relayoutColumn lays the conversation out again after messages were added
// or grew, and keeps the newest line in view when the reader follows it.
// Each level of the column is laid out on its own: Container.Refresh would
// redraw every message, and a resize alone does nothing while the chat is
// shorter than the window (the activity line then stayed over the answer).
func (u *desktopUI) relayoutColumn() {
	defer devSlow("laying the conversation out", 6*time.Millisecond, time.Now())
	content := u.scroll.Content.(*fyne.Container)
	relayout(u.transcript) // first: it measures the items the sizes below are made of
	content.Resize(content.MinSize().Max(u.scroll.Size()))
	for _, c := range []*fyne.Container{content, u.column, u.transcript} {
		c.Layout.Layout(c.Objects, c.Size())
	}
	if u.followTail {
		u.scroll.ScrollToBottom()
	} else {
		u.scroll.ScrollToOffset(u.scroll.Offset) // the bars follow the new height
	}
	relayout(u.transcript) // the view moved without a scroll event: bring its items in
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

func (p *eventPump) emit(kind string, data any) {
	p.mu.Lock()
	merged := false
	if n := len(p.events); n > 0 {
		last := &p.events[n-1]
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
		p.events = append(p.events, uiEvent{kind, data})
	}
	schedule := !p.scheduled
	p.scheduled = true
	p.mu.Unlock()
	if schedule {
		fyne.Do(p.flush)
	}
}

// flush handles everything received so far, on the UI thread.
func (p *eventPump) flush() {
	p.mu.Lock()
	events := p.events
	p.events, p.scheduled = nil, false
	p.mu.Unlock()
	started := time.Now()
	for _, e := range events {
		devEvent(e)
		p.handle(e)
	}
	if dev != nil {
		if took := time.Since(started); took > 8*time.Millisecond {
			devf("perf", "slow: handling %d agent events took %s (first: %s)", len(events), took.Round(100*time.Microsecond), events[0].kind)
		}
	}
}

// devEvent logs one event of an agent loop as the interface receives it.
func devEvent(e uiEvent) {
	if dev == nil {
		return
	}
	switch data := e.data.(type) {
	case string:
		if e.kind == "delta" {
			devf("run", "delta, %d characters", len(data))
		} else {
			devf("run", "%s %s", e.kind, devText(data, 120))
		}
	case activity:
		devf("run", "activity %s %s %s", data.State, data.Tool, devText(data.Detail, 80))
	case toolOutcome:
		failure := ""
		if data.Err != nil {
			failure = ", error: " + devText(data.Err.Error(), 80)
		}
		devf("run", "%s %s %s, output %d bytes, %s%s", e.kind, data.Name, devText(data.Args, 100), len(data.Output), data.Elapsed.Round(time.Millisecond), failure)
	case message:
		devf("run", "%s %s: %s", e.kind, data.Role, devText(data.Content, 100))
	case []message:
		devf("run", "%s, %d messages", e.kind, len(data))
	case map[string]any:
		devf("run", "%s %v", e.kind, devText(fmt.Sprint(data["title"]), 100))
	default:
		devf("run", "%s %v", e.kind, data)
	}
}

// turn follows one run of the agent loop. It works on the chat's data and
// touches widgets only while that chat is on screen, so the user can go
// elsewhere while the agent works.
type turn struct {
	u         *desktopUI
	chat      *conversation
	cfg       config
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

func (u *desktopUI) newTurn(chat *conversation, cfg config) *turn {
	return &turn{u: u, chat: chat, cfg: cfg, index: -1, calls: map[string]int{}, states: map[int]string{}}
}

// render re-parses the streaming answer at most every 60 ms.
func (t *turn) render(force bool) {
	if t.index < 0 {
		return
	}
	if !force && time.Since(t.lastParse) < 60*time.Millisecond {
		if !t.parseDue {
			t.parseDue = true
			index := t.index
			time.AfterFunc(60*time.Millisecond, func() {
				fyne.Do(func() {
					t.parseDue = false
					if t.u.ctx.Err() == nil && t.index == index {
						t.render(true)
					}
				})
			})
		}
		return
	}
	t.lastParse = time.Now()
	t.u.remote.itemChanged(t.chat, t.index)
	if view, ok := t.u.liveView(t.chat, t.index).(*markdownView); ok {
		view.ParseMarkdown(t.chat.Items[t.index].Text)
		t.u.items.changed(view.box) // measured again even if the reader is elsewhere in the chat
		t.u.relayoutColumn()
	}
}

// setRow changes a tool row's state, on screen too when it is shown.
func (t *turn) setRow(index int, state string) {
	t.states[index] = state
	t.u.remote.itemChanged(t.chat, index)
	if row, ok := t.u.liveView(t.chat, index).(*toolRow); ok {
		row.update(t.chat.Items[index], state)
	}
}

func (t *turn) handle(e uiEvent) {
	u := t.u
	if u.ctx.Err() != nil {
		return
	}
	switch e.kind {
	case "delta":
		if t.index < 0 {
			t.index = len(t.chat.Items) // set first: the new view must know it streams
			u.addItem(t.chat, chatItem{Kind: "assistant"})
		}
		t.chat.Items[t.index].Text += e.data.(string)
		t.render(false)
		t.show(activity{State: "write"})
	case "message_end":
		t.render(true)
		t.index = -1
	case "activity":
		t.show(e.data.(activity))
	case "tool":
		o := e.data.(toolOutcome)
		index := u.addItem(t.chat, chatItem{Kind: "tool", Tool: o.Name, Args: o.Args})
		t.calls[o.ID], t.lastCall = index, o.ID
		t.setRow(index, "running")
	case "approval":
		p := e.data.(map[string]any)
		if index, ok := t.calls[t.lastCall]; ok {
			t.setRow(index, "pending")
		}
		u.showApproval(t.chat, p)
		t.show(activity{State: "approval", Detail: approvalSubject(p)})
	case "tool_exec":
		o := e.data.(toolOutcome)
		if index, ok := t.calls[o.ID]; ok {
			t.setRow(index, "running")
		}
		t.showRun(o)
	case "tool_result":
		o := e.data.(toolOutcome)
		index, ok := t.calls[o.ID]
		if !ok {
			return
		}
		item := &t.chat.Items[index]
		item.Text, item.Diff, item.Millis = o.Output, o.Diff, o.Elapsed.Milliseconds()
		if o.Err != nil {
			item.Failed = o.Err.Error()
		}
		t.setRow(index, "done")
		delete(t.states, index)
		u.panel.changed(t.chat, index)
		t.show(activity{State: "wait"})
	case "injected":
		m := e.data.(message)
		u.addItem(t.chat, chatItem{Kind: "user", Text: m.Content, Images: m.Images})
		if t.chat == u.current { // the answer before it is now complete
			u.syncCopies()
			u.relayoutColumn()
		}
		u.save()
	case "history":
		t.chat.History = e.data.([]message)
	case "todo":
		u.todoUpdated(t.chat)
		u.save()
	case "usage":
		t.usage = e.data.(int)
		u.remote.metaChanged(t.chat)
		if t.chat == u.current {
			u.setContextUsage(e.data.(int), modelContextWindow(t.cfg))
		}
	case "notice":
		t.label = e.data.(string)
		u.applyActivity(t)
	case "session": // Claude Code named the chat's session: later turns resume it
		t.chat.Config.Session = e.data.(string)
		if t.chat == u.current {
			u.cfg.Session = t.chat.Config.Session
		}
		u.save()
	case "plan":
		u.plan = e.data.(planUsage)
		if t.chat == u.current {
			u.contextRing.plan = u.plan
		}
	}
}

// finish closes the turn: calls left without a result were stopped.
func (t *turn) finish() {
	t.waiting++
	t.render(true)
	t.index = -1
	for index, state := range t.states {
		if state != "done" {
			t.chat.Items[index].Failed = stoppedCall
			t.setRow(index, "done")
			t.u.panel.changed(t.chat, index)
		}
	}
	t.states = map[int]string{}
}

// state is how far a call of this turn has got, for the side panel.
func (t *turn) state(chat *conversation, index int) string {
	if t == nil || t.chat != chat || t.states[index] == "" {
		return "done"
	}
	return t.states[index]
}

func approvalSubject(p map[string]any) string {
	title, _ := p["title"].(string)
	return strings.TrimSuffix(title, "?")
}

// show tells, under the conversation, what the agent is doing.
func (t *turn) show(a activity) {
	t.waiting++ // the command is over: no more counting
	label := map[string]string{
		"wait":    "Waiting for the model",
		"think":   "Thinking",
		"write":   "Writing",
		"compact": "Compacting the conversation",
	}[a.State]
	switch a.State {
	case "call":
		detail := firstLine(a.Detail)
		switch a.Tool {
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
		default:
			label = "Preparing " + a.Tool
		}
		if detail != "" {
			label += ": " + detail
		}
	case "approval":
		label = "Waiting for your approval"
		if a.Detail != "" {
			label += ": " + a.Detail
		}
	case "background":
		// The answer is written; the agent now waits for a task it left
		// running (Claude Code's background commands and monitors).
		what := "the background task"
		if a.Detail != "" {
			what += " “" + firstLine(a.Detail) + "”"
		}
		if strings.HasSuffix(a.Detail, "background tasks") {
			what = a.Detail
		}
		t.countWait(what, 0)
		return
	}
	t.label, t.mark = label+"…", a.State
	t.u.applyActivity(t)
}

// showRun reports a tool that has started to run.
func (t *turn) showRun(o toolOutcome) {
	path, command, _ := toolArgs(o.Args)
	switch o.Name {
	case "shell":
		t.label, t.mark = "Running "+firstLine(command)+"…", "bash"
		t.countWait(firstLine(command), longCommand)
	case "read_file":
		t.label, t.mark = "Reading "+path+"…", "read"
	case "list_directory":
		t.label, t.mark = "Listing "+path+"…", "list"
	case "edit_file":
		t.label, t.mark = "Applying the edit to "+path+"…", "edit"
	default: // a tool of another harness (Claude Code's Grep, WebFetch, Agent…)
		t.label, t.mark = "Using "+o.Name+"…", "call"
	}
	t.u.applyActivity(t)
}

// applyActivity shows a turn's activity when its chat is on screen.
func (u *desktopUI) applyActivity(t *turn) {
	u.remote.metaChanged(t.chat)
	if t.chat != u.current || t.mark == "" {
		return
	}
	u.thinkLabel.SetText(t.label)
	u.workMark.setState(t.mark)
}

func firstLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return first
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
func (t *turn) countWait(what string, after time.Duration) {
	t.waiting++
	turn, started := t.waiting, time.Now()
	var tick func()
	tick = func() {
		fyne.Do(func() {
			if t.waiting != turn || t.u.ctx.Err() != nil {
				return
			}
			seconds := int(time.Since(started).Seconds())
			spent := fmt.Sprintf("%d s", seconds)
			if seconds >= 60 {
				spent = fmt.Sprintf("%d min %02d s", seconds/60, seconds%60)
			}
			t.label = fmt.Sprintf("Waiting for %s… %s", what, spent)
			t.mark = "wait"
			t.u.applyActivity(t)
			time.AfterFunc(time.Second, tick)
		})
	}
	if after == 0 { // shown at once: the line must not keep saying "Writing"
		t.label, t.mark = "Waiting for "+what+"…", "wait"
		t.u.applyActivity(t)
		after = time.Second
	}
	time.AfterFunc(after, tick)
}
