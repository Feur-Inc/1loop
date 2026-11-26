// SPDX-License-Identifier: MPL-2.0

package ui

// The menu a right click on the conversation opens while text is selected,
// as a browser's: Copy, Copy as Markdown, Quote in reply, Select all.
//
// Copy as Markdown gives back an answer's source for each of its blocks
// (a paragraph, a list, code, a table) the selection covers whole; where it
// covers part of one, the selected text with its inline styles written
// again (bold, italics, code, links), and a part of a code block in its
// fence. Other messages (the user's, errors) are plain text: that is their
// source.

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// selectionMenu shows the menu at a point of the window, when text is
// selected.
func (ui *desktopUI) selectionMenu(at fyne.Position) {
	if ui.selected == nil || ui.selected.selectedText() == "" {
		return
	}
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("Copy", func() { ui.copySelection() }),
		fyne.NewMenuItem("Copy as Markdown", func() {
			if ui.selected != nil {
				if text := ui.selected.selectedMarkdown(); text != "" {
					fyne.CurrentApp().Clipboard().SetContent(text)
				}
			}
		}),
		fyne.NewMenuItem("Quote in reply", func() {
			if ui.selected != nil {
				ui.quoteInReply(ui.selected.selectedText())
			}
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Select all", ui.chatSel.selectAll),
	)
	widget.NewPopUpMenu(menu, ui.window.Canvas()).ShowAtPosition(at)
}

func (chatSel *chatSelection) menu(at fyne.Position) { chatSel.ui.selectionMenu(at) }

// quoteInReply adds text to the message being written as a quote ("> "),
// on lines of its own, and puts the caret after it.
func (ui *desktopUI) quoteInReply(text string) {
	if ui.prompt == nil || strings.TrimSpace(text) == "" {
		return
	}
	draft := ui.prompt.Text
	switch {
	case draft == "":
	case strings.HasSuffix(draft, "\n\n"):
	case strings.HasSuffix(draft, "\n"):
		draft += "\n"
	default:
		draft += "\n\n"
	}
	draft += quoted(text) + "\n\n"
	ui.prompt.SetText(draft)
	lines := strings.Split(draft, "\n")
	ui.prompt.CursorRow, ui.prompt.CursorColumn = len(lines)-1, 0
	ui.prompt.Refresh()
	ui.window.Canvas().Focus(ui.prompt)
}

// quoted is text as a Markdown quote.
func quoted(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

func (chatSel *chatSelection) selectedMarkdown() string {
	var parts []string
	for _, layer := range chatSel.marked {
		if text := layer.selectedMarkdown(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// selectedMarkdown is the selection as Markdown: see the top of the file.
func (selection *selectLayer) selectedMarkdown() string {
	plain := selection.selectedText()
	view := selection.markdownView()
	if plain == "" || view == nil {
		return plain
	}
	start, end := selection.span()
	from, to := selection.text.at(start, true), selection.text.at(end, false)
	var parts []string
	for first := 0; first < len(selection.pieces); {
		block := selection.pieces[first].block
		last := first
		for last+1 < len(selection.pieces) && selection.pieces[last+1].block == block {
			last++
		}
		blockFrom, blockTo := selection.text.starts[first], selection.text.ends[last]
		lo, hi := max(from, blockFrom), min(to, blockTo)
		if lo < hi && block < len(view.blocks) {
			source := view.blocks[block]
			switch {
			case lo == blockFrom && hi == blockTo: // the whole block: its source
				parts = append(parts, strings.Trim(source.text, "\n"))
			case source.kind == "code":
				fence := "```"
				for strings.Contains(source.text, fence+"\n") && len(fence) < 8 {
					fence += "`"
				}
				parts = append(parts, fence+source.lang+"\n"+string(selection.text.runes[lo:hi])+"\n"+fence)
			default:
				parts = append(parts, selection.inlineMarkdown(first, last, lo, hi))
			}
		}
		first = last + 1
	}
	return strings.Join(parts, "\n\n")
}

// markdownView is the answer the layer lies over, nil for other messages.
func (selection *selectLayer) markdownView() *markdownView {
	for _, view := range selection.ui.views {
		if view, ok := view.(*markdownView); ok && view.layer == selection {
			return view
		}
	}
	return nil
}

// inlineMarkdown writes [lo, hi) of the text of pieces first to last with
// their inline styles: a style opens where its span starts being selected
// and closes where it stops.
func (selection *selectLayer) inlineMarkdown(first, last, lo, hi int) string {
	var builder strings.Builder
	var open *span
	closing := ""
	for i := first; i <= last; i++ {
		piece := selection.pieces[i]
		a, z := max(lo, selection.text.starts[i]), min(hi, selection.text.ends[i])
		if a > z || (a == z && len(piece.runes) > 0) {
			continue
		}
		if piece.span != open {
			builder.WriteString(closing)
			open, closing = piece.span, ""
			if open != nil {
				var opening string
				opening, closing = markdownMarks(open, string(selection.text.runes[a:z]))
				builder.WriteString(opening)
			}
		}
		if piece.mark { // a list item's bullet or number
			builder.WriteString(markdownMarker(string(piece.runes)))
		} else {
			builder.WriteString(string(selection.text.runes[a:z]))
		}
		// What separates it from the next piece, when the selection goes on.
		if next := selection.text.ends[i]; z == next && i < last && selection.text.starts[i+1] < hi {
			after := string(selection.text.runes[next:selection.text.starts[i+1]])
			if strings.Contains(after, "\n") || strings.Contains(after, "\t") {
				builder.WriteString(closing)
				open, closing = nil, ""
			}
			builder.WriteString(strings.ReplaceAll(after, "\t", " | "))
		}
	}
	builder.WriteString(closing)
	return builder.String()
}

// markdownMarks are what open and close a span's styles in Markdown.
func markdownMarks(s *span, text string) (string, string) {
	open, close := "", ""
	wrap := func(before, after string) {
		open, close = open+before, after+close
	}
	if s.math != "" {
		return "$", "$"
	}
	if target := s.target(); target != "" && target != text && !s.cite && !strings.HasPrefix(target, refScheme) {
		wrap("[", "]("+target+")")
	}
	if s.strike {
		wrap("~~", "~~")
	}
	switch {
	case s.bold && s.italic:
		wrap("***", "***")
	case s.bold:
		wrap("**", "**")
	case s.italic:
		wrap("*", "*")
	}
	if s.code || s.cite {
		ticks := "`"
		for strings.Contains(s.text, ticks) {
			ticks += "`"
		}
		wrap(ticks, ticks)
	}
	return open, close
}

// markdownMarker is a list item's bullet or number as Markdown writes it.
func markdownMarker(marker string) string {
	trimmed := strings.TrimSpace(marker)
	if trimmed != "" && strings.ContainsRune("0123456789", rune(trimmed[0])) {
		return trimmed + " "
	}
	return "- "
}
