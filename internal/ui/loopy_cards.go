// SPDX-License-Identifier: MPL-2.0

package ui

// Loopy's bubbles, reactions and cards (loopy_view.go). Bubbles are short,
// as in a text-message app: the user's on the right in teal, Loopy's on
// the left beside its face. Reactions are small drawn icons (Fyne draws no
// colour emoji). A card replaces a paragraph with something to act on:
// a mail or message draft, a choice, an approval with its warning, a
// "needs you", a morning summary, a coding task to hand over, a routine.

import (
	"fmt"
	"image/color"

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

var loopyReactionNames = []string{"thumbs_up", "heart", "done", "laugh", "wow", "eyes", "moon"}

var loopyReactionLabel = map[string]string{
	"thumbs_up": "Thumbs up", "heart": "Heart", "done": "Done", "laugh": "Haha", "wow": "Wow", "eyes": "Looking", "moon": "Good night",
}

// loopyReactionMarkup draws each reaction in colour, on a 16 unit grid.
var loopyReactionMarkup = map[string]string{
	"thumbs_up": `<path d="M5.2 7.2 7.6 2.8c.9 0 1.6.7 1.5 1.6L8.8 6.4h3.4c.9 0 1.5.8 1.3 1.6l-1 4.4c-.2.7-.8 1.1-1.5 1.1H5.2z" fill="#FFC23D"/><rect x="2.4" y="7" width="2.3" height="6.5" rx=".6" fill="#FFB000"/>`,
	"heart":     `<path d="M8 13.4 2.8 8.4A3 3 0 0 1 8 4.6a3 3 0 0 1 5.2 3.8z" fill="#E86A5F"/>`,
	"done":      `<circle cx="8" cy="8" r="6" fill="#7FC88F"/><path d="m5.2 8.2 2 2 3.8-4" fill="none" stroke="#121212" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>`,
	"laugh":     `<circle cx="8" cy="8" r="6" fill="#FFC23D"/><path d="M5 9.2a3 3 0 0 0 6 0z" fill="#121212"/><path d="M5.3 6.3 6.6 6.9M10.7 6.3 9.4 6.9" stroke="#121212" stroke-width="1.1" stroke-linecap="round"/>`,
	"wow":       `<circle cx="8" cy="8" r="6" fill="#FFC23D"/><circle cx="6" cy="6.6" r=".8" fill="#121212"/><circle cx="10" cy="6.6" r=".8" fill="#121212"/><ellipse cx="8" cy="10.3" rx="1.3" ry="1.6" fill="#121212"/>`,
	"eyes":      `<ellipse cx="5.4" cy="8" rx="2.4" ry="3.4" fill="#F4F4F4"/><ellipse cx="10.6" cy="8" rx="2.4" ry="3.4" fill="#F4F4F4"/><circle cx="6.2" cy="8.4" r="1.2" fill="#121212"/><circle cx="11.4" cy="8.4" r="1.2" fill="#121212"/>`,
	"moon":      `<path d="M10.8 11.6A5 5 0 0 1 6.4 2.6 5.6 5.6 0 1 0 13.4 10a5 5 0 0 1-2.6 1.6z" fill="#FFC23D"/>`,
}

func loopyReactionIcon(name string) fyne.Resource {
	markup, ok := loopyReactionMarkup[name]
	if !ok {
		markup = loopyReactionMarkup["thumbs_up"]
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">` + markup + `</svg>`
	return fyne.NewStaticResource("loopy-reaction-"+name+".svg", []byte(svg))
}

// reactionBadge is a reaction on a bubble's edge.
func reactionBadge(name string) fyne.CanvasObject {
	icon := canvas.NewImageFromResource(loopyReactionIcon(name))
	icon.SetMinSize(fyne.NewSquareSize(14))
	background := canvas.NewRectangle(colRaised)
	background.CornerRadius, background.StrokeColor, background.StrokeWidth = 11, colBackground, 2
	return container.NewStack(background, padded(3, 3, 6, 6, container.NewCenter(icon)))
}

// ---------------------------------------------------------------------------
// Bubbles.

// loopyBubble is one message in a bubble.
type loopyBubble struct {
	id     string
	row    *fyne.Container
	box    *fyne.Container
	text   *flowText
	fill   *canvas.Rectangle
	mine   bool
	onMenu func()
}

const loopyBubbleMax = 460

func newLoopyBubble(text string, mine bool, reaction string) *loopyBubble {
	bubble := &loopyBubble{mine: mine}
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

// dim greys a message that waits for Loopy to be free.
func (bubble *loopyBubble) dim() {
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

// cardView draws a card and wires its buttons.
func (mode *loopyMode) cardView(message loopy.Message) fyne.CanvasObject {
	c := message.Card
	icon := canvas.NewImageFromResource(tinted(loopyCardIcon(c.Kind), colMuted))
	icon.SetMinSize(fyne.NewSquareSize(15))
	title := textLine(c.Title, 13, colText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	headParts := []fyne.CanvasObject{container.NewCenter(icon), container.NewCenter(title)}
	if c.Subtitle != "" {
		headParts = append(headParts, container.NewCenter(textLine(c.Subtitle, 12.5, colMuted)))
	}
	var status fyne.CanvasObject = layout.NewSpacer()
	task, isTask := mode.task(c.Task)
	switch {
	case isTask && task.Status != "proposed":
		ink := map[string]color.Color{"running": colAmber, "done": colAdded}[task.Status]
		if ink == nil {
			ink = colMuted
		}
		status = container.NewCenter(loopyPill(map[string]string{"running": "Working", "done": "Done", "failed": "Failed", "cancelled": "Cancelled"}[task.Status], ink))
	case c.Answer != "":
		status = container.NewCenter(loopyPill(c.Answer, colAdded))
	case c.Waiting():
		status = container.NewCenter(loopyPill("Waiting for you", colAmber))
	}
	head := padded(9, 9, 13, 13, container.NewBorder(nil, nil, container.New(layout.NewCustomPaddedHBoxLayout(8), headParts...), status))
	rows := []fyne.CanvasObject{head}
	if c.Warning != "" {
		rows = append(rows, padded(8, 8, 13, 13, plainFlow("⚠ "+c.Warning, 12.5, colDanger)))
	}
	if len(c.Fields) > 0 {
		grid := container.New(layout.NewCustomPaddedVBoxLayout(6))
		for _, field := range c.Fields {
			label := textLine(field.Label, 13, colMuted)
			grid.Add(container.NewBorder(nil, nil, sized(64, label), nil, plainFlow(field.Value, 13, colText)))
		}
		rows = append(rows, padded(10, 10, 13, 13, grid))
	}
	if c.Body != "" {
		rows = append(rows, padded(10, 10, 13, 13, plainFlow(c.Body, 13.5, colText)))
	}
	if len(c.Options) > 0 {
		options := container.NewVBox()
		for i, option := range c.Options {
			chosen := c.Answer == option.Label
			number := textLine(fmt.Sprint(i+1), 12, colText)
			if chosen {
				number.Color = colInk
			}
			dotFill := colRaised
			if chosen {
				dotFill = colAmber
			}
			dot := canvas.NewRectangle(dotFill)
			dot.CornerRadius = 6
			dot.SetMinSize(fyne.NewSquareSize(20))
			badge := container.NewStack(dot, container.NewCenter(number))
			text := container.New(layout.NewCustomPaddedVBoxLayout(1), textLine(option.Label, 13.5, colText))
			if option.Detail != "" {
				text.Add(textLine(option.Detail, 12.5, colMuted))
			}
			var aside fyne.CanvasObject = layout.NewSpacer()
			if option.Aside != "" {
				aside = container.NewCenter(textLine(option.Aside, 13, colText))
			}
			line := container.NewBorder(nil, nil, sized(22, container.NewCenter(badge)), aside, padded(0, 0, 10, 0, text))
			var back fyne.CanvasObject = canvas.NewRectangle(color.Transparent)
			if chosen {
				back = canvas.NewRectangle(color.NRGBA{R: 0x26, G: 0x20, B: 0x12, A: 0xff})
			}
			entry := container.NewStack(back, padded(9, 9, 13, 13, line))
			if c.Answer == "" {
				entry.Add(newTapArea(func() { mode.answerCard(message, option.Label) }))
			}
			options.Add(entry)
		}
		rows = append(rows, options)
	}
	if c.Answer == "" && len(c.Actions) > 0 {
		buttons := container.New(layout.NewCustomPaddedHBoxLayout(8))
		for i, action := range c.Actions {
			style := styleOutline
			if i == 0 {
				style = stylePrimary
			}
			buttons.Add(newRow(action, nil, style, func() { mode.answerCard(message, action) }))
		}
		rows = append(rows, padded(10, 10, 13, 13, buttons))
	}
	if c.Kind == "delegate" && c.Task != "" {
		if task, ok := mode.task(c.Task); ok && task.Status != "proposed" {
			rows = append(rows, padded(8, 10, 13, 13, mode.taskProgress(task)))
		}
	}
	box := card(rows...)
	return container.New(&maxWidthLayout{width: 540}, box)
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
	for i, step := range task.Steps {
		ink := colAdded
		if i == len(task.Steps)-1 && task.Status == "running" {
			ink = colAmber
		}
		dot := canvas.NewCircle(ink)
		mark := padded(3, 0, 0, 0, container.NewGridWrap(fyne.NewSquareSize(8), dot)) // level with the text's middle
		steps.Add(container.NewHBox(container.NewCenter(mark), container.NewCenter(textLine(step, 13, colTextSoft))))
	}
	actions := container.NewHBox(newRow("Open the chat", nil, styleOutline, func() { mode.openTask(task) }))
	if task.Status == "running" {
		actions.Add(newRow("Stop", nil, styleOutline, func() { mode.stopTask(task) }))
	}
	return container.NewVBox(steps, spacer(0, 6), actions)
}
