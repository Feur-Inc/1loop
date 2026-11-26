// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/agent"
	"1loop/internal/core"
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

// #473: a paragraph written word by word (grow) is laid out and drawn as a
// paragraph made with its whole text at once: the same lines, runs, pieces
// and links, at every step.
func TestFlowTextGrowsLikeANewOne(t *testing.T) {
	test.NewTempApp(t)
	answer := "Intro.\n\n" + strings.Repeat("Words that wrap, a `code bit` and **bold ones**, then [a link](https://example.com) and averyveryverylongwordthatwillnotfitonalineofitsownatallreally more. ", 6) +
		"\n\n- a list\n- of two\n\nThe end, written  slowly."
	const width = 420
	view := newMarkdownView("")
	view.box.Resize(fyne.NewSize(width, 10))
	kept := 0
	for n := 1; n <= len(answer); n += 1 + n%5 {
		var before []*flowText
		flowTexts(view.box, &before)
		view.ParseMarkdown(answer[:n])
		if last := len(before) - 1; last >= 0 && len(before[last].marks) > 2 && len(view.objs) == len(view.blocks) {
			var after []*flowText
			flowTexts(view.box, &after)
			if after[last] == before[last] && len(after[last].objects) > 0 && len(before[last].objects) > 0 && after[last].objects[0] == before[last].objects[0] {
				kept++ // the same paragraph, its first line not drawn again
			}
		}
		whole := newMarkdownView(answer[:n])
		whole.box.Resize(fyne.NewSize(width, 10))
		var grown, made []*flowText
		flowTexts(view.box, &grown)
		flowTexts(whole.box, &made)
		if len(grown) != len(made) {
			t.Fatalf("after %d bytes: %d paragraphs, a whole parse makes %d", n, len(grown), len(made))
		}
		for i := range made {
			if err := sameFlow(grown[i], made[i]); err != "" {
				t.Fatalf("after %d bytes (%q), paragraph %d: %s", n, answer[max(0, n-20):n], i, err)
			}
		}
	}
	if kept < 50 {
		t.Fatalf("the growing paragraph kept its first lines %d times only: it is built again", kept)
	}
}

// sameFlow tells how two laid-out paragraphs differ, if they do.
func sameFlow(a, b *flowText) string {
	if a.width != b.width || a.height != b.height || a.natural != b.natural || a.widest != b.widest {
		return fmt.Sprintf("width %.1f height %.1f natural %.1f widest %.1f, expected %.1f %.1f %.1f %.1f",
			a.width, a.height, a.natural, a.widest, b.width, b.height, b.natural, b.widest)
	}
	if len(a.objects) != len(b.objects) || len(a.pieces) != len(b.pieces) || len(a.links) != len(b.links) {
		return fmt.Sprintf("%d objects, %d pieces, %d links, expected %d, %d, %d",
			len(a.objects), len(a.pieces), len(a.links), len(b.objects), len(b.pieces), len(b.links))
	}
	for i := range a.objects {
		x, y := a.objects[i], b.objects[i]
		if fmt.Sprintf("%T", x) != fmt.Sprintf("%T", y) || x.Position() != y.Position() || x.Size() != y.Size() {
			return fmt.Sprintf("object %d: %T at %v %v, expected %T at %v %v", i, x, x.Position(), x.Size(), y, y.Position(), y.Size())
		}
		if text, ok := x.(*canvas.Text); ok && text.Text != y.(*canvas.Text).Text {
			return fmt.Sprintf("object %d draws %q, expected %q", i, text.Text, y.(*canvas.Text).Text)
		}
	}
	for i := range a.pieces {
		x, y := a.pieces[i], b.pieces[i]
		if string(x.runes) != string(y.runes) || x.at != y.at || x.size != y.size || x.after != y.after {
			return fmt.Sprintf("piece %d: %q at %v after %q, expected %q at %v after %q", i, string(x.runes), x.at, x.after, string(y.runes), y.at, y.after)
		}
	}
	for i := range a.links {
		if a.links[i] != b.links[i] {
			return fmt.Sprintf("link %d: %+v, expected %+v", i, a.links[i], b.links[i])
		}
	}
	return ""
}

// sameItem compares every field of a chat item: one added to ChatItem must
// be compared there too, or a changed item would show as it was.
func TestSameItemComparesEveryField(t *testing.T) {
	if n := reflect.TypeOf(agent.ChatItem{}).NumField(); n != 12 {
		t.Fatalf("agent.ChatItem has %d fields, sameItem (chat_loading.go) compares 12: compare the new ones", n)
	}
	item := agent.ChatItem{ID: "a", Kind: "user", Text: "hi", Images: []core.ImagePart{{Mime: "image/png", Data: "x"}},
		From: &agent.ChatSender{ID: "c", Title: "C"}}
	other := snapshotChatItem(item)
	other.From = &agent.ChatSender{ID: "c", Title: "C"}
	if !sameItem(item, other) {
		t.Fatal("a copy of an item is not the same item")
	}
	for _, change := range []func(*agent.ChatItem){
		func(i *agent.ChatItem) { i.Text += "!" },
		func(i *agent.ChatItem) { i.Images[0].Data = "y" },
		func(i *agent.ChatItem) { i.From = &agent.ChatSender{ID: "c", Title: "C", Hops: 1} },
		func(i *agent.ChatItem) { i.Millis = 3 },
	} {
		changed := snapshotChatItem(item)
		change(&changed)
		if sameItem(item, changed) {
			t.Fatalf("a changed item is the same: %+v", changed)
		}
	}
}

// #440/#497: a row out of sight is measured there at another width (the
// window resized), then changes while still out of sight. Every row after
// it moves by exactly the difference in its height, the rows before it do
// not move, and once it is shown at the first width again, its slot is the
// height it needs and the rows around it touch it: no overlap, no gap.
func TestTranscriptRowChangedWhileHiddenAtAnotherWidth(t *testing.T) {
	ui, chat := perfWindow(t, 200)
	layout := ui.items
	ui.followTail = false
	ui.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	ui.relayoutColumn()
	at := -1 // an answer far below the view
	for i := 160; i < len(chat.Items) && at < 0; i++ {
		if chat.Items[i].Kind == "assistant" {
			at = i
		}
	}
	view := ui.views[at].(*markdownView)
	row := layout.owner[view.box]
	r := layout.index[row]
	wide := layout.width

	ui.window.Resize(fyne.NewSize(820, 760))
	for range 200 {
		ui.remeasure()
	}
	ui.relayoutColumn()
	if row.Visible() || layout.rows[r].width != layout.width || layout.width == wide {
		t.Fatalf("the row should be out of sight, measured at the new width %.0f (it is at %.0f, visible %v)", layout.width, layout.rows[r].width, row.Visible())
	}
	before := append([]float32(nil), layout.tops...)
	height := layout.rows[r].size.Height

	view.ParseMarkdown(chat.Items[at].Text + "\n\n" + strings.Repeat("Another paragraph, written while out of sight. ", 20))
	layout.changed(view.box)
	ui.relayoutColumn()
	grow := layout.rows[r].size.Height - height
	if grow <= 0 {
		t.Fatalf("the row did not grow: %.1f, then %.1f", height, layout.rows[r].size.Height)
	}
	if need := measureRow(row, layout.width).Height; abs32(need-layout.rows[r].size.Height) > 0.5 {
		t.Fatalf("the row kept %.1f, it needs %.1f", layout.rows[r].size.Height, need)
	}
	for i := range layout.rows {
		want := before[i]
		if i > r {
			want += grow
		}
		object := layout.rows[i].object
		if abs32(layout.tops[i]-want) > 0.5 || !object.Visible() && abs32(object.Position().Y-want) > 0.5 {
			t.Fatalf("row %d after row %d grew by %.1f: top %.1f, at %.1f, expected %.1f", i, r, grow, layout.tops[i], object.Position().Y, want)
		}
	}

	ui.window.Resize(fyne.NewSize(1180, 760))
	for range 200 {
		ui.remeasure()
	}
	origin, _ := offsetIn(ui.scroll.Content, ui.transcript)
	ui.scroll.ScrollToOffset(fyne.NewPos(0, origin.Y+layout.tops[r]-100))
	ui.relayoutColumn()
	if !row.Visible() || layout.width != wide {
		t.Fatalf("the row should be shown at %.0f (visible %v, width %.0f)", wide, row.Visible(), layout.width)
	}
	checkTranscript(t, ui, "shown again at the first width")
	var texts []*flowText
	flowTexts(row, &texts)
	for _, text := range texts {
		if text.height > 0 && len(text.objects) == 0 {
			t.Fatalf("shown again, a paragraph of %.0f px draws nothing", text.height)
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
