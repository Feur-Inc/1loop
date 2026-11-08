// SPDX-License-Identifier: MPL-2.0

package ui

// Settings search, at the top of the settings' sidebar. Typing filters the
// settings live, by their name, their description, their section and the
// words people use for them ("server", "model", "cookies"). Each match is
// shown with its section; opening one opens that section's page, brings the
// setting into view and lights it up for a moment. Clearing the field brings
// back the section that was open. The phone has the same index and wording
// (SettingsSearch.kt): keep the two in step.

import (
	"image/color"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// settingEntry is one setting the search finds: its row's title on its
// section's page (which the highlight looks for), a short description, and
// other words for it.
type settingEntry struct {
	section, title, detail, words string
}

var settingsIndex = []settingEntry{
	{"Execution target", "Project folder", "The folder of the project the agent works in.", "workspace directory path projects"},
	{"Execution target", "This computer", "Let the agent work on this computer.", "local machine target"},
	{"Execution target", "SSH machines", "Computers the agent works on over SSH.", "ssh remote machine host vm"},
	{"Execution target", "Connect a server", "Pair this computer with a headless 1loop server, then work on it.", "remote server https headless pair pairing fingerprint certificate docker"},
	{"Connections", "Connections", "Accounts and services whose models the agent uses.", "accounts models api key provider chatgpt claude code openrouter vllm antigravity add connection"},
	{"Connections", "Claude Code and Codex chats", "Import the conversations of Claude Code and Codex.", "import history sessions"},
	{"Agent", "Tool permissions", "Approval, YOLO or Auto: what the agent may do without asking.", "approval yolo auto mode sandbox permissions approve ask"},
	{"Agent", "Automatic resumption", "What happens when a conversation reaches a rate limit.", "rate limit quota wait fallback model resume"},
	{"Agent", "Dynamic compaction", "How the conversation's context is kept small.", "context compaction summary tokens compact"},
	{"Agent", "Hook commands", "Your commands run at the agent's events.", "hooks scripts events"},
	{"Instructions", "Your preferences", "What the agent should know about you and how to work.", "personalization language tone instructions custom prompt"},
	{"Instructions", "Memory", "Let the agent add what it learns about you to your preferences.", "remember learn"},
	{"Instructions", "Project memory", "Each project's AGENTS.md.", "agents.md project instructions"},
	{"Browser and computer", "Browsing data", "Cookies, logins, cache and history of the agents' browser.", "browser cookies cache history clear logins browser use"},
	{"Browser and computer", "Import cookies", "Bring logins from your own browser into Browser Use.", "browser cookies login import"},
	{"Browser and computer", "Shared screen", "The screen Computer Use may control.", "computer use screen display share"},
	{"Extensions", "Extensions", "Skills, plugins and MCP servers.", "skills plugins mcp servers store install tools"},
	{"Extensions", "GitHub connector", "Let the agent use your GitHub repositories.", "github repositories connector token"},
	{"Phone remote", "Allow the 1loop phone app on this network", "Use 1loop on this computer from the phone app.", "phone android mobile remote network"},
	{"Phone remote", "Pair a phone", "Show a QR code to pair the phone app.", "phone qr code pairing pair"},
	{"Phone remote", "Paired phones", "Phones allowed to use this computer.", "phone devices forget"},
	{"Updates", "1loop updates", "Check for a new version and install it.", "update version upgrade release install"},
}

// searchSettings is what matches query, best first: every word of it must
// appear somewhere in the entry; a title that starts with it ranks first.
func searchSettings(query string) []settingEntry {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	type scored struct {
		entry settingEntry
		score int
		order int
	}
	var found []scored
	for i, entry := range settingsIndex {
		title := strings.ToLower(entry.title)
		haystack := title + " " + strings.ToLower(entry.detail+" "+entry.section+" "+entry.words)
		matched := true
		for _, word := range words {
			if !strings.Contains(haystack, word) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		score := 0
		switch first := words[0]; {
		case strings.HasPrefix(title, first):
			score = 3
		case strings.Contains(title, first):
			score = 2
		case strings.Contains(strings.ToLower(entry.section), first):
			score = 1
		}
		found = append(found, scored{entry, score, i})
	}
	sort.SliceStable(found, func(a, b int) bool { return found[a].score > found[b].score })
	out := make([]settingEntry, len(found))
	for i, match := range found {
		out[i] = match.entry
	}
	return out
}

// clearSearch empties the field without showing a page: a section is
// about to be shown.
func (settings *settingsView) clearSearch() {
	changed := settings.search.OnChanged
	settings.search.OnChanged = nil
	settings.search.SetText("")
	settings.search.OnChanged = changed
}

// searchChanged runs at each keystroke in the search field.
func (settings *settingsView) searchChanged(query string) {
	if strings.TrimSpace(query) == "" {
		if settings.searching() {
			settings.showNow(settings.section) // the normal layout again
		}
		return
	}
	settings.confirmLeave(0, func() {
		for len(settings.frames) > 0 {
			settings.drop()
		}
		for _, row := range settings.rows {
			row.SetSelected(false)
		}
		settings.frames = []settingsFrame{{content: settings.results(query), search: true}}
		settings.present()
	})
}

// searching tells whether the page shows search results.
func (settings *settingsView) searching() bool {
	return len(settings.frames) == 1 && settings.frames[0].search
}

// results is the page of what matches query.
func (settings *settingsView) results(query string) fyne.CanvasObject {
	matches := searchSettings(query)
	page := container.NewVBox(settings.pageTitle("Search", "", nil))
	if len(matches) == 0 {
		page.Add(card(settingRow("No setting matches “"+strings.TrimSpace(query)+"”",
			"Try another word, such as server, model, phone or browser, or clear the search to see every section.", nil)))
		return page
	}
	var rows []fyne.CanvasObject
	for _, match := range matches {
		match := match
		open := newRow(match.section, iconChevronRight, styleChip, func() { settings.reveal(match.section, match.title) })
		open.iconAfter = true
		row := newRow(match.title, nil, styleNav, func() { settings.reveal(match.section, match.title) })
		row.detail = match.detail
		row.size = 14
		rows = append(rows, padded(4, 4, 4, 12, container.NewBorder(nil, nil, nil, container.NewCenter(open), row)))
	}
	page.Add(card(rows...))
	return page
}

// reveal opens a setting's section, scrolls to its row and lights it up.
func (settings *settingsView) reveal(section, title string) {
	settings.showNow(section)
	light := settings.anchors[title]
	if light == nil {
		return // a whole section: its page is enough
	}
	// Laid out on the next frame: positions are known once it is drawn.
	time.AfterFunc(60*time.Millisecond, func() {
		fyne.Do(func() {
			driver := fyne.CurrentApp().Driver()
			top := driver.AbsolutePositionForObject(light).Y - driver.AbsolutePositionForObject(settings.scroll.Content).Y
			settings.scroll.ScrollToOffset(fyne.NewPos(0, fyne.Max(0, top-48)))
			flash(light)
		})
	})
}

// flash lights a row up in amber and lets the light fade over 1.6 s.
func flash(light *canvas.Rectangle) {
	anim := fyne.NewAnimation(1600*time.Millisecond, func(p float32) {
		light.FillColor = withAlpha(colAmber, 0.22*(1-p))
		if p >= 1 {
			light.FillColor = color.Transparent
		}
		light.Refresh()
	})
	anim.Curve = fyne.AnimationEaseIn
	anim.Start()
}
