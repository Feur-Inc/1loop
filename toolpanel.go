package main

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
	"fyne.io/fyne/v2/widget"
)

// toolPanel is the foldable panel on the right of the conversation. Each
// tool call opened from the transcript gets a tab showing the call itself
// (command, path, diff), its full output and how long it ran.
type toolPanel struct {
	u      *desktopUI
	view   fyne.CanvasObject
	strip  *fyne.Container
	body   *container.Scroll
	tabs   []panelTab
	active int
	shown  float32 // 0 folded .. 1 open, eased by slide
	anim   *fyne.Animation
}

type panelTab struct {
	chat  *conversation
	index int
}

const panelWidth = 460

func newToolPanel(u *desktopUI) *toolPanel {
	p := &toolPanel{u: u, active: -1}
	p.strip = container.New(layout.NewCustomPaddedHBoxLayout(4))
	fold := newRow("", iconPanel, styleWindow, p.fold)
	header := container.NewBorder(nil, nil, nil, container.NewCenter(fold), container.NewHScroll(p.strip))
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	p.body = container.NewVScroll(spacer(0, 0))
	card := canvas.NewRectangle(colSurface)
	card.CornerRadius = 12
	card.StrokeColor = colBorderSoft
	card.StrokeWidth = 1
	content := container.NewBorder(container.NewVBox(padded(6, 6, 8, 6, header), rule), nil, nil, nil, p.body)
	p.view = padded(0, 14, 0, 14, container.NewStack(card, content))
	return p
}

// splitLayout gives the panel its slice of the width as it slides in.
type splitLayout struct{ p *toolPanel }

func (l splitLayout) width(total float32) float32 {
	return fyne.Min(panelWidth, total*0.5) * l.p.shown
}

func (l splitLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (l splitLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := l.width(size.Width)
	objects[0].Resize(fyne.NewSize(size.Width-width, size.Height))
	full := fyne.Min(panelWidth, size.Width*0.5)
	// Folded, the panel waits just past the right edge of the window.
	objects[1].Move(fyne.NewPos(size.Width-width, 0))
	objects[1].Resize(fyne.NewSize(full, size.Height))
}

func (p *toolPanel) slide(to float32) {
	if p.anim != nil {
		p.anim.Stop()
	}
	from := p.shown
	p.anim = fyne.NewAnimation(240*time.Millisecond, func(t float32) {
		p.shown = from + (to-from)*t
		p.u.split.Refresh()
	})
	p.anim.Curve = easeOutQuint
	p.anim.Start()
	if to == 0 && len(p.tabs) > 0 {
		p.u.panelToggle.Show()
	} else {
		p.u.panelToggle.Hide()
	}
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
		if len(p.tabs) >= 8 { // keep the strip short: the oldest tab goes
			p.tabs = p.tabs[1:]
		}
		p.tabs = append(p.tabs, panelTab{chat, index})
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
}

func (p *toolPanel) close(i int) {
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
	p.tabs, p.active = nil, -1
	p.render()
	if p.shown > 0 {
		p.fold()
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
	if p.active >= 0 {
		tab := p.tabs[p.active]
		p.body.Content = padded(16, 20, 18, 18, p.details(tab.chat.Items[tab.index], runState(tab.chat, tab.index)))
	} else {
		p.body.Content = spacer(0, 0)
	}
	p.body.Refresh()
	p.syncRows()
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
	item := tab.chat.Items[tab.index]
	_, target, _ := describeTool(item, false)
	if item.Tool != "shell" {
		target = filepath.Base(target)
	}
	label := newRow(fitText(target, 13, fyne.TextStyle{}, 150), toolGlyph(item.Tool), styleChip, func() { p.choose(i) })
	label.selected = i == p.active
	closeTab := newIconButton(iconClose, color.NRGBA{}, color.NRGBA{}, func() { p.close(i) })
	closeTab.iconPx, closeTab.padPx = 13, 4
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 8
	if i == p.active {
		bg.FillColor = colRaised
	}
	return container.NewStack(bg, container.New(layout.NewCustomPaddedHBoxLayout(0), label, container.NewCenter(closeTab), spacer(2, 0)))
}

// details lays out one call: a heading with its outcome, then the call and
// what it returned.
func (p *toolPanel) details(item chatItem, state string) fyne.CanvasObject {
	running := state == "pending" || state == "running"
	verb, target, _ := describeTool(item, running)
	_, command, _ := toolArgs(item.Args)
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

	sections := container.NewVBox(heading)
	add := func(name, text, empty string) {
		sections.Add(spacer(0, 10))
		sections.Add(panelSection(name, text, empty))
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

// panelSection is a captioned code well with its own copy button.
func panelSection(name, text, empty string) fyne.CanvasObject {
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
	return container.NewVBox(padded(0, 2, 2, 0, header), surface(codeText(strings.TrimRight(text, "\n")), colCode, colBorderSoft, 10, 6))
}
