package ui

// The model's questions (request_user_input): each with its options and an
// "Other" line to answer in one's own words. The run waits for the answers;
// the phone can answer them too (see phone_server.go). Closing the sheet
// only sets the questions aside: a tap on the activity line brings them back.

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

func (ui *desktopUI) showQuestions(chat *conversation, request agent.QuestionRequest) *sheet {
	answers := map[string]string{}
	resolved := false
	var dialog *sheet
	var send *tapRow
	ready := func() {
		for _, question := range request.Questions {
			if strings.TrimSpace(strings.TrimPrefix(answers[question.ID], agent.OtherAnswer)) == "" {
				send.disabled = true
				send.Refresh()
				return
			}
		}
		send.disabled = false
		send.Refresh()
	}
	forget := func() {
		if chat.run != nil && chat.run.questions == dialog {
			chat.run.questions, chat.run.questionID, chat.run.answerQuestions = nil, "", nil
			ui.remote.metaChanged(chat)
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
	body := container.New(layout.NewCustomPaddedVBoxLayout(18))
	for _, question := range request.Questions {
		question := question
		var rows []*tapRow
		other := newChatEntry()
		other.SetPlaceHolder("Other: your own answer")
		mark := func(chosen *tapRow) {
			for _, row := range rows {
				row.selected = row == chosen
				row.badge = blankIcon
				if row.selected {
					row.badge = iconCheck
				}
				row.Refresh()
			}
		}
		block := container.New(layout.NewCustomPaddedVBoxLayout(4))
		if question.Header != "" {
			header := textLine(strings.ToUpper(question.Header), 11, colFaint)
			header.TextStyle = fyne.TextStyle{Bold: true}
			block.Add(header)
		}
		text := widget.NewLabel(question.Question)
		text.Wrapping = fyne.TextWrapWord
		block.Add(text)
		for _, option := range question.Options {
			label := option.Label
			var row *tapRow
			row = newRow(label, nil, styleModel, func() {
				answers[question.ID] = label
				other.SetText("")
				mark(row)
				ready()
			})
			row.detail, row.badge = option.Description, blankIcon
			rows = append(rows, row)
			block.Add(row)
		}
		other.OnChanged = func(text string) {
			other.fit()
			if strings.TrimSpace(text) != "" {
				answers[question.ID] = agent.OtherAnswer + strings.TrimSpace(text)
				mark(nil)
			} else if strings.HasPrefix(answers[question.ID], agent.OtherAnswer) {
				delete(answers, question.ID)
			}
			ready()
		}
		block.Add(other)
		body.Add(block)
	}
	send = newRow("Send answers", nil, stylePrimary, func() {
		if !send.disabled {
			finish(answers)
		}
	})
	send.disabled = true
	title := "1loop has a question"
	if len(request.Questions) > 1 {
		title = "1loop has questions"
	}
	if chat != ui.current {
		title += " · " + chat.Title
	}
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(0, min(520, body.MinSize().Height)))
	dialog = ui.modal(title, scroll, 640, func() {
		if !resolved && chat.run != nil && chat.run.questions == dialog && chat.run.turn != nil {
			ui.applyActivity(chat.run.turn) // the line now says how to reopen them
		}
	}, send)
	ui.notifyUser(chat, request.Questions[0].Question)
	if chat.run != nil {
		chat.run.questions, chat.run.questionID, chat.run.questionList = dialog, request.ID, request.Questions
		chat.run.answerQuestions = finish
		ui.remote.metaChanged(chat)
	}
	return dialog
}

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
