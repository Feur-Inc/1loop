// SPDX-License-Identifier: MPL-2.0

package ui

// The model's questions (request_user_input): one at a time, in a card
// docked above the composer of the chat that asked (#434), each with its
// options in numbered rows and "Something else" to answer in one's own
// words. The run waits for the answers; the phone can answer them too (see
// phone_server.go). Closing the card only sets the questions aside: the bar
// left above the composer, or a tap on the activity line, brings them back.

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"
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

// questionCard is the model's questions while they wait for the user
// (#434). It sits above the composer of the chat that asked, as Claude shows
// them, so the conversation stays in sight and the rest of the app works.
// While it has the focus, 1 to 9 pick an option, Enter goes on and Escape
// sets it aside; any other key goes to the composer, where a message is a
// direct reply. A chat not on screen keeps its card until it is opened.
type questionCard struct {
	widget.BaseWidget
	view     *desktopUI // the pane showing it, else the one the chat asked in
	chat     *conversation
	request  agent.QuestionRequest
	form     *questionForm
	pages    []*questionPage
	current  int
	resolved bool
	aside    bool // closed with ×: a bar above the composer brings it back

	bg      *canvas.Rectangle
	body    *fyne.Container // the current question's page
	footer  *fyne.Container // Back and Next or Send, when one click can't answer everything
	back    *tapRow
	send    *tapRow
	content fyne.CanvasObject
	bar     fyne.CanvasObject // shown in the card's place while it is set aside
}

// showQuestions docks the model's questions above the chat's composer.
func (ui *desktopUI) showQuestions(chat *conversation, request agent.QuestionRequest) *questionCard {
	ui = ui.viewOf(chat).sheetView(chat) // the window showing it, else the one in use (windows.go)
	if chat.asking != nil {
		chat.asking.close()
	}
	card := newQuestionCard(ui, chat, request)
	chat.asking = card
	if chat.run != nil {
		chat.run.questions, chat.run.questionID, chat.run.questionList = card, request.ID, request.Questions
		chat.run.answerQuestions = card.finish
		ui.remote.metaChanged(chat)
		ui.syncTray()
	}
	ui.questionsChanged(chat)
	if view := card.view; view.current == chat && view.focused() == view && strings.TrimSpace(view.prompt.Text) == "" {
		card.focus() // the keys pick at once; typing still reaches the composer
	}
	ui.notifyUser(chat, request.Questions[0].Question)
	return card
}

func newQuestionCard(ui *desktopUI, chat *conversation, request agent.QuestionRequest) *questionCard {
	card := &questionCard{view: ui, chat: chat, request: request, form: newQuestionForm(request.Questions)}
	card.ExtendBaseWidget(card)
	for i := range request.Questions {
		card.pages = append(card.pages, newQuestionPage(card, i))
	}
	card.body = container.NewStack()
	card.back = newRow("Back", nil, styleOutline, func() { card.show(card.current - 1) })
	card.send = newRow("", nil, stylePrimary, func() {
		if missing := card.form.firstMissing(); missing >= 0 && missing != card.current {
			card.show(missing) // says what is missing: takes the user there
			return
		}
		card.advance()
	})
	card.send.center = true
	card.footer = container.New(layout.NewCustomPaddedHBoxLayout(8), card.back, layout.NewSpacer(), card.send)
	if !card.needsFooter() {
		card.footer.Hide()
	}
	card.bg = canvas.NewRectangle(colPopover)
	card.bg.CornerRadius, card.bg.StrokeWidth, card.bg.StrokeColor = rounded(12), 1, colBorderSoft
	card.content = container.NewStack(card.bg, padded(10, 10, 14, 10, container.New(layout.NewCustomPaddedVBoxLayout(8), card.body, card.footer)))
	card.bar = card.asideBar()
	card.show(0)
	return card
}

// needsFooter says whether the card needs its own buttons: one click on an
// option answers a single question, not several, nor a multi-select.
func (card *questionCard) needsFooter() bool {
	if len(card.request.Questions) > 1 {
		return true
	}
	return card.request.Questions[0].MultiSelect
}

// show turns to a question.
func (card *questionCard) show(index int) {
	if index < 0 || index >= len(card.pages) {
		return
	}
	card.current = index
	card.body.Objects = []fyne.CanvasObject{card.pages[index].box}
	card.body.Refresh()
	card.sync()
	if card.view != nil && card.view.questionSlot != nil {
		card.view.questionSlot.Refresh() // the card's height follows its question
	}
}

// sync draws what was picked, and the footer's buttons.
func (card *questionCard) sync() {
	page := card.pages[card.current]
	for i, option := range page.options {
		option.setPicked(card.form.picked[card.current][card.request.Questions[card.current].Options[i].Label])
	}
	page.otherRow.setPicked(strings.TrimSpace(card.form.other[card.current]) != "")
	card.back.Hidden = card.current == 0
	card.send.text = card.form.sendLabel()
	if card.form.missing() == 0 {
		card.send.style = stylePrimary
	} else if card.form.answered(card.current) && card.current+1 < len(card.pages) {
		card.send.text, card.send.style = "Next", stylePrimary
	} else {
		card.send.style = styleOutline
	}
	card.send.Refresh()
	card.footer.Refresh()
}

// advance is Enter: the next question once this one has an answer, and the
// answers once they are all there.
func (card *questionCard) advance() {
	advance(card.form, card.current, card.show, func() { card.finish(card.form.answers()) })
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

// pick chooses an option of the shown question. A single choice goes on at
// once, as Claude does: one click answers a lone question.
func (card *questionCard) pick(option int) {
	question := card.request.Questions[card.current]
	if option < 0 || option >= len(question.Options) {
		return
	}
	card.form.pick(card.current, question.Options[option].Label)
	if question.MultiSelect {
		card.sync()
		card.focus()
		return
	}
	card.pages[card.current].other.SetText("") // a pick replaces the written answer
	card.sync()
	card.advance()
}

// reply answers the shown question in the user's own words: a message sent
// from the composer while the card waits.
func (card *questionCard) reply(text string) {
	card.pages[card.current].other.SetText(text)
	card.form.write(card.current, text)
	card.sync()
	card.advance()
}

// finish sends the answers, nil to skip them all, and takes the card away.
func (card *questionCard) finish(given map[string]string) {
	if card.resolved {
		return
	}
	if card.chat.server != nil {
		card.chat.server.action("/api/answer", map[string]any{"id": card.request.ID, "answers": given})
	} else if card.view.backend != nil {
		card.view.backend.Answer(card.request.ID, given)
	}
	card.close()
}

// close takes the card away without answering: the run ended, or the server
// no longer asks.
func (card *questionCard) close() {
	if card.resolved {
		return
	}
	card.resolved = true
	chat := card.chat
	if chat.asking == card {
		chat.asking = nil
	}
	if run := chat.run; run != nil && run.questions == card {
		run.questions, run.questionID, run.answerQuestions = nil, "", nil
		card.view.remote.metaChanged(chat)
		card.view.syncTray()
	}
	card.view.questionsChanged(chat)
}

func (card *questionCard) dismiss() { card.close() }

// shown says whether the card is on show: not answered, nor set aside.
func (card *questionCard) shown() bool { return !card.resolved && !card.aside }

// setAside folds the card into a bar above the composer, for a look at the
// whole conversation; the bar, or the activity line, brings it back.
func (card *questionCard) setAside() {
	if card.resolved || card.aside {
		return
	}
	card.aside = true
	card.view.questionsChanged(card.chat)
	if run := card.chat.run; run != nil && run.turn != nil {
		card.view.applyActivity(run.turn) // the line now says how to reopen it
	}
	if view := card.view; view.prompt != nil && view.window != nil {
		view.window.Canvas().Focus(view.prompt)
	}
}

func (card *questionCard) reopen() {
	if card.resolved || !card.aside {
		return
	}
	card.aside = false
	card.view.questionsChanged(card.chat)
	if run := card.chat.run; run != nil && run.turn != nil {
		card.view.applyActivity(run.turn)
	}
	card.focus()
}

func (card *questionCard) focus() {
	if view := card.view; view != nil && view.window != nil && card.shown() {
		view.window.Canvas().Focus(card)
	}
}

// asideBar is what stays above the composer while the card is set aside.
func (card *questionCard) asideBar() fyne.CanvasObject {
	text := "1loop is waiting for your answer"
	if count := len(card.request.Questions); count > 1 {
		text = fmt.Sprintf("1loop is waiting for %d answers", count)
	}
	dot := canvas.NewCircle(colAmber)
	show := newRow("Show", nil, styleChip, card.reopen)
	skip := newRow("Skip", nil, styleChip, func() { card.finish(nil) })
	background := canvas.NewRectangle(colPopover)
	background.CornerRadius, background.StrokeWidth, background.StrokeColor = rounded(12), 1, colBorderSoft
	line := container.NewBorder(nil, nil,
		container.NewHBox(container.NewCenter(container.New(fixedSquare{8}, dot)), textLine(text, 13, colMuted)), container.NewHBox(skip, show))
	return container.NewStack(background, padded(4, 4, 14, 6, line))
}

func (card *questionCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(card.content)
}

// The card takes the focus for its keys; its outline shows when it has it.
func (card *questionCard) FocusGained() {
	card.bg.StrokeColor = withAlpha(colAmber, 0.55)
	card.bg.Refresh()
}

func (card *questionCard) FocusLost() {
	card.bg.StrokeColor = colBorderSoft
	card.bg.Refresh()
}

func (card *questionCard) Tapped(*fyne.PointEvent) { card.focus() }

// TypedRune: 1 to 9 (and 0 for a tenth) pick an option, the number after the
// last goes to "Something else"; any other key starts a reply in the
// composer, which is never more than a key away.
func (card *questionCard) TypedRune(r rune) {
	if r >= '0' && r <= '9' {
		number := int(r - '0')
		if number == 0 {
			number = 10
		}
		page := card.pages[card.current]
		switch {
		case number <= len(page.options):
			card.pick(number - 1)
		case number == len(page.options)+1:
			card.view.window.Canvas().Focus(page.other)
		}
		return
	}
	if view := card.view; view.prompt != nil {
		view.window.Canvas().Focus(view.prompt)
		view.prompt.TypedRune(r)
	}
}

func (card *questionCard) TypedKey(e *fyne.KeyEvent) {
	switch e.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		card.advance()
	case fyne.KeyLeft:
		card.show(card.current - 1)
	case fyne.KeyRight:
		card.show(card.current + 1)
	case fyne.KeyEscape:
		card.setAside()
	}
}

// questionsChanged draws a chat's questions again in the panes showing it.
func (ui *desktopUI) questionsChanged(chat *conversation) {
	panes := ui.allPanes()
	if len(panes) == 0 {
		panes = []*desktopUI{ui}
	}
	for _, pane := range panes {
		if pane.current == chat || pane.shownQuestions != nil && pane.shownQuestions.chat == chat {
			pane.renderQuestions()
		}
	}
}

// renderQuestions shows the open chat's questions above the composer: the
// card, the bar while it is set aside, nothing once they are answered.
func (ui *desktopUI) renderQuestions() {
	if ui.questionSlot == nil {
		return
	}
	var card *questionCard
	if ui.current != nil && ui.current.asking != nil && !ui.current.asking.resolved {
		card = ui.current.asking
	}
	ui.questionSlot.RemoveAll()
	ui.shownQuestions = card
	if card == nil {
		ui.questionSlot.Hide()
		ui.replyPlaceholder(false)
		return
	}
	card.view = ui
	if card.aside {
		ui.questionSlot.Add(card.bar)
	} else {
		ui.questionSlot.Add(card)
	}
	ui.replyPlaceholder(!card.aside)
	ui.questionSlot.Show()
	ui.questionSlot.Refresh()
	ui.relayoutColumn()
}

// replyPlaceholder says in the composer that a message answers the card.
func (ui *desktopUI) replyPlaceholder(reply bool) {
	if ui.prompt == nil {
		return
	}
	placeholders := promptPlaceholders
	if reply {
		placeholders = []string{"Or reply directly…"}
	}
	if slices.Equal(ui.prompt.placeholders, placeholders) {
		return
	}
	ui.prompt.placeholders = placeholders
	ui.prompt.SetPlaceHolder(placeholders[0])
	ui.prompt.fit()
}

// promptPlaceholders are the composer's own, longest first.
var promptPlaceholders = []string{"Describe what you want to build…", "What shall we build?", "Message…"}

// reopenQuestions shows the open chat's questions again after they were set
// aside: a tap on the activity line.
func (ui *desktopUI) reopenQuestions() {
	if ui.current != nil && ui.current.asking != nil {
		ui.current.asking.reopen()
	}
}

// questionPage is one question: the question, its options and "Something
// else", the user's own words.
type questionPage struct {
	box      *fyne.Container
	options  []*questionOption
	otherRow *questionOption
	other    *chatEntry
}

func newQuestionPage(card *questionCard, index int) *questionPage {
	question := card.form.questions[index]
	page := &questionPage{other: newChatEntry()}

	text := widget.NewLabel(question.Question)
	text.Wrapping = fyne.TextWrapWord
	text.TextStyle = fyne.TextStyle{Bold: true}
	right := container.NewHBox()
	if count := len(card.form.questions); count > 1 {
		position := textLine(fmt.Sprintf("%d of %d", index+1, count), 12, colMuted)
		right.Add(container.NewCenter(position))
	}
	right.Add(container.NewCenter(newRow("", iconClose, styleIcon, card.setAside)))
	top := container.NewBorder(nil, nil, nil, container.NewVBox(right), padded(-6, -6, -8, 0, text))
	head := container.NewVBox()
	if question.Header != "" {
		head.Add(container.NewHBox(tag(question.Header, colMuted, colSelected)))
	}
	head.Add(top)
	if question.MultiSelect {
		head.Add(textLine("Pick any that apply", 12, colFaint))
	}

	list := container.New(layout.NewCustomPaddedVBoxLayout(0))
	for i, option := range question.Options {
		shown, recommended := recommendedLabel(option.Label)
		label := widget.NewLabel(shown)
		label.Wrapping = fyne.TextWrapWord
		var title fyne.CanvasObject = padded(-8, -8, -8, -8, label) // the label's own padding would double the row's
		if recommended {
			title = container.NewBorder(nil, nil, nil, container.NewCenter(tag("Recommended", colAmber, withAlpha(colAmber, 0.14))), title)
		}
		body := container.New(layout.NewCustomPaddedVBoxLayout(2), title)
		if option.Description != "" {
			description := widget.NewLabel(option.Description)
			description.Wrapping = fyne.TextWrapWord
			description.Importance = widget.LowImportance
			body.Add(padded(-8, -8, -8, -8, description))
		}
		row := newQuestionOption(strconv.Itoa(i+1), nil, body, question.MultiSelect, func() { card.pick(i) })
		if i > 0 {
			list.Add(questionDivider())
		}
		page.options = append(page.options, row)
		list.Add(row)
	}

	page.other.SetPlaceHolder("Something else")
	page.other.OnChanged = func(text string) {
		page.other.fit()
		card.form.write(index, text)
		card.sync()
	}
	page.other.send = card.advance
	page.other.steer = card.advance
	page.other.escape = card.setAside
	skip := newRow("Skip", nil, styleChip, func() { card.finish(nil) })
	page.otherRow = newQuestionOption("", iconPencil, container.NewBorder(nil, nil, nil, container.NewCenter(skip), page.other), question.MultiSelect, nil)
	page.otherRow.field = true
	page.otherRow.onTap = func() { card.view.window.Canvas().Focus(page.other) }

	options := container.New(&cappedLayout{limit: card.optionsLimit}, container.NewVScroll(list))
	page.box = container.New(layout.NewCustomPaddedVBoxLayout(6), head, options, questionDivider(), page.otherRow)
	return page
}

// optionsLimit is the most room the options take before they scroll: the
// card leaves most of the conversation in sight.
func (card *questionCard) optionsLimit() float32 {
	if card.view == nil || card.view.window == nil {
		return 320
	}
	return max(160, card.view.window.Canvas().Size().Height*0.4)
}

// questionDivider is the hairline between options.
func questionDivider() fyne.CanvasObject {
	line := canvas.NewRectangle(colBorderSoft)
	line.SetMinSize(fyne.NewSize(0, 1))
	return padded(0, 0, 8, 8, line)
}

// cappedLayout is as tall as its content, up to limit: past it, the content
// (a scroll) scrolls.
type cappedLayout struct{ limit func() float32 }

func (l *cappedLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	for _, object := range objects {
		inner := object
		if scroll, ok := object.(*container.Scroll); ok {
			inner = scroll.Content
		}
		size = size.Max(inner.MinSize())
	}
	size.Height = min(size.Height, l.limit())
	return size
}

func (l *cappedLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
}

// questionSlotLayout holds the card above the composer, gap apart.
type questionSlotLayout struct{ gap float32 }

func (l *questionSlotLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	for _, object := range objects {
		if object.Visible() {
			size = size.Max(object.MinSize())
		}
	}
	if size.Height > 0 {
		size.Height += l.gap
	}
	return size
}

func (l *questionSlotLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(fyne.NewSize(size.Width, max(0, size.Height-l.gap)))
	}
}

// fixedSquare lays out its objects as a square of side.
type fixedSquare struct{ side float32 }

func (l fixedSquare) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSquareSize(l.side) }

func (l fixedSquare) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos((size.Width-l.side)/2, (size.Height-l.side)/2))
		object.Resize(fyne.NewSquareSize(l.side))
	}
}

// tag is a small rounded label: a question's header, the Recommended badge.
func tag(text string, ink, fill color.NRGBA) fyne.CanvasObject {
	label := textLine(text, 11, ink)
	label.TextStyle = fyne.TextStyle{Bold: true}
	background := canvas.NewRectangle(fill)
	background.CornerRadius = rounded(6)
	return container.NewVBox(container.NewStack(background, padded(2, 2, 7, 7, label)))
}

// questionOption is a row of the card: its key in a keycap (a pencil for
// "Something else") beside its content. Picked, the keycap fills with the
// accent; on a multi-select it shows a tick. field: the content is the
// "Something else" entry, which takes the clicks.
type questionOption struct {
	widget.BaseWidget
	key     string
	icon    fyne.Resource
	content fyne.CanvasObject
	multi   bool
	field   bool
	picked  bool
	hovered bool
	pressed bool
	onTap   func()
}

func newQuestionOption(key string, icon fyne.Resource, content fyne.CanvasObject, multi bool, onTap func()) *questionOption {
	option := &questionOption{key: key, icon: icon, content: content, multi: multi, onTap: onTap}
	option.ExtendBaseWidget(option)
	return option
}

func (option *questionOption) setPicked(picked bool) {
	if option.picked != picked {
		option.picked = picked
		option.Refresh()
	}
}

func (option *questionOption) Tapped(*fyne.PointEvent) {
	if option.onTap != nil {
		option.onTap()
	}
}

func (option *questionOption) Cursor() desktop.Cursor { return desktop.PointerCursor }
func (option *questionOption) MouseIn(*desktop.MouseEvent) {
	option.hovered = true
	option.Refresh()
}
func (option *questionOption) MouseMoved(*desktop.MouseEvent) {}
func (option *questionOption) MouseOut() {
	option.hovered, option.pressed = false, false
	option.Refresh()
}
func (option *questionOption) MouseDown(*desktop.MouseEvent) {
	option.pressed = true
	option.Refresh()
}
func (option *questionOption) MouseUp(*desktop.MouseEvent) {
	option.pressed = false
	option.Refresh()
}

func (option *questionOption) CreateRenderer() fyne.WidgetRenderer {
	renderer := &questionOptionRenderer{option: option, bg: canvas.NewRectangle(color.Transparent),
		cap: canvas.NewRectangle(overlay(0x10)), number: textLine(option.key, 12, colMuted),
		tick: canvas.NewImageFromResource(tinted(iconCheck, colInk))}
	renderer.bg.CornerRadius = rounded(8)
	renderer.cap.CornerRadius, renderer.cap.StrokeWidth = rounded(6), 1
	renderer.number.Alignment = fyne.TextAlignCenter
	renderer.tick.FillMode = canvas.ImageFillContain
	if option.icon != nil {
		renderer.glyph = canvas.NewImageFromResource(tinted(option.icon, colMuted))
		renderer.glyph.FillMode = canvas.ImageFillContain
	}
	renderer.Refresh()
	return renderer
}

type questionOptionRenderer struct {
	option *questionOption
	bg     *canvas.Rectangle
	cap    *canvas.Rectangle // the keycap
	number *canvas.Text
	tick   *canvas.Image // a multi-select's picked mark
	glyph  *canvas.Image // the keycap's icon in place of a number
}

const (
	optionPadX, optionPadY = 8, 8
	optionCap              = 24
	optionGap              = 12
)

func (renderer *questionOptionRenderer) padY() float32 {
	if renderer.option.field {
		return 2
	}
	return optionPadY
}

func (renderer *questionOptionRenderer) Layout(size fyne.Size) {
	padY := renderer.padY()
	renderer.bg.Resize(size)
	capY := padY
	if renderer.option.field {
		capY = (size.Height - optionCap) / 2
	}
	capPos := fyne.NewPos(optionPadX, capY)
	renderer.cap.Move(capPos)
	renderer.cap.Resize(fyne.NewSquareSize(optionCap))
	renderer.number.Move(capPos)
	renderer.number.Resize(fyne.NewSize(optionCap, optionCap))
	renderer.tick.Move(capPos.AddXY(5, 5))
	renderer.tick.Resize(fyne.NewSquareSize(optionCap - 10))
	if renderer.glyph != nil {
		renderer.glyph.Move(capPos.AddXY(5, 5))
		renderer.glyph.Resize(fyne.NewSquareSize(optionCap - 10))
	}
	x := float32(optionPadX + optionCap + optionGap)
	if renderer.option.field {
		x -= 8 // the entry pads its own text
	}
	renderer.option.content.Move(fyne.NewPos(x, padY))
	renderer.option.content.Resize(fyne.NewSize(size.Width-x-optionPadX, size.Height-2*padY))
}

func (renderer *questionOptionRenderer) MinSize() fyne.Size {
	padY := renderer.padY()
	min := renderer.option.content.MinSize()
	return fyne.NewSize(min.Width+2*optionPadX+optionCap+optionGap, fyne.Max(min.Height, optionCap)+2*padY)
}

func (renderer *questionOptionRenderer) Refresh() {
	option := renderer.option
	fill, capFill, capStroke, ink := color.NRGBA{}, overlay(0x10), colBorderSoft, colMuted
	switch {
	case option.pressed && !option.field:
		fill = overlay(0x16)
	case option.hovered && !option.field:
		fill = colHover
	}
	if option.picked {
		fill, capFill, capStroke, ink = withAlpha(colAmber, 0.08), colAmber, colAmber, colInk
	}
	renderer.bg.FillColor = fill
	renderer.cap.FillColor, renderer.cap.StrokeColor = capFill, capStroke
	renderer.number.Color = ink
	renderer.number.Hidden = option.key == "" || option.multi && option.picked
	renderer.tick.Hidden = !(option.multi && option.picked && option.key != "")
	if renderer.glyph != nil {
		renderer.glyph.Resource = tinted(option.icon, ink)
		renderer.glyph.Refresh()
	}
	renderer.bg.Refresh()
	renderer.cap.Refresh()
	renderer.number.Refresh()
	renderer.tick.Refresh()
}

func (renderer *questionOptionRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{renderer.bg, renderer.cap, renderer.number, renderer.tick}
	if renderer.glyph != nil {
		objects = append(objects, renderer.glyph)
	}
	return append(objects, renderer.option.content)
}

func (renderer *questionOptionRenderer) Destroy() {}
