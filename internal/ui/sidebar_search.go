// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	registry "1loop/internal/settings"
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

// What the search looks for: everything, or one kind, chosen with a tag
// above the results or typed as a prefix ("chat:", "setting:").
type searchKind int

const (
	searchAll searchKind = iota
	searchChatsOnly
	searchSettingsOnly
)

var searchKinds = []struct {
	kind  searchKind
	label string
}{{searchAll, "All"}, {searchChatsOnly, "Chats"}, {searchSettingsOnly, "Settings"}}

// searchPrefixes are the tags that can be typed at the start of a search.
var searchPrefixes = map[string]searchKind{
	"chat:": searchChatsOnly, "chats:": searchChatsOnly,
	"setting:": searchSettingsOnly, "settings:": searchSettingsOnly,
}

// searchTag takes a typed tag off the start of the query: the kind it
// names, or the one chosen, and the words left.
func searchTag(query string, chosen searchKind) (searchKind, string) {
	trimmed := strings.TrimLeft(query, " ")
	lower := strings.ToLower(trimmed)
	for prefix, kind := range searchPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return kind, trimmed[len(prefix):]
		}
	}
	return chosen, query
}

// searchFound is what a search shows: chats, then settings.
type searchFound struct {
	chats    []chatHit
	settings []registry.Setting
}

// findAll runs a search over the chats and the settings, limited to one
// kind by a tag. With no words it lists the chats, or every setting when
// only settings are asked for.
func findAll(query string, chosen searchKind, chats []searchEntry, settings []registry.Setting) (searchKind, searchFound) {
	kind, words := searchTag(query, chosen)
	var found searchFound
	if kind != searchSettingsOnly {
		found.chats = matchChats(words, chats)
		if len(found.chats) > searchLimit {
			found.chats = found.chats[:searchLimit]
		}
	}
	if kind != searchChatsOnly {
		if strings.TrimSpace(words) == "" {
			if kind == searchSettingsOnly {
				found.settings = settings
			}
		} else {
			for _, match := range registry.Search(settings, words) {
				found.settings = append(found.settings, match.Setting)
			}
		}
	}
	return kind, found
}

// search finds chats of the target and settings in one list (Ctrl+K):
// Up and Down choose, Enter opens, Tab changes the tag, Escape closes.
func (ui *desktopUI) search() {
	chats := ui.sideChats
	entries := make([]searchEntry, len(chats))
	for i, chat := range chats {
		var texts []string
		if chat.text != nil {
			texts = chat.text()
		}
		entries[i] = newSearchEntry(chat.title, texts)
	}
	settings := registry.On(registry.OnDesktop)
	field := newKeyEntry()
	field.SetPlaceHolder("Search chats and settings")
	field.tabs = true
	tags := container.New(layout.NewCustomPaddedHBoxLayout(6))
	list := container.New(layout.NewCustomPaddedVBoxLayout(2))
	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(0, 360))
	var dialog *sheet
	var rows []*tapRow
	var opens []func()
	chosen, active := searchAll, 0
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
	add := func(row *tapRow, open func()) {
		row.iconAfter = false
		row.truncate = true
		row.onTap = func() { dialog.Hide(); open() }
		rows = append(rows, row)
		opens = append(opens, open)
		list.Add(row)
	}
	heading := func(text string) {
		list.Add(padded(8, 2, 10, 0, textLine(text, 12, colFaint)))
	}
	var render func()
	showTags := func(kind searchKind) {
		tags.RemoveAll()
		for _, tag := range searchKinds {
			tag := tag
			chip := newRow(tag.label, nil, styleChip, func() {
				chosen = tag.kind
				if typed, words := searchTag(field.Text, chosen); typed != chosen {
					field.SetText(strings.TrimLeft(words, " ")) // the chip wins over a typed tag
				}
				render()
				ui.window.Canvas().Focus(field)
			})
			chip.accent = tag.kind == kind
			chip.selected = tag.kind == kind
			tags.Add(chip)
		}
	}
	render = func() {
		kind, found := findAll(field.Text, chosen, entries, settings)
		showTags(kind)
		list.RemoveAll()
		rows, opens = nil, nil
		if len(found.chats) > 0 {
			heading("Chats")
		}
		for _, hit := range found.chats {
			chat := chats[hit.index]
			row := newRow(chat.title, nil, styleMenu, nil)
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
			add(row, func() {
				delete(ui.foldedProjects, ui.projectOf(chat.folder)) // show where it is
				chat.open()
				ui.renderChats()
			})
		}
		if len(found.settings) > 0 {
			heading("Settings")
		}
		for _, setting := range found.settings {
			key := setting.Key
			row := newRow(setting.Title, iconSliders, styleMenu, nil)
			row.detail = registry.SectionTitle(setting.Section)
			if setting.Page != "" {
				row.detail += " › " + setting.Page
			}
			row.detail += " · " + setting.Description
			add(row, func() {
				ui.settings()
				ui.prefs.jump(key) // opens its page, its row glowing
			})
		}
		if len(rows) == 0 {
			list.Add(padded(10, 10, 10, 0, textLine("Nothing matches", 13, colFaint)))
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
				dialog.Hide()
				opens[active]()
			}
		case fyne.KeyTab:
			kind, words := searchTag(field.Text, chosen)
			chosen = (kind + 1) % searchKind(len(searchKinds))
			if words != field.Text {
				field.SetText(strings.TrimLeft(words, " ")) // renders
			} else {
				render()
			}
		case fyne.KeyEscape:
			dialog.dismiss()
		default:
			return false
		}
		return true
	}
	hint := textLine("Tab or chat: / setting: to narrow", 12, colFaint)
	top := container.NewVBox(field, padded(8, 0, 0, 0, container.NewBorder(nil, nil, tags, container.NewCenter(hint))))
	body := container.NewBorder(top, nil, nil, nil, padded(8, 0, 0, 0, scroll))
	dialog = ui.modal("Search", body, 600, nil)
	render()
	ui.window.Canvas().Focus(field)
}

// keyEntry is a one-line entry whose keys a list beneath it can take first.
type keyEntry struct {
	widget.Entry
	onKey    func(*fyne.KeyEvent) bool
	tabs     bool                     // Tab goes to onKey instead of moving the focus
	shortcut func(fyne.Shortcut) bool // the app's shortcuts, before the entry's own
}

func (entry *keyEntry) TypedShortcut(s fyne.Shortcut) {
	if entry.shortcut != nil && entry.shortcut(s) {
		return
	}
	entry.Entry.TypedShortcut(s)
}

func (entry *keyEntry) AcceptsTab() bool { return entry.tabs }

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
