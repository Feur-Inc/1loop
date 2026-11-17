// SPDX-License-Identifier: MPL-2.0

package ui

// Headless benchmarks of the interface's hot paths, on Fyne's test driver
// (no GPU): the numbers compare runs of the same machine, before and after
// a change (docs/perf/2026-11-audit.md). The real window is measured by
// ONELOOP_BENCH (bench.go) and scripts/perf/.
//
//	go test ./internal/ui -run '^$' -bench Perf -benchmem

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/agent"
)

// perfItems is a long chat like scripts/perf/genstate's, without pictures.
func perfItems(n int) []agent.ChatItem {
	answer := strings.Repeat("The transcript layout measures each item at the column width. ", 6) +
		"\n\n- **bold** and `code`\n- [a link](https://example.com)\n\n```go\nfunc f() {\n\treturn 42\n}\n```\n\nThe end."
	items := make([]agent.ChatItem, 0, n)
	for i := 0; len(items) < n; i++ {
		items = append(items, agent.ChatItem{Kind: "user", Text: fmt.Sprintf("Question %d about the layout?", i)})
		items = append(items, agent.ChatItem{Kind: "tool", Tool: "shell", Args: `{"command":"go test ./..."}`, Text: strings.Repeat("ok\n", 10), Millis: 1200})
		items = append(items, agent.ChatItem{Kind: "tool", Tool: "read_file", Args: `{"path":"internal/ui/transcript.go"}`, Text: "package ui", Millis: 3})
		items = append(items, agent.ChatItem{Kind: "assistant", Text: answer})
	}
	return items[:n]
}

// perfWindow is a window of 1180 × 760 on the test driver showing a chat
// of n items, loaded to the end.
func perfWindow(b testing.TB, n int) (*desktopUI, *conversation) {
	b.Helper()
	test.NewTempApp(b).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui, _ := twoWindows(&testing.T{})
	ui.windows = ui.windows[:1]
	ui.window = test.NewWindow(nil)
	ui.window.SetContent(ui.build())
	ui.window.Resize(fyne.NewSize(1180, 760))
	chat := &conversation{ID: "long", Title: "Long", Items: perfItems(n)}
	ui.chats = []*conversation{chat}
	ui.openLocalChat(chat)
	for ui.loadingChat != nil {
		ui.advanceChatLoad()
	}
	return ui, chat
}

func BenchmarkPerfOpenChat(b *testing.B) {
	for _, n := range []int{500, 4000} {
		for _, cached := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/cached=%v", n, cached), func(b *testing.B) {
				ui, chat := perfWindow(b, n)
				other := &conversation{ID: "short", Title: "Short", Items: perfItems(4)}
				ui.chats = append(ui.chats, other)
				b.ResetTimer()
				for range b.N {
					ui.openLocalChat(other)
					for ui.loadingChat != nil {
						ui.advanceChatLoad()
					}
					if !cached {
						ui.cachedChat = nil // the cost of a first open
					}
					ui.openLocalChat(chat)
					for ui.loadingChat != nil {
						ui.advanceChatLoad()
					}
				}
			})
		}
	}
}

// One frame's layout of the transcript, as each redraw does it.
func BenchmarkPerfTranscriptLayout(b *testing.B) {
	for _, n := range []int{500, 4000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			ui, _ := perfWindow(b, n)
			size := ui.transcript.Size()
			b.ResetTimer()
			for range b.N {
				ui.items.MinSize(ui.transcript.Objects)
				ui.items.Layout(ui.transcript.Objects, size)
			}
		})
	}
}

// A wheel step of 40 px in the middle of the chat, with the frame's layout.
func BenchmarkPerfScrollStep(b *testing.B) {
	ui, _ := perfWindow(b, 4000)
	ui.scroll.ScrollToOffset(fyne.NewPos(0, ui.scroll.Content.Size().Height/2))
	step := float32(-40)
	b.ResetTimer()
	for i := range b.N {
		if i%200 == 0 {
			step = -step
		}
		ui.scroll.Scroll.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: step}})
		ui.items.Layout(ui.transcript.Objects, ui.transcript.Size())
	}
}

// A streamed delta at the end of a long chat, parsed at once.
func BenchmarkPerfStreamDelta(b *testing.B) {
	for _, n := range []int{50, 4000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			ui, chat := perfWindow(b, n)
			current := ui.newTurn(chat, chat.Config)
			current.handle(uiEvent{"delta", "Start of the answer. "})
			b.ResetTimer()
			for i := range b.N {
				if i%400 == 399 { // answers of ~2,000 words, as a model writes them
					current.handle(uiEvent{"message_end", nil})
				}
				// Due at once: a zero lastParse parses now instead of
				// scheduling it on a timer, whose fyne.Do the test driver
				// would run on another goroutine.
				current.lastParse = time.Time{}
				current.handle(uiEvent{"delta", "a few more words "})
			}
		})
	}
}

func BenchmarkPerfMarkdownParse(b *testing.B) {
	text := strings.Repeat(perfItems(4)[3].Text+"\n\n", 20)
	view := newMarkdownView("")
	b.ResetTimer()
	for range b.N {
		view.ParseMarkdown(text)
	}
}

// The sidebar made again, as at each chat switch, rename or theme change.
func BenchmarkPerfRenderChats(b *testing.B) {
	ui, _ := perfWindow(b, 4)
	var chats []*conversation
	for i := range 200 {
		chat := &conversation{ID: fmt.Sprint("c", i), Title: fmt.Sprint("Chat ", i), Items: perfItems(6),
			Config: agent.Config{Workspace: fmt.Sprintf("/tmp/project-%02d", i%30)}}
		if i%3 == 0 && i > 0 {
			chat.Subchat = &agent.SubchatLink{Parent: chats[i-1].ID}
		}
		chats = append(chats, chat)
	}
	ui.chats = append(ui.chats, chats...)
	for i := range 30 {
		ui.projects = append(ui.projects, fmt.Sprintf("/tmp/project-%02d", i))
	}
	b.ResetTimer()
	for range b.N {
		ui.renderChats()
	}
}

// A theme saved again (watchThemes): palette, radii, backdrop, sidebar.
func BenchmarkPerfThemeChanged(b *testing.B) {
	ui, _ := perfWindow(b, 500)
	b.ResetTimer()
	for range b.N {
		ui.themeChanged()
	}
}
