// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"reflect"
	"testing"

	"1loop/internal/agent"
)

func testQuestions() []agent.Question {
	return []agent.Question{
		{ID: "scope", Question: "Which?", Options: []agent.QuestionOption{{Label: "Both (Recommended)"}, {Label: "Integration"}, {Label: "Add-on"}}},
		{ID: "parts", Question: "Which parts?", MultiSelect: true, Options: []agent.QuestionOption{{Label: "Docs"}, {Label: "Tests"}, {Label: "CI"}}},
	}
}

func TestQuestionFormSingleChoice(t *testing.T) {
	form := newQuestionForm(testQuestions())
	form.pick(0, "Integration")
	form.pick(0, "Both (Recommended)")
	if got := form.answer(0); got != "Both (Recommended)" { // the label as the model wrote it
		t.Fatalf("answer: %q", got)
	}
	form.write(0, "  a bit of both  ")
	if got := form.answer(0); got != agent.OtherAnswer+"a bit of both" {
		t.Fatalf("written answer: %q", got)
	}
	if len(form.picked[0]) != 0 {
		t.Fatalf("writing kept the option picked: %v", form.picked[0])
	}
	form.write(0, "   ")
	if form.answered(0) {
		t.Fatalf("a blank Other is no answer: %q", form.answer(0))
	}
	form.write(0, "mine")
	form.pick(0, "Add-on")
	if got := form.answer(0); got != "Add-on" || form.other[0] != "" {
		t.Fatalf("a pick replaces the written answer: %q, other %q", got, form.other[0])
	}
}

func TestQuestionFormMultiSelect(t *testing.T) {
	form := newQuestionForm(testQuestions())
	form.pick(1, "CI")
	form.pick(1, "Docs")
	form.pick(1, "Tests")
	form.pick(1, "Tests")                         // a second click unpicks it
	if got := form.answer(1); got != "Docs, CI" { // in the options' order
		t.Fatalf("answer: %q", got)
	}
	form.write(1, "release notes")
	if got := form.answer(1); got != "Docs, CI, release notes" {
		t.Fatalf("with own words: %q", got)
	}
	form.pick(1, "Docs")
	form.pick(1, "CI")
	if got := form.answer(1); got != agent.OtherAnswer+"release notes" {
		t.Fatalf("own words alone: %q", got)
	}
}

func TestQuestionFormMissing(t *testing.T) {
	form := newQuestionForm(testQuestions())
	if form.sendLabel() != "Answer 2 more questions" || form.firstMissing() != 0 {
		t.Fatalf("nothing answered: %q, first %d", form.sendLabel(), form.firstMissing())
	}
	form.pick(1, "Docs")
	if form.sendLabel() != "Answer 1 more question" || form.firstMissing() != 0 {
		t.Fatalf("one answered: %q, first %d", form.sendLabel(), form.firstMissing())
	}
	form.write(0, "my way")
	if form.sendLabel() != "Send answers" || form.firstMissing() != -1 {
		t.Fatalf("all answered: %q", form.sendLabel())
	}
	want := map[string]string{"scope": agent.OtherAnswer + "my way", "parts": "Docs"}
	if got := form.answers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("answers: %v, want %v", got, want)
	}

	one := newQuestionForm(testQuestions()[:1])
	if one.sendLabel() != "Choose an answer" {
		t.Fatalf("one question: %q", one.sendLabel())
	}
	one.pick(0, "Add-on")
	if one.sendLabel() != "Send answer" {
		t.Fatalf("one question answered: %q", one.sendLabel())
	}
}

func TestRecommendedLabel(t *testing.T) {
	for label, want := range map[string]struct {
		shown       string
		recommended bool
	}{
		"Both (Recommended)":  {"Both", true},
		"Both (recommended) ": {"Both", true},
		"Integration only":    {"Integration only", false},
		"(Recommended)":       {"(Recommended)", false},
	} {
		shown, recommended := recommendedLabel(label)
		if shown != want.shown || recommended != want.recommended {
			t.Errorf("%q: %q %v, want %q %v", label, shown, recommended, want.shown, want.recommended)
		}
	}
}

// questionWindow is a window with a running chat open, ready to be asked.
func questionWindow(t *testing.T) (*desktopUI, *conversation) {
	t.Helper()
	window, _ := deleteTreeWindow(t)
	chat := window.chatByID("a1") // running
	window.openLocalChat(chat)
	return window, chat
}

func oneQuestion() agent.QuestionRequest {
	return agent.QuestionRequest{ID: "r1", Questions: []agent.Question{
		{ID: "planet", Question: "Which planet has the most moons?", Options: []agent.QuestionOption{
			{Label: "Jupiter", Description: "The largest planet."}, {Label: "Saturn", Description: "The ringed giant."}}}}}
}

// The questions dock above the composer of the chat that asked: no sheet
// over the window, and the composer offers a direct reply (#434).
func TestQuestionsDockAboveTheComposer(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, oneQuestion())
	if len(window.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("the questions opened over the window")
	}
	if !window.questionSlot.Visible() || window.shownQuestions != card || len(window.questionSlot.Objects) != 1 || window.questionSlot.Objects[0] != card {
		t.Fatal("the card is not above the composer")
	}
	if window.prompt.PlaceHolder != "Or reply directly…" {
		t.Fatalf("placeholder %q", window.prompt.PlaceHolder)
	}
	if chat.run.questions != card || chat.run.questionID != "r1" {
		t.Fatal("the run does not know its questions: the phone can't answer them")
	}
}

// One click answers a lone question, as in Claude, and the card goes.
func TestQuestionCardPickAnswers(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, oneQuestion())
	card.pick(1)
	if got := card.form.answers(); !reflect.DeepEqual(got, map[string]string{"planet": "Saturn"}) {
		t.Fatalf("answers %v", got)
	}
	if !card.resolved || window.questionSlot.Visible() || chat.asking != nil || chat.run.questions != nil {
		t.Fatal("the card stayed after its answer")
	}
	if window.prompt.PlaceHolder == "Or reply directly…" {
		t.Fatal("the composer still offers a reply")
	}
}

// A message sent from the composer answers in the user's own words; it is
// not queued for the next turn.
func TestQuestionCardReplyFromComposer(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, oneQuestion())
	window.prompt.SetText("Neither, it's Earth")
	window.submit()
	if got := card.form.answer(0); got != agent.OtherAnswer+"Neither, it's Earth" {
		t.Fatalf("answer %q", got)
	}
	if !card.resolved || len(chat.queue) != 0 || window.prompt.Text != "" {
		t.Fatalf("resolved %v, queue %d, prompt %q", card.resolved, len(chat.queue), window.prompt.Text)
	}
}

// × folds the card into a bar above the composer; Show brings it back, and
// the keys pick again.
func TestQuestionCardSetAsideAndBack(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, oneQuestion())
	card.setAside()
	if card.shown() || !window.questionSlot.Visible() || window.questionSlot.Objects[0] != card.bar {
		t.Fatal("set aside: the bar should replace the card")
	}
	if window.prompt.PlaceHolder == "Or reply directly…" {
		t.Fatal("set aside: the composer is a composer again")
	}
	window.reopenQuestions()
	if !card.shown() || window.questionSlot.Objects[0] != card {
		t.Fatal("the card did not come back")
	}
	card.TypedRune('1')
	if got := card.form.answers()["planet"]; got != "Jupiter" {
		t.Fatalf("key 1 picked %q", got)
	}
}

// The card stays with its chat: another chat shows none, and the card is
// back when its chat is opened again.
func TestQuestionCardFollowsItsChat(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, oneQuestion())
	window.openLocalChat(window.chatByID("other"))
	if window.questionSlot.Visible() || window.shownQuestions != nil {
		t.Fatal("another chat shows the questions")
	}
	window.openLocalChat(chat)
	if !window.questionSlot.Visible() || window.shownQuestions != card {
		t.Fatal("the chat's questions did not come back")
	}
}

// Skip answers nothing: the model hears the questions were closed.
func TestQuestionCardSkip(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, oneQuestion())
	card.finish(nil)
	if !card.resolved || window.questionSlot.Visible() || chat.asking != nil {
		t.Fatal("Skip left the card")
	}
}

// Several questions go one after the other: a single choice moves on, a
// multi-select waits for its button, and all are sent together.
func TestQuestionCardSeveralQuestions(t *testing.T) {
	window, chat := questionWindow(t)
	card := window.showQuestions(chat, agent.QuestionRequest{ID: "r2", Questions: testQuestions()})
	if !card.footer.Visible() {
		t.Fatal("several questions need Back and Next")
	}
	card.pick(1)
	if card.current != 1 || card.resolved {
		t.Fatalf("after the first pick: question %d, resolved %v", card.current, card.resolved)
	}
	card.pick(0)
	card.pick(2)
	if card.resolved {
		t.Fatal("a multi-select sent before its button")
	}
	card.send.Tapped(nil)
	if !card.resolved || !reflect.DeepEqual(card.form.answers(), map[string]string{"scope": "Integration", "parts": "Docs, CI"}) {
		t.Fatalf("resolved %v, answers %v", card.resolved, card.form.answers())
	}
}
