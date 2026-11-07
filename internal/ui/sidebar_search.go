// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

var (
	iconSearch    = lineIcon("search", `<circle cx="7" cy="7" r="4.3"/><path d="M10.2 10.2 13.5 13.5"/>`)
	iconFolderAdd = lineIcon("folder-add", `<path d="M2.5 5c0-.8.6-1.5 1.4-1.5h2.7l1.3 1.4h4.2c.8 0 1.4.6 1.4 1.4v4.9c0 .8-.6 1.3-1.4 1.3H3.9c-.8 0-1.4-.5-1.4-1.3z"/><path d="M8 7.3v3.4M6.3 9h3.4"/>`)
)

// Search shows this many chats at most: past that, more words narrow it.
const searchLimit = 50

// searchEntry is a chat as search reads it, lowered once per search.
type searchEntry struct {
	title, text       string
	lowTitle, lowText string
}

func newSearchEntry(title string, texts []string) searchEntry {
	text := strings.Join(texts, "\n")
	return searchEntry{title: title, text: text, lowTitle: strings.ToLower(title), lowText: strings.ToLower(text)}
}

// chatHit is a chat search found: by its title, or by its messages, with
// the words around the first match.
type chatHit struct {
	index   int
	snippet string // "" for a title match
}

// matchChats finds the chats whose title, or else whose title and messages
// together, hold every word of the query: title matches first, each in the
// chats' order. An empty query matches every chat.
func matchChats(query string, entries []searchEntry) []chatHit {
	words := strings.Fields(strings.ToLower(query))
	var byTitle, byText []chatHit
	for i, entry := range entries {
		if containsAll(entry.lowTitle, words) {
			byTitle = append(byTitle, chatHit{index: i})
		} else if len(words) > 0 && containsAll(entry.lowTitle+"\n"+entry.lowText, words) {
			// Show a word the title does not explain.
			word := words[0]
			for _, w := range words {
				if !strings.Contains(entry.lowTitle, w) {
					word = w
					break
				}
			}
			byText = append(byText, chatHit{index: i, snippet: entry.snippet(word)})
		}
	}
	return append(byTitle, byText...)
}

func containsAll(text string, words []string) bool {
	for _, word := range words {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

// snippet is the text around the first place word is found, on one line.
func (entry searchEntry) snippet(word string) string {
	at := strings.Index(entry.lowText, word)
	if at < 0 {
		return ""
	}
	text := entry.text
	if len(text) != len(entry.lowText) { // lowering changed the byte offsets
		text = entry.lowText
	}
	start, end := max(0, at-30), min(len(text), at+len(word)+60)
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	snippet := strings.Join(strings.Fields(text[start:end]), " ")
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(text) {
		snippet += "…"
	}
	return snippet
}

// searchChats finds a chat of the target by its title or its messages
// (Ctrl+F): Up and Down choose, Enter opens, Escape closes.
func (ui *desktopUI) searchChats() {
	chats := ui.sideChats
	entries := make([]searchEntry, len(chats))
	for i, chat := range chats {
		var texts []string
		if chat.text != nil {
			texts = chat.text()
		}
		entries[i] = newSearchEntry(chat.title, texts)
	}
	field := newKeyEntry()
	field.SetPlaceHolder("Search chats by title or content")
	list := container.New(layout.NewCustomPaddedVBoxLayout(2))
	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(0, 340))
	var dialog *sheet
	var hits []chatHit
	var rows []*tapRow
	active := 0
	highlight := func(index int) {
		for i, row := range rows {
			if i == index {
				row.setHover(1, true)
			} else if row.hover > 0 {
				row.setHover(0, true)
			}
		}
		active = index
		if index < 0 || index >= len(rows) {
			return
		}
		y, height := rows[index].Position().Y, rows[index].Size().Height
		if y < scroll.Offset.Y {
			scroll.ScrollToOffset(fyne.NewPos(0, y))
		} else if y+height > scroll.Offset.Y+scroll.Size().Height {
			scroll.ScrollToOffset(fyne.NewPos(0, y+height-scroll.Size().Height))
		}
	}
	open := func(hit chatHit) {
		dialog.Hide()
		chat := chats[hit.index]
		delete(ui.foldedProjects, ui.projectOf(chat.folder)) // show where it is
		chat.open()
		ui.renderChats()
	}
	render := func() {
		hits = matchChats(field.Text, entries)
		list.RemoveAll()
		rows = nil
		for _, hit := range hits[:min(len(hits), searchLimit)] {
			hit := hit
			chat := chats[hit.index]
			row := newRow(chat.title, nil, styleMenu, func() { open(hit) })
			row.iconAfter = false
			row.truncate = true
			row.detail = "Other chats"
			if project := ui.projectOf(chat.folder); project != "" {
				row.detail = ui.projectName(project)
			}
			if hit.snippet != "" {
				row.detail += " · " + hit.snippet
			}
			if chat.running {
				row.badge = iconDot
			}
			rows = append(rows, row)
			list.Add(row)
		}
		if len(rows) == 0 {
			list.Add(padded(10, 10, 10, 0, textLine("No chat matches", 13, colFaint)))
		}
		list.Refresh()
		scroll.ScrollToTop()
		highlight(0)
	}
	field.OnChanged = func(string) { render() }
	field.onKey = func(key *fyne.KeyEvent) bool {
		switch key.Name {
		case fyne.KeyDown, fyne.KeyUp:
			if n := len(rows); n > 0 {
				step := 1
				if key.Name == fyne.KeyUp {
					step = -1
				}
				highlight((active + step + n) % n)
			}
		case fyne.KeyReturn, fyne.KeyEnter:
			if active >= 0 && active < len(rows) {
				open(hits[active])
			}
		case fyne.KeyEscape:
			dialog.dismiss()
		default:
			return false
		}
		return true
	}
	body := container.NewBorder(field, nil, nil, nil, padded(10, 0, 0, 0, scroll))
	dialog = ui.modal("Search chats", body, 560, nil)
	render()
	ui.window.Canvas().Focus(field)
}

// keyEntry is a one-line entry whose keys a list beneath it can take first.
type keyEntry struct {
	widget.Entry
	onKey func(*fyne.KeyEvent) bool
}

func newKeyEntry() *keyEntry {
	entry := &keyEntry{}
	entry.ExtendBaseWidget(entry)
	return entry
}

func (entry *keyEntry) TypedKey(key *fyne.KeyEvent) {
	if entry.onKey != nil && entry.onKey(key) {
		return
	}
	entry.Entry.TypedKey(key)
}
