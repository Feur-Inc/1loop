// SPDX-License-Identifier: MPL-2.0

package ui

// Loopy's bubbles, reactions and cards (loopy_view.go). Bubbles are short,
// as in a text-message app: the user's on the right in teal, Loopy's on the
// left beside its face. A card replaces a paragraph with something to act
// on: a mail draft the user edits, a choice, an approval with its warning, a
// sign-in, a "needs you", a summary, a coding task, a routine.

import (
	"context"
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/loopy"
)

var (
	iconMemory = lineIcon("memory", `<path d="M8 2.8a4.2 4.2 0 0 0-4.2 4.2c0 1.5.7 2.5 1.6 3.3.5.4.7.9.7 1.4v.8h3.8v-.8c0-.5.2-1 .7-1.4.9-.8 1.6-1.8 1.6-3.3A4.2 4.2 0 0 0 8 2.8z"/><path d="M6.4 14h3.2"/>`)
	iconPin    = lineIcon("pin", `<path d="M9.8 2.5 13.5 6.2 11 7.4 8.6 9.8 9 12.5 3.5 7 6.2 7.4 8.6 5z"/><path d="M5.6 10.4 2.5 13.5"/>`)
	iconCode   = lineIcon("code", `<path d="M5.5 5 2.5 8l3 3M10.5 5l3 3-3 3M9 3.5l-2 9"/>`)
	iconMail   = lineIcon("mail", `<rect x="2.2" y="3.5" width="11.6" height="9" rx="1.6"/><path d="m2.7 4.6 5.3 4 5.3-4"/>`)
	iconChat   = lineIcon("chat", `<path d="M3 3.5h10a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H7l-3 2.5v-2.5H3a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1z"/>`)
	iconHelp   = lineIcon("help", `<circle cx="8" cy="8" r="5.8"/><path d="M6.4 6.3a1.7 1.7 0 1 1 2.4 1.6c-.5.2-.8.6-.8 1.2M8 11.2v.1"/>`)
	iconList   = lineIcon("list", `<path d="M5.5 4.5h8M5.5 8h8M5.5 11.5h8"/><path d="M2.6 4.5h.1M2.6 8h.1M2.6 11.5h.1"/>`)
	iconSun    = lineIcon("sun", `<circle cx="8" cy="8" r="2.6"/><path d="M8 1.8v1.4M8 12.8v1.4M1.8 8h1.4M12.8 8h1.4M3.6 3.6l1 1M11.4 11.4l1 1M3.6 12.4l1-1M11.4 4.6l1-1"/>`)
)

// loopyCardIcon is a card kind's icon, in the card and the sidebar.
func loopyCardIcon(kind string) fyne.Resource {
	switch kind {
	case "mail":
		return iconMail
	case "event":
		return iconClock
	case "signin", "secret":
		return iconShield
	case "message":
		return iconChat
	case "approval":
		return iconShield
	case "needs_you":
		return iconHelp
	case "summary":
		return iconSun
	case "delegate":
		return iconCode
	case "routine":
		return iconClock
	}
	return iconList
}

// ---------------------------------------------------------------------------
// Reactions.

// reactionBadge is a reaction on a bubble's edge, as the emoji it is.
func reactionBadge(name string) fyne.CanvasObject {
	label := canvas.NewText(loopy.Emoji(name), colText)
	label.TextSize = 13
	background := canvas.NewRectangle(colRaised)
	background.CornerRadius, background.StrokeColor, background.StrokeWidth = 11, colBackground, 2
	return container.NewStack(background, padded(2, 2, 7, 7, container.NewCenter(label)))
}

// ---------------------------------------------------------------------------
// Bubbles.

// loopyBubble is one message in a bubble.
type loopyBubble struct {
	id       string
	row      *fyne.Container
	box      *fyne.Container
	text     *flowText
	fill     *canvas.Rectangle
	mine     bool
	dimmed   bool
	reaction string
	onMenu   func()
}

const loopyBubbleMax = 460

func newLoopyBubble(text string, mine bool, reaction string) *loopyBubble {
	bubble := &loopyBubble{mine: mine, reaction: reaction}
	ink := colText
	fill := colRaised
	if mine {
		fill, ink = colLoopyMe, color.NRGBA{R: 0xf4, G: 0xf4, B: 0xf4, A: 0xff}
	}
	bubble.text = plainFlow(text, 14.5, ink)
	bubble.fill = canvas.NewRectangle(fill)
	bubble.fill.CornerRadius = 18
	tap := &bubbleTap{bubble: bubble}
	tap.ExtendBaseWidget(tap)
	bubble.box = container.NewStack(bubble.fill, padded(8, 8, 13, 13, bubble.text), tap)
	var avatar fyne.CanvasObject = spacer(0, 0)
	if !mine {
		avatar = newLoopyFace(26, "idle", true, true).image
	}
	var badge fyne.CanvasObject = spacer(0, 0)
	if reaction != "" {
		badge = reactionBadge(reaction)
	}
	bubble.row = container.New(&loopyBubbleLayout{bubble: bubble}, avatar, bubble.box, badge)
	return bubble
}

// setText changes what a streaming bubble says.
func (bubble *loopyBubble) setText(text string) {
	bubble.text.spans = []span{{text: text}}
	bubble.text.width = 0
	bubble.text.Refresh()
	bubble.row.Refresh()
}

// plain says whether the bubble can show a message by its text alone.
func (bubble *loopyBubble) plain(message loopy.Message) bool {
	return message.Card == nil && message.Memory == "" && message.Note == "" && message.Reaction == bubble.reaction && len(message.Files) == 0 && !bubble.dimmed && (message.From == "me" || !loopyMarkup(message.Text))
}

// dim greys a message that waits for Loopy to be free.
func (bubble *loopyBubble) dim() {
	bubble.dimmed = true
	bubble.fill.FillColor = color.NRGBA{R: 0x0f, G: 0x7a, B: 0x6e, A: 0x80}
}

// bubbleTap opens a bubble's reactions on a right click.
type bubbleTap struct {
	widget.BaseWidget
	bubble *loopyBubble
}

func (tap *bubbleTap) CreateRenderer() fyne.WidgetRenderer { return emptyRenderer{} }
func (tap *bubbleTap) Tapped(*fyne.PointEvent)             {}
func (tap *bubbleTap) TappedSecondary(*fyne.PointEvent) {
	if tap.bubble.onMenu != nil {
		tap.bubble.onMenu()
	}
}

// loopyBubbleLayout sets the face, the bubble as wide as its text up to a
// limit, and the reaction on the bubble's bottom edge.
type loopyBubbleLayout struct {
	bubble *loopyBubble
	width  float32 // the row's width at the last layout
}

const (
	loopyAvatar = 26
	loopyGap    = 8
)

// measure is the bubble's size in a row width wide.
func (l *loopyBubbleLayout) measure(width float32) fyne.Size {
	room := width
	if !l.bubble.mine {
		room -= loopyAvatar + loopyGap
	}
	limit := fyne.Min(loopyBubbleMax, room*.82)
	natural, _ := l.bubble.text.extent()
	inner := fyne.Min(natural+1, limit-26)
	l.bubble.text.flow(inner)
	return fyne.NewSize(inner+26, l.bubble.text.height+16)
}

func (l *loopyBubbleLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := l.width
	if width <= 0 {
		width = 672
	}
	size := l.measure(width)
	if _, badge := objects[2].(*fyne.Container); badge {
		size.Height += objects[2].MinSize().Height - 8 // it hangs under the bubble's edge
	}
	return fyne.NewSize(size.Width, size.Height)
}

func (l *loopyBubbleLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	avatar, box, badge := objects[0], objects[1], objects[2]
	if size.Width != l.width {
		l.width = size.Width
		defer l.bubble.row.Refresh() // its height may change with the width
	}
	bubble := l.measure(size.Width)
	x := float32(0)
	if l.bubble.mine {
		x = size.Width - bubble.Width
	} else {
		avatar.Resize(fyne.NewSquareSize(loopyAvatar))
		avatar.Move(fyne.NewPos(0, bubble.Height-loopyAvatar))
		x = loopyAvatar + loopyGap
	}
	box.Resize(bubble)
	box.Move(fyne.NewPos(x, 0))
	if b := badge.MinSize(); b.Width > 0 {
		badge.Resize(b)
		bx := x + 8
		if l.bubble.mine {
			bx = x + bubble.Width - b.Width - 8
		}
		badge.Move(fyne.NewPos(bx, bubble.Height-8))
	}
}

// ---------------------------------------------------------------------------
// Cards.

// loopyDraft is what is typed in a card, kept while the card is drawn again.
type loopyDraft struct {
	fields []*widget.Entry
	body   *widget.Entry
	ask    *widget.Entry
	values map[string]*widget.Entry
	keep   *widget.Check
	hint   *widget.Entry
}

// answer sends a card's answer, with what was typed in it.
func (mode *loopyMode) answer(message loopy.Message, action string) {
	card := message.Card
	answer := loopy.Answer{Message: message.ID, Answer: action}
	if draft := mode.drafts[message.ID]; draft != nil {
		if card.Editable && len(draft.fields) == len(card.Fields) && card.Kind != "delegate" {
			for i, entry := range draft.fields {
				answer.Fields = append(answer.Fields, loopy.Field{Label: card.Fields[i].Label, Value: entry.Text})
			}
		}
		if draft.body != nil {
			answer.Body = draft.body.Text
		}
		if draft.ask != nil {
			answer.Text = draft.ask.Text
		}
		if draft.values != nil {
			answer.Values = map[string]string{}
			for name, entry := range draft.values {
				answer.Values[name] = entry.Text
			}
		}
		if draft.keep != nil {
			answer.Keep, answer.Hint = draft.keep.Checked, draft.hint.Text
		}
	}
	mode.say("")
	mode.do(func(ctx context.Context, client loopy.Client) error { return client.Answer(ctx, answer) }, func(err error) {
		if err == nil {
			delete(mode.drafts, message.ID) // a password is not kept
		}
		mode.reload()
	})
}

// draft is the fields of a card that can be typed in, made once.
func (mode *loopyMode) draft(message loopy.Message) *loopyDraft {
	if draft := mode.drafts[message.ID]; draft != nil {
		return draft
	}
	card := message.Card
	draft := &loopyDraft{}
	if card.Editable {
		if card.Kind != "delegate" {
			for _, field := range card.Fields {
				entry := widget.NewEntry()
				entry.SetText(field.Value)
				draft.fields = append(draft.fields, entry)
			}
		}
		draft.body = widget.NewMultiLineEntry()
		draft.body.Wrapping = fyne.TextWrapWord
		draft.body.SetMinRowsVisible(min(12, max(3, strings.Count(card.Body, "\n")+len([]rune(card.Body))/70+1)))
		draft.body.SetText(card.Body)
	}
	if card.Ask != "" {
		draft.ask = widget.NewEntry()
		if card.Secret {
			draft.ask = widget.NewPasswordEntry()
		}
		draft.ask.SetPlaceHolder(card.Ask)
		draft.ask.OnSubmitted = func(string) { mode.answer(message, card.Actions[0]) }
	}
	if card.Kind == "signin" {
		draft.values = map[string]*widget.Entry{}
		for _, field := range card.Fields {
			entry := widget.NewEntry()
			entry.SetPlaceHolder("User name or address")
			if field.Label == "password" {
				entry = widget.NewPasswordEntry()
				entry.SetPlaceHolder("Password")
			}
			entry.OnSubmitted = func(string) { mode.answer(message, card.Actions[0]) }
			draft.values[field.Label] = entry
		}
		if card.Keep {
			draft.keep = widget.NewCheck("Keep in Loopy's vault", nil)
			draft.hint = widget.NewEntry()
			draft.hint.SetPlaceHolder("When to use it (work account…), optional")
		}
	}
	mode.drafts[message.ID] = draft
	return draft
}

// cardView draws a card and wires its buttons.
func (mode *loopyMode) cardView(message loopy.Message) fyne.CanvasObject {
	c := message.Card
	waiting := c.Waiting()
	icon := canvas.NewImageFromResource(tinted(loopyCardIcon(c.Kind), colMuted))
	icon.SetMinSize(fyne.NewSquareSize(15))
	heading := container.New(layout.NewCustomPaddedVBoxLayout(1), newFlowText([]span{{text: c.Title, bold: true}}, 13, colText))
	if c.Subtitle != "" {
		heading.Add(plainFlow(c.Subtitle, 12.5, colMuted))
	}
	var status fyne.CanvasObject = layout.NewSpacer()
	task, isTask := mode.task(c.Task)
	isTask = isTask && c.Kind == "delegate"
	switch {
	case isTask && task.Status != "proposed":
		ink := map[string]color.Color{"running": colAmber, "waiting": colDanger, "done": colAdded}[task.Status]
		if ink == nil {
			ink = colMuted
		}
		status = container.NewCenter(loopyPill(map[string]string{"running": "Working", "waiting": "Needs you", "done": "Done", "failed": "Failed", "cancelled": "Cancelled"}[task.Status], ink))
	case c.Answer != "":
		ink := color.Color(colAdded)
		if c.Answer == "Deny" || c.Answer == "Stopped" || c.Answer == "Expired" || c.Answer == "Discarded" || c.Answer == "Cancelled" || c.Answer == "Not now" || c.Answer == "Drop it" {
			ink = colMuted
		}
		status = container.NewCenter(loopyPill(c.Answer, ink))
	case waiting && c.Kind == "needs_you":
		status = container.NewCenter(loopyPill("Needs you", colDanger))
	case waiting:
		status = container.NewCenter(loopyPill("Waiting for you", colAmber))
	}
	head := padded(9, 9, 13, 13, container.NewBorder(nil, nil, padded(0, 0, 0, 8, container.NewCenter(icon)), padded(0, 0, 8, 0, status), heading))
	rows := []fyne.CanvasObject{head}
	if c.Warning != "" {
		rows = append(rows, padded(8, 8, 13, 13, plainFlow("⚠ "+c.Warning, 12.5, colDanger)))
	}
	var draft *loopyDraft
	if waiting && (c.Editable || c.Ask != "" || c.Kind == "signin") {
		draft = mode.draft(message)
	}
	editing := draft != nil && draft.fields != nil
	if len(c.Fields) > 0 && c.Kind != "signin" {
		grid := container.New(layout.NewCustomPaddedVBoxLayout(6))
		for i, field := range c.Fields {
			label := textLine(field.Label, 13, colMuted)
			var value fyne.CanvasObject = plainFlow(field.Value, 13, colText)
			if editing {
				value = draft.fields[i]
			}
			grid.Add(container.NewBorder(nil, nil, sized(72, container.NewVBox(layout.NewSpacer(), label, layout.NewSpacer())), nil, value))
		}
		rows = append(rows, padded(10, 10, 13, 13, grid))
	}
	switch {
	case draft != nil && draft.body != nil:
		rows = append(rows, padded(8, 8, 13, 13, draft.body))
	case c.Body != "":
		rows = append(rows, padded(10, 10, 13, 13, plainFlow(c.Body, 13.5, colText)))
	}
	if draft != nil && draft.ask != nil {
		rows = append(rows, padded(8, 8, 13, 13, draft.ask))
	}
	if draft != nil && draft.values != nil {
		form := container.New(layout.NewCustomPaddedVBoxLayout(8))
		for _, field := range c.Fields {
			form.Add(draft.values[field.Label])
		}
		if draft.keep != nil {
			form.Add(draft.keep)
			form.Add(draft.hint)
		}
		form.Add(plainFlow("It goes straight into the page: Loopy never sees it.", 12, colMuted))
		rows = append(rows, padded(10, 10, 13, 13, form))
	}
	if len(c.Options) > 0 {
		rows = append(rows, mode.cardOptions(message))
	}
	if c.Result != "" {
		rows = append(rows, padded(8, 8, 13, 13, plainFlow(c.Result, 12.5, colMuted)))
	}
	if waiting && len(c.Actions) > 0 {
		buttons := container.New(&wrapLayout{gap: 8})
		if c.Kind == "needs_you" && c.Ask == "" && mode.view.Browser && !mode.view.Taken {
			buttons.Add(newRow("Take over the browser", iconBrowser, styleOutline, mode.takeBrowser))
		}
		for i, action := range c.Actions {
			style := styleOutline
			if i == 0 {
				style = stylePrimary
			}
			buttons.Add(newRow(action, nil, style, func() { mode.answer(message, action) }))
		}
		rows = append(rows, padded(10, 10, 13, 13, buttons))
	}
	if isTask && task.Status != "proposed" {
		rows = append(rows, padded(8, 10, 13, 13, mode.taskProgress(task)))
	}
	return container.New(&maxWidthLayout{width: 560}, card(rows...))
}

// cardOptions are a choice's options, or a summary's lines with their
// button.
func (mode *loopyMode) cardOptions(message loopy.Message) fyne.CanvasObject {
	c := message.Card
	options := container.NewVBox()
	for i, option := range c.Options {
		chosen := c.Answer == option.Label
		text := container.New(layout.NewCustomPaddedVBoxLayout(1), plainFlow(option.Label, 13.5, colText))
		if option.Detail != "" {
			text.Add(plainFlow(option.Detail, 12.5, colMuted))
		}
		if c.Kind == "summary" {
			var right fyne.CanvasObject
			if option.Action != "" {
				right = container.NewCenter(newRow(option.Action, nil, styleOutline, func() { mode.answer(message, option.Label) }))
			}
			options.Add(padded(8, 8, 13, 13, container.NewBorder(nil, nil, nil, right, padded(0, 0, 0, 10, text))))
			continue
		}
		number := textLine(fmt.Sprint(i+1), 12, colText)
		dotFill := colRaised
		if chosen {
			number.Color, dotFill = colInk, colAmber
		}
		dot := canvas.NewRectangle(dotFill)
		dot.CornerRadius = 6
		dot.SetMinSize(fyne.NewSquareSize(20))
		badge := container.NewStack(dot, container.NewCenter(number))
		var aside fyne.CanvasObject = layout.NewSpacer()
		if option.Aside != "" {
			aside = container.NewCenter(textLine(option.Aside, 13, colText))
		}
		line := container.NewBorder(nil, nil, sized(22, container.NewCenter(badge)), aside, padded(0, 0, 10, 0, text))
		var back fyne.CanvasObject = canvas.NewRectangle(color.Transparent)
		if chosen {
			back = canvas.NewRectangle(overlay(0x12))
		}
		entry := container.NewStack(back, padded(9, 9, 13, 13, line))
		if c.Answer == "" {
			entry.Add(newTapArea(func() { mode.answer(message, option.Label) }))
		}
		options.Add(entry)
	}
	return options
}

// wrapLayout sets its objects side by side, on as many lines as it takes.
type wrapLayout struct {
	gap   float32
	width float32
}

func (l *wrapLayout) place(objects []fyne.CanvasObject, width float32, move bool) float32 {
	x, y, line := float32(0), float32(0), float32(0)
	for _, object := range objects {
		if !object.Visible() {
			continue
		}
		size := object.MinSize()
		if x > 0 && x+size.Width > width {
			x, y, line = 0, y+line+l.gap, 0
		}
		if move {
			object.Resize(size)
			object.Move(fyne.NewPos(x, y))
		}
		x += size.Width + l.gap
		line = fyne.Max(line, size.Height)
	}
	return y + line
}

func (l *wrapLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := l.width
	if width <= 0 {
		width = 500
	}
	return fyne.NewSize(60, l.place(objects, width, false))
}

func (l *wrapLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	l.width = size.Width
	l.place(objects, size.Width, true)
}

// loopyPill is a small status label in its colour.
func loopyPill(text string, ink color.Color) fyne.CanvasObject {
	label := textLine(text, 11, ink)
	label.TextStyle = fyne.TextStyle{Bold: true}
	r, g, b, _ := ink.RGBA()
	fill := canvas.NewRectangle(color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 40})
	fill.CornerRadius = 6
	return container.NewStack(fill, padded(2, 2, 7, 7, label))
}

// maxWidthLayout keeps its content at most width wide, on the left.
type maxWidthLayout struct{ width float32 }

func (l *maxWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := objects[0].MinSize()
	return fyne.NewSize(fyne.Min(size.Width, l.width), size.Height)
}

func (l *maxWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := fyne.Min(size.Width, l.width)
	objects[0].Resize(fyne.NewSize(width, objects[0].MinSize().Height))
	objects[0].Move(fyne.NewPos(0, 0))
}

// taskProgress is a delegated task's steps, as its chat goes.
func (mode *loopyMode) taskProgress(task loopy.Task) fyne.CanvasObject {
	steps := container.New(layout.NewCustomPaddedVBoxLayout(6))
	shown := task.Steps
	if len(shown) > 7 { // the start, then the latest
		steps.Add(textLine(fmt.Sprintf("%d earlier steps", len(shown)-6), 12, colFaint))
		shown = shown[len(shown)-6:]
	}
	for i, step := range shown {
		ink := color.Color(colAdded)
		if i == len(shown)-1 && task.Status == "running" {
			ink = colAmber
		}
		if i == len(shown)-1 && (task.Status == "waiting" || task.Status == "failed") {
			ink = colDanger
		}
		dot := canvas.NewCircle(ink)
		mark := padded(3, 0, 0, 0, container.NewGridWrap(fyne.NewSquareSize(8), dot)) // level with the text's middle
		steps.Add(container.NewBorder(nil, nil, container.NewCenter(mark), textLine(step.Time.Local().Format("15:04"), 12, colFaint), plainFlow(step.Text, 13, colTextSoft)))
	}
	actions := container.New(&wrapLayout{gap: 8}, newRow("Open the chat", nil, styleOutline, func() { mode.openTask(task) }))
	if task.Status == "running" || task.Status == "waiting" {
		actions.Add(newRow("Stop", nil, styleOutline, func() { mode.action(loopy.Action{Do: "task.stop", ID: task.ID}, nil) }))
	}
	for _, pr := range task.PRs {
		actions.Add(newRow(pr, iconCode, styleOutline, func() { mode.openPR(pr) }))
	}
	return container.NewVBox(steps, spacer(0, 6), actions)
}
