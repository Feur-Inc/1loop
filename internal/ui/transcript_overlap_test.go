// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// misplaced lists what is drawn outside its place: a part that reaches
// past the bottom of what holds it, two parts on top of each other, or
// text drawn for another height than it was given.
func misplaced(object fyne.CanvasObject, path string) []string {
	var found []string
	switch object := object.(type) {
	case *flowText:
		if object.height > object.Size().Height+0.5 {
			found = append(found, fmt.Sprintf("%s: text %v high in %v", path, object.height, object.Size().Height))
		}
	case *fyne.Container:
		var shown []fyne.CanvasObject
		for _, child := range object.Objects {
			if child.Visible() {
				shown = append(shown, child)
			}
		}
		for i, child := range shown {
			at := fmt.Sprintf("%s/%d", path, i)
			if bottom := child.Position().Y + child.Size().Height; bottom > object.Size().Height+0.5 {
				found = append(found, fmt.Sprintf("%s: ends at %v, below its holder (%v)", at, bottom, object.Size().Height))
			}
			for _, other := range shown[i+1:] {
				if overlap(child, other) {
					found = append(found, fmt.Sprintf("%s: under another part", at))
				}
			}
			found = append(found, misplaced(child, at)...)
		}
	}
	return found
}

// overlap: the two cross, not one drawn behind the whole of the other (a
// background).
func overlap(a, b fyne.CanvasObject) bool {
	const slack = 0.5
	crossing := a.Position().X+a.Size().Width > b.Position().X+slack && b.Position().X+b.Size().Width > a.Position().X+slack &&
		a.Position().Y+a.Size().Height > b.Position().Y+slack && b.Position().Y+b.Size().Height > a.Position().Y+slack
	return crossing && !behind(a, b) && !behind(b, a)
}

func behind(a, b fyne.CanvasObject) bool {
	const slack = 0.5
	return a.Position().X >= b.Position().X-slack && a.Position().Y >= b.Position().Y-slack &&
		a.Position().X+a.Size().Width <= b.Position().X+b.Size().Width+slack &&
		a.Position().Y+a.Size().Height <= b.Position().Y+b.Size().Height+slack
}

// An answer streamed word by word, a list among its blocks, keeps each
// bullet under the previous one and stays above the row that follows it
// (#440): each rebuilt block was placed with the height its text had at a
// provisional width, and nothing laid it out again when the answer's
// height did not change.
func TestStreamedAnswerDoesNotOverlap(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui := &desktopUI{}
	ui.items = &transcriptLayout{ui: ui}
	ui.items.reset()
	ui.transcript = container.New(ui.items)
	ui.transcript.Resize(fyne.NewSize(712, 4000))

	answer := newMarkdownView("")
	next := canvas.NewRectangle(colText) // the row after the answer: a Copy row, a tool row
	next.SetMinSize(fyne.NewSize(712, 30))
	ui.transcript.Objects = []fyne.CanvasObject{answer.box, next}

	long := strings.Repeat("words that wrap across the column ", 6)
	full := "Here is the plan:\n\n- " + long + "\n- " + long + "\n- " + long + "\n  - " + long + "\n\n```go\nfmt.Println()\n```\n\nAnd " + long
	words := strings.SplitAfter(full, " ")
	text := ""
	for _, word := range words {
		text += word
		answer.ParseMarkdown(text)
		ui.items.changed(answer.box)
		relayout(ui.transcript)
		if found := misplaced(ui.transcript, "transcript"); len(found) > 0 {
			t.Fatalf("after %q:\n%s", text[max(0, len(text)-40):], strings.Join(found, "\n"))
		}
	}
}

// An answer of a chat being opened is measured hidden, then shown when it
// comes into view: its blocks are placed as in a streamed one.
func TestLoadedAnswerDoesNotOverlap(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui := &desktopUI{}
	ui.items = &transcriptLayout{ui: ui}
	ui.items.reset()
	ui.transcript = container.New(ui.items)
	ui.transcript.Resize(fyne.NewSize(712, 4000))

	long := strings.Repeat("words that wrap across the column ", 6)
	answer := newMarkdownView("Here is the plan:\n\n- " + long + "\n- " + long + "\n  - " + long + "\n    - " + long + "\n\nAnd " + long)
	next := canvas.NewRectangle(colText)
	next.SetMinSize(fyne.NewSize(712, 30))
	ui.transcript.Objects = []fyne.CanvasObject{answer.box, next}
	ui.items.measure(answer.box, 712)
	answer.box.Hide()
	relayout(ui.transcript)
	if found := misplaced(ui.transcript, "transcript"); len(found) > 0 {
		t.Fatal(strings.Join(found, "\n"))
	}
}
