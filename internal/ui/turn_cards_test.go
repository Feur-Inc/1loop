// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// Over a picture or shader, each run of the agent's rows (an answer, its
// tool rows) has one rounded card behind it, a margin around them; the
// user's messages and the gaps between turns have none (#503). Before,
// each answer's text had a square backdrop of its own and tool rows none.
func TestTurnCards(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	saved := shaderBehind
	t.Cleanup(func() { shaderBehind = saved })
	shaderBehind = true
	ui := &desktopUI{}
	ui.items = &transcriptLayout{ui: ui}
	ui.items.reset()
	ui.items.cards = container.NewWithoutLayout()
	ui.transcript = container.New(ui.items)
	row := func(height float32) fyne.CanvasObject {
		r := canvas.NewRectangle(colText)
		r.SetMinSize(fyne.NewSize(100, height))
		return r
	}
	answer, tool, user, next := row(60), row(30), row(40), row(50)
	ui.items.alone(user)
	ui.transcript.Objects = []fyne.CanvasObject{answer, tool, user, next}
	ui.transcript.Resize(fyne.NewSize(700, 1000))
	relayout(ui.transcript)

	var shown []fyne.CanvasObject
	for _, card := range ui.items.cards.Objects {
		if card.Visible() {
			shown = append(shown, card)
		}
	}
	if len(shown) != 2 {
		t.Fatalf("%d cards, want 2: the answer with its tool row, then the next answer", len(shown))
	}
	first, second := shown[0], shown[1]
	if first.Position().Y != answer.Position().Y-cardPadY || first.Position().Y+first.Size().Height != tool.Position().Y+tool.Size().Height+cardPadY {
		t.Errorf("first card %v %v, rows from %v to %v", first.Position(), first.Size(), answer.Position().Y, tool.Position().Y+tool.Size().Height)
	}
	if first.Position().X != -cardPadX || first.Size().Width != 700+2*cardPadX {
		t.Errorf("first card's margin: %v %v", first.Position(), first.Size())
	}
	if top, bottom := user.Position().Y, user.Position().Y+user.Size().Height; first.Position().Y+first.Size().Height > top || second.Position().Y < bottom {
		t.Errorf("a card covers the user's message (%v to %v): %v, %v", top, bottom, first.Size(), second.Position())
	}

	shaderBehind = false // a plain background: no card
	relayout(ui.transcript)
	for _, card := range ui.items.cards.Objects {
		if card.Visible() {
			t.Fatal("a card over the plain background")
		}
	}
}
