// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"

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
