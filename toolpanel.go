package main

import (
	"fmt"
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
)

// toolPanel is the foldable panel on the right of the conversation. Each
// tool call opened from the transcript gets a tab showing the call itself
// (command, path, diff), its full output and how long it ran.
type toolPanel struct {
	u      *desktopUI
	view   fyne.CanvasObject
	strip  *fyne.Container
	body   *kineticScroll
	page   *fyne.Container
	texts  []*outputView // the code wells of the call on display
	tabs   []panelTab
	active int
	shown  float32 // 0 folded .. 1 open, eased by slide
	target float32 // where the slide is going
	snap   bool    // the conversation is too long to follow the slide frame by frame
	anim   *fyne.Animation
}

type panelTab struct {
	chat     *conversation
	index    int
	title    string
	icon     fyne.Resource
	content  fyne.CanvasObject
	terminal *panelTerminal
	cancel   func()
	todoChat *conversation
	todoView *fyne.Container
}

const panelWidth = 840

func newToolPanel(u *desktopUI) *toolPanel {
	p := &toolPanel{u: u, active: -1}
	p.strip = container.New(layout.NewCustomPaddedHBoxLayout(4))
	add := newRow("", iconPlus, styleWindow, nil)
	add.onTap = func() {
		showMenu(u.window, add, []menuAction{
			{label: "Terminal", icon: toolGlyph("shell"), run: p.openTerminal},
			{label: "Modifications Git", icon: toolGlyph("edit_file"), run: p.openChanges},
		})
	}
	fold := newRow("", iconPanel, styleWindow, p.fold)
	todo := newRow("TODO", iconCheck, styleChip, p.openTodo)
	header := container.NewBorder(nil, nil, nil, container.NewHBox(todo, add, fold), container.NewHScroll(p.strip))
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	p.body = newKineticVScroll(spacer(0, 0))
	p.body.OnScrolled = func(fyne.Position) { p.place() }
	p.page = container.NewStack(p.body)
	card := canvas.NewRectangle(colSurface)
	card.CornerRadius = 12
	card.StrokeColor = colBorderSoft
	card.StrokeWidth = 1
	content := container.NewBorder(container.NewVBox(padded(8, 8, 8, 6, header), rule), nil, nil, nil, p.page)
	p.view = padded(0, 14, 0, 14, container.NewStack(card, content))
	p.render()
	return p
}

// splitLayout gives the panel its slice of the width as it slides in.
type splitLayout struct{ p *toolPanel }

func (l splitLayout) width(total float32) float32 {
	width := l.p.u.panelWidth
	if width <= 0 {
		width = fyne.Min(panelWidth, total*0.58)
	}
	return panelWidthWithin(width, total)
}

func panelWidthWithin(width, total float32) float32 {
	return fyne.Max(320, fyne.Min(width, total-360))
}

func (l splitLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (l splitLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	full := l.width(size.Width)
	width := full * l.p.shown
	conversationWidth := size.Width - width
	if l.p.snap {
		conversationWidth = size.Width - full*l.p.target
	}
	l.p.u.controls.Layout.(*composerControlsLayout).available = fyne.Min(760, conversationWidth-48) - 20
	if l.p.snap {
		// Wrapping a long conversation again on every frame is what made
		// the slide stutter: it takes its final width at once instead.
		objects[0].Resize(fyne.NewSize(size.Width-full*l.p.target, size.Height))
	} else {
		objects[0].Resize(fyne.NewSize(size.Width-width, size.Height))
	}
	// Folded, the panel waits just past the right edge of the window.
	objects[1].Move(fyne.NewPos(size.Width-width, 0))
	objects[1].Resize(fyne.NewSize(full, size.Height))
	objects[2].Move(fyne.NewPos(size.Width-width-4, 0))
	objects[2].Resize(fyne.NewSize(8, size.Height-14))
	if l.p.target > 0 {
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

func newPanelResizeHandle(p *toolPanel) *panelResizeHandle {
	h := &panelResizeHandle{panel: p, grip: canvas.NewRectangle(colBorderSoft)}
	h.grip.CornerRadius = 1
	h.grip.SetMinSize(fyne.NewSize(2, 40))
	h.ExtendBaseWidget(h)
	return h
}

func (h *panelResizeHandle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewCenter(h.grip))
}

func (h *panelResizeHandle) Cursor() desktop.Cursor { return desktop.HResizeCursor }
func (h *panelResizeHandle) MouseIn(*desktop.MouseEvent) {
	h.grip.FillColor = colAmber
	h.grip.Refresh()
}
func (h *panelResizeHandle) MouseMoved(*desktop.MouseEvent) {}
func (h *panelResizeHandle) MouseOut() {
	h.grip.FillColor = colBorderSoft
	h.grip.Refresh()
}
func (h *panelResizeHandle) Dragged(e *fyne.DragEvent) {
	p := h.panel
	if p.anim != nil {
		p.anim.Stop()
	}
	p.shown, p.target, p.snap = 1, 1, false
	total := p.u.split.Size().Width
	p.u.panelWidth = panelWidthWithin((splitLayout{p}).width(total)-e.Dragged.DX, total)
	relayout(p.u.split)
}
func (h *panelResizeHandle) DragEnd() { h.panel.u.save() }

func (p *toolPanel) slide(to float32) {
	if p.anim != nil {
		p.anim.Stop()
	}
	from := p.shown
	p.target, p.snap = to, false
	p.anim = fyne.NewAnimation(240*time.Millisecond, func(t float32) {
		p.shown = from + (to-from)*t
		started := time.Now()
		relayout(p.u.split) // never Refresh: it would redraw the whole conversation on every frame
		if time.Since(started) > 8*time.Millisecond {
			p.snap = true
		}
		if t >= 1 {
			p.snap = false
		}
	})
	p.anim.Curve = easeOutQuint
	p.anim.Start()
	p.u.panelToggle.SetSelected(to > 0)
	p.syncRows()
}

func (p *toolPanel) fold()   { p.slide(0) }
func (p *toolPanel) unfold() { p.slide(1) }

func (p *toolPanel) find(chat *conversation, index int) int {
	for i, tab := range p.tabs {
		if tab.chat == chat && tab.index == index {
			return i
		}
	}
	return -1
}

// showing reports whether a call is the one the panel shows.
func (p *toolPanel) showing(chat *conversation, index int) bool {
	return p.shown > 0 && p.active >= 0 && p.find(chat, index) == p.active
}

// open shows a call, in its existing tab or a new one.
func (p *toolPanel) open(chat *conversation, index int) {
	i := p.find(chat, index)
	if i < 0 {
		p.tabs = append(p.tabs, panelTab{chat: chat, index: index})
		i = len(p.tabs) - 1
	}
	p.choose(i)
	if p.shown < 1 {
		p.unfold()
	}
}

func (p *toolPanel) choose(i int) {
	p.active = i
	p.render()
	p.body.ScrollToTop()
	if t := p.tabs[i].terminal; t != nil {
		p.u.window.Canvas().Focus(t.widget)
	}
}

func (p *toolPanel) close(i int) {
	p.tabs[i].dispose()
	p.tabs = append(p.tabs[:i], p.tabs[i+1:]...)
	switch {
	case len(p.tabs) == 0:
		p.active = -1
		p.render()
		p.fold()
		return
	case p.active >= len(p.tabs) || p.active > i:
		p.active--
	}
	p.render()
}

// reset forgets every tab (another chat was opened).
func (p *toolPanel) reset() {
	p.dispose()
	p.tabs, p.active = nil, -1
	p.render()
	if p.shown > 0 {
		p.fold()
	}
}

func (t panelTab) dispose() {
	if t.terminal != nil {
		t.terminal.close()
	}
	if t.cancel != nil {
		t.cancel()
	}
}

func (p *toolPanel) dispose() {
	for _, tab := range p.tabs {
		tab.dispose()
	}
}

// changed refreshes the panel when a call it shows gets its result.
func (p *toolPanel) changed(chat *conversation, index int) {
	if i := p.find(chat, index); i >= 0 {
		p.render()
	}
}

func (p *toolPanel) render() {
	p.strip.RemoveAll()
	for i, tab := range p.tabs {
		p.strip.Add(p.tabView(i, tab))
	}
	p.strip.Refresh()
	p.texts = nil
	if p.active >= 0 {
		tab := p.tabs[p.active]
		if tab.content != nil {
			p.page.Objects = []fyne.CanvasObject{tab.content}
		} else {
			item := tab.chat.Items[tab.index]
			if item.Tool == "edit_file" && item.Diff != "" {
				p.page.Objects = []fyne.CanvasObject{container.NewBorder(padded(12, 8, 12, 12, toolDetailHeading(item, runState(tab.chat, tab.index))), nil, nil, nil, newDiffView(item.Diff))}
			} else {
				p.body.Content = padded(16, 20, 18, 18, p.details(item, runState(tab.chat, tab.index)))
				p.page.Objects = []fyne.CanvasObject{p.body}
			}
		}
	} else {
		p.page.Objects = []fyne.CanvasObject{padded(28, 20, 20, 20, container.NewVBox(textLine("Panneau latéral", 18, colText), note("Ouvrez un terminal ou les modifications Git avec le bouton +, ou cliquez sur un appel d’outil dans la conversation.")))}
	}
	p.page.Refresh()
	p.body.Refresh()
	p.place()
	p.syncRows()
}

// place tells each code well which of its rows the panel shows.
func (p *toolPanel) place() {
	for _, text := range p.texts {
		if offset, ok := offsetIn(p.body.Content, text); ok {
			top := p.body.Offset.Y - offset.Y
			text.show(top, top+p.body.Size().Height)
		}
	}
}

// offsetIn is where target sits inside root, through nested containers.
func offsetIn(root, target fyne.CanvasObject) (fyne.Position, bool) {
	if root == target {
		return fyne.Position{}, true
	}
	if c, ok := root.(*fyne.Container); ok {
		for _, o := range c.Objects {
			if pos, found := offsetIn(o, target); found {
				return o.Position().Add(pos), true
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

func (l placedLayout) MinSize(objects []fyne.CanvasObject) fyne.Size { return l.inner.MinSize(objects) }

func (l placedLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.inner.Layout(objects, size)
	l.after()
}

// syncRows highlights the transcript row of the call on display.
func (p *toolPanel) syncRows() {
	for _, o := range p.u.transcript.Objects {
		if row, ok := o.(*toolRow); ok {
			if selected := p.showing(row.chat, row.index); selected != row.selected {
				row.selected = selected
				row.Refresh()
			}
		}
	}
}

func (p *toolPanel) tabView(i int, tab panelTab) fyne.CanvasObject {
	target, icon := tab.title, tab.icon
	if tab.chat != nil {
		item := tab.chat.Items[tab.index]
		_, target, _ = describeTool(item, false)
		if item.Tool != "shell" {
			target = filepath.Base(target)
		}
		icon = toolGlyph(item.Tool)
	}
	label := newRow(fitText(target, 13, fyne.TextStyle{}, 190), icon, styleChip, func() { p.choose(i) })
	label.selected = i == p.active
	label.ink = colText
	closeTab := newIconButton(iconClose, color.NRGBA{}, color.NRGBA{}, func() { p.close(i) })
	closeTab.iconPx, closeTab.padPx = 14, 6
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 10
	if i == p.active {
		bg.FillColor = colRaised
		bg.StrokeColor, bg.StrokeWidth = colBorder, 1
	}
	return container.NewStack(bg, padded(4, 4, 5, 4, container.New(layout.NewCustomPaddedHBoxLayout(2), label, container.NewCenter(closeTab))))
}

// details lays out one call: a heading with its outcome, then the call and
// what it returned.
func toolDetailHeading(item chatItem, state string) fyne.CanvasObject {
	running := state == "pending" || state == "running"
	verb, target, _ := describeTool(item, running)
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
	if item.Tool == "edit_file" && item.Diff != "" {
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

func (p *toolPanel) details(item chatItem, state string) fyne.CanvasObject {
	running := state == "pending" || state == "running"
	_, command, _ := toolArgs(item.Args)
	sections := container.New(placedLayout{layout.NewVBoxLayout(), p.place}, toolDetailHeading(item, state))
	add := func(name, text, empty string) {
		sections.Add(spacer(0, 10))
		sections.Add(p.section(name, text, empty))
	}
	switch item.Tool {
	case "shell":
		add("Command", command, "")
		add("Output", item.Text, outputPlaceholder(running, "No output"))
	case "edit_file":
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
	return sections
}

func outputPlaceholder(running bool, empty string) string {
	if running {
		return "Waiting for the result…"
	}
	return empty
}

// section is a captioned code well with its own copy button.
func (p *toolPanel) section(name, text, empty string) fyne.CanvasObject {
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
	p.texts = append(p.texts, well)
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
	v := &outputView{lines: strings.Split(text, "\n"), bottom: 2000,
		diff: strings.HasPrefix(text, "--- ") || strings.HasPrefix(text, "diff --git ")}
	v.wrap(52) // about the panel's width, until the first layout says
	v.ExtendBaseWidget(v)
	return v
}

func outputCell() fyne.Size {
	return fyne.MeasureText("M", outputTextSize, fyne.TextStyle{Monospace: true})
}

func outputRowHeight() float32 { return outputCell().Height + 3 }

// wrap cuts the lines into rows of at most cols characters.
func (v *outputView) wrap(cols int) {
	v.cols = cols
	v.rows = v.rows[:0]
	for _, line := range v.lines {
		var ink color.Color = colText
		if v.diff {
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
			v.rows = append(v.rows, outputRow{string(runes[:cols]), ink})
			runes = runes[cols:]
		}
		v.rows = append(v.rows, outputRow{string(runes), ink})
	}
}

func expandTabs(line string) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	var b strings.Builder
	column := 0
	for _, r := range line {
		if r != '\t' {
			b.WriteRune(r)
			column++
			continue
		}
		for n := 4 - column%4; n > 0; n-- {
			b.WriteByte(' ')
			column++
		}
	}
	return b.String()
}

// show sets which part of the view is on screen (in its own coordinates).
func (v *outputView) show(top, bottom float32) {
	v.top, v.bottom = top, bottom
	if v.render != nil {
		v.render.place()
	}
}

func (v *outputView) CreateRenderer() fyne.WidgetRenderer {
	v.render = &outputRenderer{view: v}
	return v.render
}

type outputRenderer struct {
	view  *outputView
	texts []*canvas.Text
	owner []int // the row each text shows, -1 when hidden
}

func (r *outputRenderer) MinSize() fyne.Size {
	return fyne.NewSize(outputCell().Width*12, float32(len(r.view.rows))*outputRowHeight())
}

func (r *outputRenderer) Layout(size fyne.Size) {
	if cols := max(12, int(size.Width/outputCell().Width)); cols != r.view.cols {
		r.view.wrap(cols)
		r.clear()
	}
	r.place()
}

// clear takes every row off its text: they are all written again.
func (r *outputRenderer) clear() {
	for slot := range r.owner {
		r.owner[slot] = -1
		r.texts[slot].Hide()
	}
}

// place gives the rows on screen (and a few around) a text each. A text
// keeps its row while that row stays in range, so scrolling only writes
// the rows that come in.
func (r *outputRenderer) place() {
	v, height := r.view, outputRowHeight()
	first := max(0, int(v.top/height)-6)
	last := min(len(v.rows)-1, int(v.bottom/height)+6)
	if need := last - first + 1; need > len(r.texts) {
		for len(r.texts) < need+12 {
			text := canvas.NewText("", colText)
			text.TextSize, text.TextStyle = outputTextSize, fyne.TextStyle{Monospace: true}
			text.Hide()
			r.texts = append(r.texts, text)
		}
		r.owner = make([]int, len(r.texts))
		r.clear()
	}
	for slot, row := range r.owner {
		if row >= 0 && (row < first || row > last) {
			r.owner[slot] = -1
			r.texts[slot].Hide()
		}
	}
	for row := first; row <= last; row++ {
		slot := row % len(r.texts)
		if r.owner[slot] == row {
			continue
		}
		r.owner[slot] = row
		text := r.texts[slot]
		text.Text, text.Color = v.rows[row].text, v.rows[row].ink
		text.Move(fyne.NewPos(0, float32(row)*height))
		text.Resize(fyne.NewSize(v.Size().Width, height))
		text.Show()
		text.Refresh()
	}
}

func (r *outputRenderer) Refresh() {
	r.clear()
	r.place()
}

func (r *outputRenderer) Objects() []fyne.CanvasObject {
	objects := make([]fyne.CanvasObject, len(r.texts))
	for i, text := range r.texts {
		objects[i] = text
	}
	return objects
}

func (r *outputRenderer) Destroy() {}
