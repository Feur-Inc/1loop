// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

// word is the word wordAt finds in text around the first rune of marked,
// the text as one piece.
func word(text, marked string) string {
	runes := []rune(text)
	at := len([]rune(text[:strings.Index(text, marked)]))
	start, end := wordAt(runes, nil, at)
	return string(runes[start:end])
}

func TestWordAt(t *testing.T) {
	for _, c := range []struct{ text, at, want string }{
		{"select foo_bar now", "bar", "foo_bar"},
		{"I don't know", "t know", "don't"},
		{"it’s here", "s here", "it’s"},
		{"about 1,000.5 units", "000", "1,000.5"},
		{"open main.go first", "go first", "main.go"},
		{"see https://example.com/a?b=c&d=e, then", "example", "https://example.com/a?b=c&d=e"},
		{"(https://en.wikipedia.org/wiki/Go_(programming_language)).", "wiki/", "https://en.wikipedia.org/wiki/Go_(programming_language)"},
		{"edit internal/ui/selection.go.", "ui", "internal/ui/selection.go"},
		{"in ~/projects/1loop: build", "projects", "~/projects/1loop"},
		{"write to someone@example.org.", "example", "someone@example.org"},
		{"one, two", ",", ","},
		{"stop... now", ".", "."},
		{"a  b", " ", "  "},
		{"café au lait", "fé", "café"},
		{"Привет мир", "мир", "мир"},
		{"done 👍🏽 ok", "👍", "👍🏽"},
		{"x 👩‍💻 y", "💻", "👩‍💻"},
		{"well-known", "known", "known"},
		{"end. Next", "Next", "Next"},
	} {
		if got := word(c.text, c.at); got != c.want {
			t.Errorf("in %q at %q: got %q, want %q", c.text, c.at, got, c.want)
		}
	}
}

// Words go across the style runs of one line: the pieces of "**bold**er
// `code`" make one text with nothing between them.
func TestWordAcrossPieces(t *testing.T) {
	text := textOf([]selPiece{
		{runes: []rune("un")}, {runes: []rune("bold")}, {runes: []rune("ed text"), after: " "}, {runes: []rune("next")},
	})
	start, end := text.unitAt(byWord, 3)
	if got := string(text.runes[start:end]); got != "unbolded" {
		t.Fatalf("across pieces: %q", got)
	}
	if text.point(start, false) != (selPoint{0, 0}) || text.point(end, true) != (selPoint{2, 2}) {
		t.Fatalf("points %v %v", text.point(start, false), text.point(end, true))
	}
	// The wrap between "text" and "next" is a space: two words.
	start, end = text.unitAt(byWord, len([]rune("unbolded tex")))
	if got := string(text.runes[start:end]); got != "text" {
		t.Fatalf("at a wrap: %q", got)
	}
}

// A list mark and a formula are objects: a word of their own.
func TestWordAtObjects(t *testing.T) {
	text := textOf([]selPiece{{runes: []rune("• "), solid: true}, {runes: []rune("item"), after: "\n"}})
	if start, end := text.unitAt(byWord, 0); start != 0 || end != 2 {
		t.Fatalf("the mark: %d %d", start, end)
	}
	if start, end := text.unitAt(byWord, 3); string(text.runes[start:end]) != "item" {
		t.Fatalf("the item: %d %d", start, end)
	}
}

func TestParagraphAt(t *testing.T) {
	text := textOf([]selPiece{
		{runes: []rune("First "), after: ""}, {runes: []rune("para"), after: ""}, {runes: []rune("graph."), after: "\n\n"},
		{runes: []rune("cell"), after: "\t"}, {runes: []rune("other cell"), after: "\n"},
		{runes: []rune("fmt.Println()"), after: "\n"}, {runes: []rune(""), after: "\n"}, {runes: []rune("x := 1")},
	})
	for _, c := range []struct {
		at   string
		want string
	}{
		{"para", "First paragraph."},
		{"graph", "First paragraph."},
		{"other", "other cell"},
		{"cell\t", "cell"},
		{"Println", "fmt.Println()"},
		{"x :=", "x := 1"},
	} {
		at := len([]rune(string(text.runes)[:strings.Index(string(text.runes), c.at)]))
		start, end := text.unitAt(byParagraph, at)
		if got := string(text.runes[start:end]); got != c.want {
			t.Errorf("at %q: got %q, want %q", c.at, got, c.want)
		}
	}
}

func TestPointsAndOffsets(t *testing.T) {
	text := textOf([]selPiece{{runes: []rune("ab"), after: "\n\n"}, {runes: []rune("cd")}})
	if got := text.at(selPoint{0, 2}, true); got != 4 {
		t.Fatalf("a start at a piece's end is the next one's start: %d", got)
	}
	if got := text.at(selPoint{0, 2}, false); got != 2 {
		t.Fatalf("an end at a piece's end stays there: %d", got)
	}
	if got := text.point(3, true); got != (selPoint{0, 2}) {
		t.Fatalf("an end in a separator: %v", got)
	}
	if got := text.point(3, false); got != (selPoint{1, 0}) {
		t.Fatalf("a start in a separator: %v", got)
	}
}

func TestExtendSpan(t *testing.T) {
	text := textOf([]selPiece{{runes: []rune("alpha beta gamma delta")}})
	runes := string(text.runes)
	index := func(s string) int { return len([]rune(runes[:strings.Index(runes, s)])) }
	lo, hi := text.unitAt(byWord, index("gamma"))
	// Forward: to the end of the word under the pointer.
	from, to := extendSpan(text, byWord, lo, hi, index("lta"), index("lta"))
	if got := string(text.runes[min(from, to):max(from, to)]); got != "gamma delta" || from != lo {
		t.Fatalf("forward: %q (from %d)", got, from)
	}
	// Backward: from the end of the first word to the start of the one under the pointer.
	from, to = extendSpan(text, byWord, lo, hi, index("eta"), index("eta"))
	if got := string(text.runes[min(from, to):max(from, to)]); got != "beta gamma" || from != hi {
		t.Fatalf("backward: %q (from %d)", got, from)
	}
	// Inside the first word: just it.
	from, to = extendSpan(text, byWord, lo, hi, index("mma"), index("mma"))
	if from != lo || to != hi {
		t.Fatalf("inside: %d %d", from, to)
	}
	// By character: from the press to the caret.
	if from, to = extendSpan(text, byChar, 3, 3, 7, 8); from != 3 || to != 8 {
		t.Fatalf("by character: %d %d", from, to)
	}
}

func TestClickCounter(t *testing.T) {
	var counter clickCounter
	start := time.Unix(1000, 0)
	at := fyne.NewPos(10, 10)
	interval := 400 * time.Millisecond
	steps := []struct {
		after time.Duration
		at    fyne.Position
		want  int
	}{
		{0, at, 1},
		{200 * time.Millisecond, at.AddXY(2, 1), 2}, // a double click, a little off
		{400 * time.Millisecond, at.AddXY(2, 2), 3}, // a triple click
		{500 * time.Millisecond, at, 1},             // a fourth starts over
		{600 * time.Millisecond, at, 2},
		{1100 * time.Millisecond, at, 1},              // too late
		{1200 * time.Millisecond, at.AddXY(20, 0), 1}, // too far
	}
	for _, step := range steps {
		if got := counter.press(start.Add(step.after), step.at, interval); got != step.want {
			t.Fatalf("press at %v: count %d, want %d", step.after, got, step.want)
		}
	}
}
