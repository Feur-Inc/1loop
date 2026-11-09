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
