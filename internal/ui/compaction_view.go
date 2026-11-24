// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

// compactionNote says a note of the conversation tells a compaction of
// 1loop's, which can be compared: Claude Code's own has no numbers.
func compactionNote(item agent.ChatItem) bool {
	return item.Kind == "note" && strings.HasPrefix(item.Text, "Context compacted") && strings.Contains(item.Text, " → ~")
}

// compactionRecords are a chat's last compactions, this computer's or its
// server's.
func (ui *desktopUI) compactionRecords(chat *conversation) ([]agent.CompactionRecord, error) {
	if chat.server == nil {
		return chat.Compaction.List(), nil
	}
	ctx, cancel := context.WithTimeout(ui.ctx, 15*time.Second)
	defer cancel()
	var reply struct{ Records []agent.CompactionRecord }
	err := chat.server.client.json(ctx, "GET", "/api/chats/"+chat.serverID+"/compactions", nil, &reply)
	return reply.Records, err
}

// compareCompaction shows the context before and after the compaction a note
// tells (#128).
func (ui *desktopUI) compareCompaction(chat *conversation, note string) {
	go func() {
		records, err := ui.compactionRecords(chat)
		fyne.Do(func() {
			if err != nil {
				ui.notice("Compaction", err.Error())
				return
			}
			record := agent.FindCompaction(records, note)
			if record == nil {
				ui.notice("Compaction", "This compaction is no longer kept: a chat keeps its last three.")
				return
			}
			ui.showCompaction(*record)
		})
	}()
}

func (ui *desktopUI) showCompaction(record agent.CompactionRecord) {
	size := ui.window.Canvas().Size()
	width, height := fyne.Min(1080, size.Width-80), fyne.Max(240, size.Height-240)
	side := func(title string, tokens, count int, messages []agent.CompactedMessage) fyne.CanvasObject {
		heading := textLine(title, 14, colText)
		heading.TextStyle.Bold = true
		total := textLine(fmt.Sprintf("~%s tokens  ·  %d messages", agent.Tokens(tokens), count), 12, colMuted)
		list := newKineticVScroll(padded(10, 10, 12, 12, compactionBlocks(messages)))
		return container.NewBorder(padded(0, 8, 2, 2, container.NewBorder(nil, nil, heading, total)), nil, nil, nil, surface(list, colCode, colBorder, 10, 0))
	}
	saved := 0
	if record.Before > 0 {
		saved = 100 - record.After*100/record.Before
	}
	intro := note(fmt.Sprintf("%s  ·  %d%% freed. Before, each message is abbreviated; after, the summary is whole. Everything stays in the conversation and can be recalled.", record.At.Format("2 January 15:04"), saved))
	columns := container.New(layout.NewGridLayoutWithColumns(2), side("Before", record.Before, record.Count, record.Was), side("After", record.After, len(record.Now), record.Now))
	body := container.NewBorder(padded(0, 10, 0, 0, intro), nil, nil, nil, container.NewGridWrap(fyne.NewSize(width-40, height), columns))
	ui.modal("Context compaction", body, width, nil)
}

// compactionBlocks is a context, message by message: who, how much, what.
// The summary stands out.
func compactionBlocks(messages []agent.CompactedMessage) fyne.CanvasObject {
	box := container.New(layout.NewCustomPaddedVBoxLayout(10))
	for _, message := range messages {
		who := map[string]string{"user": "You", "assistant": "Assistant", "tool": "Tool result", "1loop": "Summary and notes"}[message.Role]
		if who == "" {
			who = message.Role
		}
		ink := colMuted
		if message.Role == "1loop" {
			ink = colAmber
		}
		if message.Tokens > 0 {
			who += "  ·  ~" + agent.Tokens(message.Tokens)
		}
		text := newFlowText(plainSpans(message.Text), 12.5, colText)
		text.tail, text.tight = "", true
		box.Add(container.New(layout.NewCustomPaddedVBoxLayout(2), textLine(who, 11.5, ink), text))
	}
	return box
}
