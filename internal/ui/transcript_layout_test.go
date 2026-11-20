// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/agent"
)

// checkTranscript fails when rows of the transcript on screen overlap or
// leave a gap, or when a row's slot is not the height its content needs:
// what #440 showed (an answer drawn over the next rows, big empty gaps).
func checkTranscript(t *testing.T, ui *desktopUI, when string) {
	t.Helper()
	ui.relayoutColumn()
	var prev fyne.CanvasObject
	shown := 0
	for _, object := range ui.transcript.Objects {
		if !object.Visible() {
			prev = nil
			continue
		}
		shown++
		if need := object.MinSize().Height; abs32(need-object.Size().Height) > 0.5 {
			t.Fatalf("%s: a row's slot is %.1f high, its content needs %.1f", when, object.Size().Height, need)
		}
		if prev != nil {
			want := prev.Position().Y + prev.Size().Height + transcriptGap
			if abs32(object.Position().Y-want) > 0.5 {
				t.Fatalf("%s: a row at %.1f, expected at %.1f (overlap or gap)", when, object.Position().Y, want)
			}
		}
		checkStack(t, object, when)
		prev = object
	}
	if shown == 0 {
		t.Fatalf("%s: nothing on screen", when)
	}
	// Hidden rows keep their places too: finding a message scrolls to them.
	y := float32(0)
	for i, object := range ui.transcript.Objects {
		if abs32(object.Position().Y-y) > 0.5 {
			t.Fatalf("%s: row %d at %.1f, expected at %.1f", when, i, object.Position().Y, y)
		}
		y += ui.items.heightOf(i) + transcriptGap
	}
}

// checkStack fails when the children of a vertical box inside a row
// overlap, or when one is laid out at another height than it needs.
func checkStack(t *testing.T, object fyne.CanvasObject, when string) {
	t.Helper()
	box, ok := object.(*fyne.Container)
	if !ok || !box.Visible() {
		return
	}
	var prev fyne.CanvasObject
	vertical := fmt.Sprintf("%T", box.Layout) == "*layout.vBoxLayout" || fmt.Sprintf("%T", box.Layout) == "*layout.customPaddedLayout"
	for _, child := range box.Objects {
		if !child.Visible() {
			continue
		}
		if vertical {
			if need := child.MinSize().Height; abs32(need-child.Size().Height) > 0.5 {
				t.Fatalf("%s: a %T inside a row is %.1f high, it needs %.1f (gap or overlap)", when, child, child.Size().Height, need)
			}
			if prev != nil && child.Position().Y < prev.Position().Y+prev.Size().Height-0.5 {
				t.Fatalf("%s: two parts of a row overlap", when)
			}
		}
		checkStack(t, child, when)
		prev = child
	}
}

// #440: streaming, a row inserted mid-turn, resizes and scrolling keep the
// transcript consistent: no overlap, no gap.
func TestTranscriptStaysConsistent(t *testing.T) {
	ui, chat := perfWindow(t, 120)
	checkTranscript(t, ui, "opened")

	current := ui.newTurn(chat, chat.Config)
	answer := "A list:\n\n" + strings.Repeat("- an item long enough to wrap on a narrow column, with **bold** and `code`\n", 8)
	for i, word := range strings.Fields(answer + " Then a paragraph that keeps growing while the agent writes it, word after word.") {
		current.lastParse = time.Time{}
		if i == 0 {
			current.handle(uiEvent{"delta", word})
		} else {
			current.handle(uiEvent{"delta", " " + word})
		}
		if i%7 == 0 {
			checkTranscript(t, ui, fmt.Sprintf("streaming, word %d", i))
		}
	}
	// A message sent while the agent works, then a tool row.
	ui.addItem(chat, agent.ChatItem{Kind: "user", Text: "And also this, sent mid-turn."})
	checkTranscript(t, ui, "message mid-turn")
	ui.addItem(chat, agent.ChatItem{Kind: "tool", Tool: "shell", Args: `{"command":"ls"}`, Text: "ok"})
	checkTranscript(t, ui, "tool row mid-turn")

	// Narrower (the side panel opens), wider, then scrolled up and down.
	for _, width := range []float32{760, 1400, 900} {
		ui.window.Resize(fyne.NewSize(width, 760))
		for range 50 {
			ui.remeasure()
		}
		checkTranscript(t, ui, fmt.Sprintf("resized to %.0f", width))
	}
	ui.followTail = false
	for _, at := range []float32{0, 0.3, 0.6, 1} {
		ui.scroll.ScrollToOffset(fyne.NewPos(0, at*(ui.scroll.Content.Size().Height-ui.scroll.Size().Height)))
		ui.relayoutColumn()
		checkTranscript(t, ui, fmt.Sprintf("scrolled to %.0f%%", at*100))
	}
	// Hidden rows that changed while out of view are measured again.
	ui.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	ui.relayoutColumn()
	current.lastParse = time.Time{}
	current.handle(uiEvent{"delta", "\n\n" + strings.Repeat("More text written while the reader is at the top. ", 30)})
	ui.followTail = true
	ui.relayoutColumn()
	checkTranscript(t, ui, "back at the end")
}

// A streamed answer parsed piece by piece, from its last blocks only, ends
// with the blocks a whole parse gives, at every step.
func TestMarkdownIncrementalParse(t *testing.T) {
	answers := []string{
		"Intro paragraph.\n\n- one\n- two\n\n  continued item\n\n```sh\nmake\n\n```\n\nThe end, $x$ written.\n\n# Heading\nText\n===\n\n1. first\n2. second\n\n> quote\nlazy line\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n    indented code\n\nlast",
		strings.Repeat("A paragraph of words that goes on.\n\n", 6) + "$$\nx^2\n$$\n\n---\n\n* star\n\n  * nested\n\ndone",
	}
	for _, answer := range answers {
		view := newMarkdownView("")
		for n := 1; n <= len(answer); n++ {
			view.ParseMarkdown(answer[:n])
			_, want := splitMarkdown([]byte(answer[:n]))
			if len(want) != len(view.blocks) {
				t.Fatalf("after %q: %d blocks, a whole parse gives %d", answer[:n], len(view.blocks), len(want))
			}
			for i := range want {
				if want[i] != view.blocks[i] {
					t.Fatalf("after %q: block %d is %+v, a whole parse gives %+v", answer[:n], i, view.blocks[i], want[i])
				}
			}
			if len(view.objs) != len(view.blocks) || len(view.box.Objects) != len(view.blocks) {
				t.Fatalf("after %q: %d widgets for %d blocks", answer[:n], len(view.box.Objects), len(view.blocks))
			}
		}
	}
}

// #497: after a chat is loaded or the window resized, no row keeps a height
// other than the one it needs at the column's width, shown or not, and every
// row sits exactly under the one before it.
func TestTranscriptRowsKeepTheirHeight(t *testing.T) {
	ui, chat := perfWindow(t, 150)
	check := func(when string) {
		t.Helper()
		for range 200 {
			ui.remeasure()
		}
		ui.relayoutColumn()
		layout := ui.items
		y := float32(0)
		for i, row := range layout.rows {
			if abs32(layout.tops[i]-y) > 0.5 {
				t.Fatalf("%s: row %d top %.1f, expected %.1f", when, i, layout.tops[i], y)
			}
			if need := measureRow(row.object, layout.width).Height; abs32(need-row.size.Height) > 0.5 {
				t.Fatalf("%s: row %d (%T) kept %.1f, needs %.1f at %.0f", when, i, row.object, row.size.Height, need, layout.width)
			}
			y += row.size.Height + transcriptGap
		}
	}
	check("loaded")
	for _, width := range []float32{700, 1400, 1000} {
		ui.window.Resize(fyne.NewSize(width, 760))
		check(fmt.Sprintf("resized to %.0f", width))
	}
	// Opened again at another window width.
	other := &conversation{ID: "short", Title: "Short", Items: perfItems(4)}
	ui.chats = append(ui.chats, other)
	ui.openLocalChat(other)
	for ui.loadingChat != nil {
		ui.advanceChatLoad()
	}
	ui.window.Resize(fyne.NewSize(800, 760))
	ui.cachedChat = nil
	ui.openLocalChat(chat)
	for ui.loadingChat != nil {
		ui.advanceChatLoad()
	}
	check("reopened narrower")
}

// flowTexts lists the paragraphs inside an item.
func flowTexts(object fyne.CanvasObject, into *[]*flowText) {
	switch object := object.(type) {
	case *flowText:
		*into = append(*into, object)
	case *fyne.Container:
		for _, child := range object.Objects {
			flowTexts(child, into)
		}
	}
}

// #497: a row scrolled out of sight and back draws its text again, and keeps
// its place.
func TestTranscriptRowsDrawAfterScrollingBack(t *testing.T) {
	ui, _ := perfWindow(t, 200)
	ui.followTail = false
	bottom := ui.scroll.Content.Size().Height - ui.scroll.Size().Height
	for _, at := range []float32{0, 1, 0.5, 0, 1, 0.25} {
		ui.scroll.ScrollToOffset(fyne.NewPos(0, at*bottom))
		ui.relayoutColumn()
		checkTranscript(t, ui, fmt.Sprintf("scrolled to %.0f%%", at*100))
		for i, object := range ui.transcript.Objects {
			if !object.Visible() {
				continue
			}
			var texts []*flowText
			flowTexts(object, &texts)
			for _, text := range texts {
				if text.height > 0 && len(text.objects) == 0 {
					t.Fatalf("scrolled to %.0f%%: row %d is shown with %.0f px of text and nothing drawn", at*100, i, text.height)
				}
			}
		}
	}
}

// #473/#497: when a row's height changes, every row after it moves by
// exactly the difference, shown or not.
func TestTranscriptRowsFollowAChangedHeight(t *testing.T) {
	ui, _ := perfWindow(t, 120)
	layout := ui.items
	for _, at := range []int{3, 40, 119} {
		before := append([]float32(nil), layout.tops...)
		row := &layout.rows[at]
		const grow = 37
		row.size.Height += grow
		layout.dirty = min(layout.dirty, at+1)
		ui.relayoutColumn()
		for i := range layout.rows {
			want := before[i]
			if i > at {
				want += grow
			}
			if abs32(layout.tops[i]-want) > 0.5 || abs32(layout.rows[i].object.Position().Y-want) > 0.5 && !layout.rows[i].object.Visible() {
				t.Fatalf("row %d after changing row %d: top %.1f, at %.1f, expected %.1f", i, at, layout.tops[i], layout.rows[i].object.Position().Y, want)
			}
		}
		// Measured again, it goes back, and so do the rows after it.
		layout.forget(row.object)
		ui.relayoutColumn()
		for i := range layout.rows {
			if abs32(layout.tops[i]-before[i]) > 0.5 {
				t.Fatalf("row %d did not come back after row %d did: %.1f, expected %.1f", i, at, layout.tops[i], before[i])
			}
		}
	}
}

// #440: an answer that streams while it is scrolled out of view, far above
// the bottom of the chat, keeps the rows around it in their places, and is
// drawn right when it is scrolled back to. Nothing in a hidden row's
// height is measured again until its text changes (changed) or it is shown.
func TestTranscriptStreamsWhileScrolledAway(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui := &desktopUI{}
	ui.items = &transcriptLayout{ui: ui}
	ui.items.reset()
	ui.transcript = container.New(ui.items)
	ui.transcript.Resize(fyne.NewSize(712, 4000))
	scroll := &kineticScroll{}
	scroll.Content = ui.transcript
	scroll.Resize(fyne.NewSize(712, 200))
	ui.scroll = scroll

	const rows, answerAt = 60, 5
	var answer *markdownView
	for i := range rows {
		if i == answerAt {
			answer = newMarkdownView("")
			ui.transcript.Objects = append(ui.transcript.Objects, answer.box)
			continue
		}
		row := canvas.NewRectangle(colText)
		row.SetMinSize(fyne.NewSize(712, 30))
		ui.transcript.Objects = append(ui.transcript.Objects, row)
	}
	scroll.Offset.Y = 1e6 // the bottom: the answer, near the top, is out of the band
	relayout(ui.transcript)
	if answer.box.Visible() {
		t.Fatal("the answer is shown at the bottom of the chat: the test needs it out of view")
	}

	check := func(when string) {
		t.Helper()
		y := float32(0)
		for i, object := range ui.transcript.Objects {
			if abs32(object.Position().Y-y) > 0.5 {
				t.Fatalf("%s: row %d at %.1f, expected at %.1f", when, i, object.Position().Y, y)
			}
			// The layout's height for a hidden row must be what its content
			// needs now: a stale one is the bug.
			if need := object.MinSize().Height; abs32(need-ui.items.heightOf(i)) > 0.5 {
				t.Fatalf("%s: row %d is laid out %.1f high, its content needs %.1f", when, i, ui.items.heightOf(i), need)
			}
			y += ui.items.heightOf(i) + transcriptGap
		}
	}

	long := strings.Repeat("words that wrap across the column ", 6)
	full := "Here is the plan:\n\n- " + long + "\n- " + long + "\n  - " + long + "\n\nAnd " + long
	text := ""
	for _, word := range strings.SplitAfter(full, " ") {
		text += word
		answer.ParseMarkdown(text)
		ui.items.changed(answer.box)
		relayout(ui.transcript)
		check(fmt.Sprintf("while hidden, after %q", text[max(0, len(text)-30):]))
	}

	scroll.Offset.Y = 0 // back to the top: the answer comes into view
	relayout(ui.transcript)
	if !answer.box.Visible() {
		t.Fatal("the answer did not come back into view")
	}
	check("scrolled back")
	if found := misplaced(ui.transcript, "transcript"); len(found) > 0 {
		t.Fatal(strings.Join(found, "\n"))
	}
}
