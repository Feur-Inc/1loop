// SPDX-License-Identifier: MPL-2.0

package ui

// The model's questions (request_user_input): one at a time, each with its
// options as choice cards and an "Other" field to answer in one's own words.
// The run waits for the answers; the phone can answer them too (see
// phone_server.go). Closing the sheet only sets the questions aside: a tap
// on the activity line brings them back.

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// questionForm is what the user has picked or written so far, by question.
type questionForm struct {
	questions []agent.Question
	picked    []map[string]bool // option labels
	other     []string          // the "Other" fields
}

func newQuestionForm(questions []agent.Question) *questionForm {
	form := &questionForm{questions: questions, picked: make([]map[string]bool, len(questions)), other: make([]string, len(questions))}
	for i := range form.picked {
		form.picked[i] = map[string]bool{}
	}
	return form
}

// pick chooses an option: the answer on a single choice, one more (or one
// less) on a multi-select.
func (form *questionForm) pick(index int, label string) {
	if form.questions[index].MultiSelect {
		if form.picked[index][label] {
			delete(form.picked[index], label)
		} else {
			form.picked[index][label] = true
		}
		return
	}
	form.picked[index] = map[string]bool{label: true}
	form.other[index] = ""
}

// write sets the question's own answer. On a single choice it takes the
// place of the option picked.
func (form *questionForm) write(index int, text string) {
	form.other[index] = text
	if strings.TrimSpace(text) != "" && !form.questions[index].MultiSelect {
		form.picked[index] = map[string]bool{}
	}
}

// answer is what is sent for a question: the option's label, or the user's
// words after agent.OtherAnswer. A multi-select lists everything chosen.
func (form *questionForm) answer(index int) string {
	var parts []string
	for _, option := range form.questions[index].Options {
		if form.picked[index][option.Label] {
			parts = append(parts, option.Label)
		}
	}
	own := strings.TrimSpace(form.other[index])
	switch {
	case own == "":
		return strings.Join(parts, ", ")
	case len(parts) == 0:
		return agent.OtherAnswer + own
	}
	return strings.Join(append(parts, own), ", ")
}

func (form *questionForm) answered(index int) bool { return form.answer(index) != "" }

// answers are the answers by question id, as Answer takes them.
func (form *questionForm) answers() map[string]string {
	answers := map[string]string{}
	for i, question := range form.questions {
		if answer := form.answer(i); answer != "" {
			answers[question.ID] = answer
		}
	}
	return answers
}

// firstMissing is the first question without an answer, -1 when none.
func (form *questionForm) firstMissing() int {
	for i := range form.questions {
		if !form.answered(i) {
			return i
		}
	}
	return -1
}

func (form *questionForm) missing() int {
	count := 0
	for i := range form.questions {
		if !form.answered(i) {
			count++
		}
	}
	return count
}

// sendLabel names the send button: what is still missing, until nothing is.
func (form *questionForm) sendLabel() string {
	missing := form.missing()
	switch {
	case missing == 0 && len(form.questions) == 1:
		return "Send answer"
	case missing == 0:
		return "Send answers"
	case len(form.questions) == 1:
		return "Choose an answer"
	case missing == 1:
		return "Answer 1 more question"
	}
	return fmt.Sprintf("Answer %d more questions", missing)
}

// recommendedLabel drops the "(Recommended)" the model puts after the option
// it recommends: the card shows a badge instead. The answer keeps the label
// as the model wrote it.
func recommendedLabel(label string) (string, bool) {
	const mark = "(recommended)"
	trimmed := strings.TrimSpace(label)
	if len(trimmed) > len(mark) && strings.EqualFold(trimmed[len(trimmed)-len(mark):], mark) {
		return strings.TrimSpace(trimmed[:len(trimmed)-len(mark)]), true
	}
	return label, false
}

func (ui *desktopUI) showQuestions(chat *conversation, request agent.QuestionRequest) *sheet {
	ui = ui.viewOf(chat).sheetView(chat) // the window showing it, else the one in use (windows.go)
	form := newQuestionForm(request.Questions)
	resolved := false
	current := 0
	var dialog *sheet
	forget := func() {
		if chat.run != nil && chat.run.questions == dialog {
			chat.run.questions, chat.run.questionID, chat.run.answerQuestions = nil, "", nil
			ui.remote.metaChanged(chat)
			ui.syncTray()
		}
	}
	finish := func(given map[string]string) {
		if resolved {
			return
		}
		resolved = true
		if chat.server != nil {
			chat.server.action("/api/answer", map[string]any{"id": request.ID, "answers": given})
		} else {
			ui.backend.Answer(request.ID, given)
		}
		forget()
		dialog.Hide()
	}

	var show func(int)
	var syncFooter func()
	pages := make([]*questionPage, len(request.Questions))
	for i := range request.Questions {
		pages[i] = newQuestionPage(form, i, func(tapped bool) {
			syncFooter()
			if tapped {
				ui.window.Canvas().Focus(dialog) // keys (numbers, Enter) go to the sheet again
			}
		})
		pages[i].other.send = func() { advance(form, current, show, func() { finish(form.answers()) }) }
		pages[i].other.steer = pages[i].other.send
		pages[i].other.escape = func() { dialog.dismiss() }
	}
	stack := container.New(&tallestLayout{}) // as tall as the tallest question: the sheet keeps its size
	for _, page := range pages {
		stack.Add(page.box)
	}

	back := newRow("Back", nil, styleOutline, func() { show(current - 1) })
	next := newRow("Next", nil, styleOutline, func() { show(current + 1) })
	send := newRow("", nil, stylePrimary, func() {
		if missing := form.firstMissing(); missing >= 0 {
			show(missing) // says what is missing: takes the user there
			return
		}
		finish(form.answers())
	})
	footer := container.New(layout.NewCustomPaddedHBoxLayout(8), back, layout.NewSpacer(), next, send)
	syncFooter = func() {
		back.Hidden = current == 0
		next.Hidden = current == len(pages)-1
		send.text = form.sendLabel()
		send.style = styleOutline
		if form.missing() == 0 {
			send.style = stylePrimary
		}
		send.center = true
		send.Refresh()
		footer.Refresh()
	}
	show = func(index int) {
		if index < 0 || index >= len(pages) {
			return
		}
		current = index
		for i, page := range pages {
			page.box.Hidden = i != index
		}
		stack.Refresh()
		syncFooter()
		if dialog != nil {
			dialog.Refresh()
			ui.window.Canvas().Focus(dialog)
		}
	}

	title := "1loop has a question"
	if len(request.Questions) > 1 {
		title = "1loop has questions"
	}
	if chat != ui.current {
		title += " · " + chat.Title
	}
	closeButton := newRow("", iconClose, styleWindow, func() { dialog.dismiss() })
	heading := textLine(title, 16, colText)
	heading.TextStyle = fyne.TextStyle{Bold: true}
	content := container.NewBorder(
		padded(0, 12, 0, 0, container.NewBorder(nil, nil, nil, closeButton, container.NewHBox(heading))),
		padded(16, 0, 0, 0, footer), nil, nil, stack)
	show(0)
	dialog = newSheet(ui.window.Canvas(), content, 640)
	dialog.onDismiss = func() {
		if !resolved && chat.run != nil && chat.run.questions == dialog && chat.run.turn != nil {
			ui.applyActivity(chat.run.turn) // the line now says how to reopen them
		}
	}
	dialog.onKey = func(e *fyne.KeyEvent) bool {
		switch e.Name {
		case fyne.KeyReturn, fyne.KeyEnter:
			advance(form, current, show, func() { finish(form.answers()) })
		case fyne.KeyLeft:
			show(current - 1)
		case fyne.KeyRight:
			show(current + 1)
		default:
			return false
		}
		return true
	}
	// 1 to 9 (and 0 for a tenth) pick an option; the number after the last
	// option goes to the Other field.
	dialog.onRune = func(r rune) {
		if r < '0' || r > '9' {
			return
		}
		number := int(r - '0')
		if number == 0 {
			number = 10
		}
		page := pages[current]
		switch {
		case number <= len(page.cards):
			page.cards[number-1].Tapped(nil)
		case number == len(page.cards)+1:
			ui.window.Canvas().Focus(page.other)
		}
	}
	dialog.Show()
	ui.window.Canvas().Focus(dialog)
	ui.notifyUser(chat, request.Questions[0].Question)
	if chat.run != nil {
		chat.run.questions, chat.run.questionID, chat.run.questionList = dialog, request.ID, request.Questions
		chat.run.answerQuestions = finish
		ui.remote.metaChanged(chat)
		ui.syncTray()
	}
	return dialog
}

// advance is Enter: the next question once this one has an answer, and the
// answers once they are all there.
func advance(form *questionForm, current int, show func(int), send func()) {
	switch {
	case form.missing() == 0:
		send()
	case form.answered(current) && current+1 < len(form.questions):
		show(current + 1)
	case form.answered(current):
		show(form.firstMissing())
	}
}

// questionPage is one question: its header and count, the question, its
// choice cards and the Other field.
type questionPage struct {
	box   *fyne.Container
	cards []*choiceCard
	other *chatEntry
}

// changed runs after each change: tapped for a card, not for typing.
func newQuestionPage(form *questionForm, index int, changed func(tapped bool)) *questionPage {
	question := form.questions[index]
	page := &questionPage{other: newChatEntry()}
	var otherCard *choiceCard
	refresh := func() {
		for i, card := range page.cards {
			card.setPicked(form.picked[index][question.Options[i].Label])
		}
		otherCard.setPicked(strings.TrimSpace(form.other[index]) != "")
	}

	top := container.NewHBox()
	if question.Header != "" {
		top.Add(tag(question.Header, colMuted, colSelected))
	}
	top.Add(layout.NewSpacer())
	if len(form.questions) > 1 {
		count := textLine(fmt.Sprintf("%d/%d", index+1, len(form.questions)), 12, colMuted)
		count.TextStyle = fyne.TextStyle{Monospace: true}
		top.Add(count)
	}
	text := widget.NewLabel(question.Question)
	text.Wrapping = fyne.TextWrapWord
	text.TextStyle = fyne.TextStyle{Bold: true}
	hint := "Pick one, or write your own"
	if question.MultiSelect {
		hint = "Pick any that apply, or add your own"
	}
	if len(question.Options) > 0 {
		hint += fmt.Sprintf(" · keys 1–%d", min(len(question.Options)+1, 10))
	}

	cards := container.New(layout.NewCustomPaddedVBoxLayout(6))
	for _, option := range question.Options {
		option := option
		shown, recommended := recommendedLabel(option.Label)
		label := textLine(shown, 14, colText)
		head := container.New(layout.NewCustomPaddedHBoxLayout(8), label)
		if recommended {
			head.Add(tag("Recommended", colAmber, withAlpha(colAmber, 0.14)))
		}
		body := container.New(layout.NewCustomPaddedVBoxLayout(2), head)
		if option.Description != "" {
			description := widget.NewLabel(option.Description)
			description.Wrapping = fyne.TextWrapWord
			description.Importance = widget.LowImportance
			body.Add(padded(-8, -8, -8, -8, description)) // the label's own padding would double the card's
		}
		card := newChoiceCard(body, question.MultiSelect, func() {
			form.pick(index, option.Label)
			if !question.MultiSelect {
				page.other.SetText("") // a pick replaces the written answer
			}
			refresh()
			changed(true)
		})
		page.cards = append(page.cards, card)
		cards.Add(card)
	}
	page.other.SetPlaceHolder("Other: type your own answer")
	page.other.OnChanged = func(text string) {
		page.other.fit()
		form.write(index, text)
		refresh()
		changed(false)
	}
	otherCard = newChoiceCard(page.other, question.MultiSelect, nil)
	otherCard.onTap = func() { fyne.CurrentApp().Driver().CanvasForObject(page.other).Focus(page.other) }
	otherCard.field = true
	cards.Add(otherCard)

	page.box = container.New(layout.NewCustomPaddedVBoxLayout(6), top, padded(-4, 0, -8, -8, text), textLine(hint, 12, colFaint), padded(6, 0, 0, 0, cards))
	refresh()
	return page
}

// tag is a small rounded label: a question's header, the Recommended badge.
func tag(text string, ink, fill color.NRGBA) fyne.CanvasObject {
	label := textLine(text, 11, ink)
	label.TextStyle = fyne.TextStyle{Bold: true}
	background := canvas.NewRectangle(fill)
	background.CornerRadius = 6
	return container.NewVBox(container.NewStack(background, padded(2, 2, 7, 7, label)))
}

// tallestLayout stacks its objects, shown or not, at the height of the
// tallest, so moving between questions does not resize the sheet.
type tallestLayout struct{}

func (*tallestLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	for _, object := range objects {
		size = size.Max(object.MinSize())
	}
	return size
}

func (*tallestLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(fyne.NewSize(size.Width, object.MinSize().Height))
	}
}

// choiceCard is an option: a radio (a checkbox on a multi-select) beside its
// content, lit with the accent once picked. field: the content is the Other
// entry, which takes the clicks.
type choiceCard struct {
	widget.BaseWidget
	content fyne.CanvasObject
	multi   bool
	field   bool
	picked  bool
	hovered bool
	pressed bool
	onTap   func()
}

func newChoiceCard(content fyne.CanvasObject, multi bool, onTap func()) *choiceCard {
	card := &choiceCard{content: content, multi: multi, onTap: onTap}
	card.ExtendBaseWidget(card)
	return card
}

func (card *choiceCard) setPicked(picked bool) {
	if card.picked != picked {
		card.picked = picked
		card.Refresh()
	}
}

func (card *choiceCard) Tapped(*fyne.PointEvent) {
	if card.onTap != nil {
		card.onTap()
	}
}

func (card *choiceCard) Cursor() desktop.Cursor              { return desktop.PointerCursor }
func (card *choiceCard) MouseIn(*desktop.MouseEvent)         { card.hovered = true; card.Refresh() }
func (card *choiceCard) MouseMoved(*desktop.MouseEvent)      {}
func (card *choiceCard) MouseOut()                           { card.hovered, card.pressed = false, false; card.Refresh() }
func (card *choiceCard) MouseDown(*desktop.MouseEvent)       { card.pressed = true; card.Refresh() }
func (card *choiceCard) MouseUp(*desktop.MouseEvent)         { card.pressed = false; card.Refresh() }
func (card *choiceCard) CreateRenderer() fyne.WidgetRenderer { return newChoiceCardRenderer(card) }

type choiceCardRenderer struct {
	card  *choiceCard
	bg    *canvas.Rectangle
	box   *canvas.Rectangle // the radio's ring, the checkbox's square
	dot   *canvas.Rectangle // the radio's dot
	check *canvas.Image     // the checkbox's tick
}

func newChoiceCardRenderer(card *choiceCard) *choiceCardRenderer {
	renderer := &choiceCardRenderer{card: card, bg: canvas.NewRectangle(color.Transparent), box: canvas.NewRectangle(color.Transparent),
		dot: canvas.NewRectangle(colAmber), check: canvas.NewImageFromResource(tinted(iconCheck, colInk))}
	renderer.bg.CornerRadius, renderer.bg.StrokeWidth = 10, 1
	renderer.box.StrokeWidth = 1.5
	renderer.box.CornerRadius = 8
	renderer.dot.CornerRadius = 4
	if card.multi {
		renderer.box.CornerRadius = 4
	}
	renderer.check.FillMode = canvas.ImageFillContain
	renderer.Refresh()
	return renderer
}

const (
	choicePadX, choicePadY = 12, 10
	choiceMark             = 16
	choiceGap              = 12
)

func (renderer *choiceCardRenderer) Layout(size fyne.Size) {
	renderer.bg.Resize(size)
	padY := float32(choicePadY)
	if renderer.card.field {
		padY = 4
	}
	inset := float32(choicePadX + choiceMark + choiceGap)
	contentHeight := size.Height - 2*padY
	markY := padY + 2 // beside the first line
	if renderer.card.field {
		markY = (size.Height - choiceMark) / 2
	}
	renderer.box.Move(fyne.NewPos(choicePadX, markY))
	renderer.box.Resize(fyne.NewSquareSize(choiceMark))
	renderer.dot.Move(fyne.NewPos(choicePadX+4, markY+4))
	renderer.dot.Resize(fyne.NewSquareSize(choiceMark - 8))
	renderer.check.Move(fyne.NewPos(choicePadX+1, markY+1))
	renderer.check.Resize(fyne.NewSquareSize(choiceMark - 2))
	contentX := inset
	if renderer.card.field {
		contentX -= 8 // the entry pads its own text
	}
	renderer.card.content.Move(fyne.NewPos(contentX, padY))
	renderer.card.content.Resize(fyne.NewSize(size.Width-contentX-choicePadX, contentHeight))
}

func (renderer *choiceCardRenderer) MinSize() fyne.Size {
	padY := float32(choicePadY)
	if renderer.card.field {
		padY = 4
	}
	min := renderer.card.content.MinSize()
	return fyne.NewSize(min.Width+choicePadX*2+choiceMark+choiceGap, fyne.Max(min.Height, choiceMark)+2*padY)
}

func (renderer *choiceCardRenderer) Refresh() {
	card := renderer.card
	fill, stroke, ring := overlay(0x05), colBorder, colBorderHot
	switch {
	case card.picked:
		fill, stroke, ring = withAlpha(colAmber, 0.10), colAmber, colAmber
	case card.pressed:
		fill, stroke = overlay(0x16), colBorderHot
	case card.hovered:
		fill, stroke = colHover, colBorderHot
	}
	if card.picked && card.pressed {
		fill = withAlpha(colAmber, 0.18)
	}
	renderer.bg.FillColor, renderer.bg.StrokeColor = fill, stroke
	renderer.box.StrokeColor = ring
	renderer.box.FillColor = color.Transparent
	renderer.dot.Hidden = card.multi || !card.picked
	renderer.check.Hidden = !card.multi || !card.picked
	if card.multi && card.picked {
		renderer.box.FillColor = colAmber
	}
	renderer.bg.Refresh()
	renderer.box.Refresh()
	renderer.dot.Refresh()
	renderer.check.Refresh()
}

func (renderer *choiceCardRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.bg, renderer.box, renderer.dot, renderer.check, renderer.card.content}
}

func (renderer *choiceCardRenderer) Destroy() {}

// reopenQuestions shows the open chat's questions again after they were
// closed without an answer.
func (ui *desktopUI) reopenQuestions() {
	if ui.current == nil || ui.current.run == nil {
		return
	}
	run := ui.current.run
	if run.questions == nil || run.questions.open {
		return
	}
	run.questions.Show()
	ui.window.Canvas().Focus(run.questions)
}
