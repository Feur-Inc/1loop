// SPDX-License-Identifier: MPL-2.0

package ui

// "@" mentions in the composer: typing "@" at the start of a word offers the
// tools and MCP servers the chat can name (agent.Mentions), in the slash
// commands' menu (slash_commands.go). The model is told about them when the
// message is sent (agent's mentionNote).

import (
	"net/url"
	"strings"
	"unicode"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

// mentionTyped is the mention being typed at the cursor: what follows an
// "@" that starts a word, and the "@"'s place in the text (in runes).
func (ui *desktopUI) mentionTyped() (string, int, bool) {
	if ui.editing != nil {
		return "", 0, false
	}
	runes := []rune(ui.prompt.Text)
	cursor := 0
	for row, line := range strings.Split(ui.prompt.Text, "\n") {
		if row == ui.prompt.CursorRow {
			cursor += min(ui.prompt.CursorColumn, len([]rune(line)))
			break
		}
		cursor += len([]rune(line)) + 1
	}
	cursor = min(cursor, len(runes))
	start := cursor
	for start > 0 && mentionRune(runes[start-1]) {
		start--
	}
	if start == 0 || runes[start-1] != '@' {
		return "", 0, false
	}
	at := start - 1
	if at > 0 && !unicode.IsSpace(runes[at-1]) && !strings.ContainsRune("([{\"'", runes[at-1]) {
		return "", 0, false // an e-mail address, not a mention
	}
	return string(runes[start:cursor]), at, true
}

func mentionRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// updateMentionMenu shows the mentions matching what is typed after "@".
func (ui *desktopUI) updateMentionMenu() {
	typed, at, ok := ui.mentionTyped()
	if !ok || ui.prompt.Text == ui.commandDismissed {
		ui.hideCommandMenu()
		return
	}
	ui.commandDismissed = ""
	if !ui.mentioning {
		ui.mentionOptions = nil // asked again each time the menu opens
		for _, mention := range ui.chatMentions() {
			ui.mentionOptions = append(ui.mentionOptions, commandOption{name: mention.Name, about: mention.About})
		}
	}
	matches := matchCommands(ui.mentionOptions, typed)
	if len(matches) == 0 && !ui.mentionsLoading {
		ui.hideCommandMenu()
		return
	}
	if !ui.mentioning || typed != ui.commandFilter || at != ui.mentionAt {
		ui.commandPick = 0
	}
	ui.mentioning, ui.mentionAt = true, at
	ui.commandFilter, ui.commandMatches = typed, matches
	ui.commandPick = max(0, min(ui.commandPick, len(matches)-1))
	ui.renderCommandMenu()
}

// chatMentions are the open chat's mentions: from the server for a remote
// chat, which answers later.
func (ui *desktopUI) chatMentions() []agent.Mention {
	if ui.server != nil {
		return ui.server.mentions()
	}
	return ui.mentionsFor(ui.current)
}

// mentionsFor are a local chat's mentions (the new chat's when chat is
// nil), with the tools a run of it gets.
func (ui *desktopUI) mentionsFor(chat *conversation) []agent.Mention {
	ui = ui.viewOf(chat) // the window showing it (windows.go)
	cfg := ui.cfg
	if chat != nil && ui.current != chat {
		cfg = chat.Config // the composer's model is the open chat's
	}
	cfg = ui.resolveModel(cfg)
	cfg.Browser, cfg.Terminal, cfg.Computer = ui.browserTools(chat), ui.terminalTools(chat), ui.computerTools(chat)
	return agent.Mentions(cfg)
}

// completeMention puts the chosen mention in place of the one being typed.
func (ui *desktopUI) completeMention() {
	if ui.commandPick >= len(ui.commandMatches) {
		return
	}
	runes := []rune(ui.prompt.Text)
	end := min(ui.mentionAt+1, len(runes))
	for end < len(runes) && mentionRune(runes[end]) {
		end++
	}
	inserted := []rune("@" + ui.commandMatches[ui.commandPick].name + " ")
	if end < len(runes) && runes[end] == ' ' {
		end++ // the space after it is already there
	}
	text := string(runes[:ui.mentionAt]) + string(inserted) + string(runes[end:])
	cursor := ui.mentionAt + len(inserted)
	ui.prompt.SetText(text)
	row, column := 0, cursor
	for _, line := range strings.Split(text, "\n") {
		if column <= len([]rune(line)) {
			break
		}
		column -= len([]rune(line)) + 1
		row++
	}
	ui.prompt.CursorRow, ui.prompt.CursorColumn = row, column
	ui.prompt.Refresh()
	ui.hideCommandMenu()
	ui.window.Canvas().Focus(ui.prompt)
}

// mentions are a remote chat's mentions, asked of the server once per chat;
// the menu is shown again when they come.
func (remote *remoteDesktop) mentions() []agent.Mention {
	if remote.mentionsChat == remote.current && remote.mentionList != nil {
		return remote.mentionList
	}
	ui := remote.parent
	if ui.mentionsLoading {
		return nil
	}
	ui.mentionsLoading = true
	chat := remote.current
	go func() {
		var reply struct{ Mentions []agent.Mention }
		err := remote.client.json(remote.ctx, "GET", "/api/mentions?chat="+url.QueryEscape(chat), nil, &reply)
		fyne.Do(func() {
			ui.mentionsLoading = false
			if err == nil {
				remote.mentionsChat, remote.mentionList = chat, append([]agent.Mention{}, reply.Mentions...)
			}
			if ui.mentioning {
				ui.mentioning = false // takes the new list
				ui.updateCommandMenu()
			}
		})
	}()
	return nil
}
