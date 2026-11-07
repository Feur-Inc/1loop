// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	registry "1loop/internal/settings"
)

func TestSidebarWidthWithin(t *testing.T) {
	for _, c := range []struct{ width, total, want float32 }{
		{280, 1400, 280},
		{90, 1400, sidebarMinWidth},   // never narrower than the minimum
		{900, 1400, sidebarMaxWidth},  // nor wider than the maximum
		{500, 900, 900 - sidebarRoom}, // the conversation keeps its room
		{500, 600, sidebarMinWidth},   // a tiny window: the minimum wins
	} {
		if got := sidebarWidthWithin(c.width, c.total); got != c.want {
			t.Errorf("sidebarWidthWithin(%v, %v) = %v, want %v", c.width, c.total, got, c.want)
		}
	}
}

func TestGroupByProject(t *testing.T) {
	type chat struct{ name, folder string }
	chats := []chat{{"a", "/p/one"}, {"b", "/p/two"}, {"c", "/p/one-wt"}, {"d", "/gone"}, {"e", "/p/one"}}
	worktrees := map[string]string{"/p/one-wt": "/p/one"}
	projectOf := func(folder string) string {
		if project := worktrees[folder]; project != "" {
			return project
		}
		return folder
	}
	groups := groupByProject([]string{"/p/one", "/p/two", "/p/empty"}, chats, func(c chat) string { return c.folder }, projectOf)
	var got []string
	for _, group := range groups {
		line := group.project + ":"
		for _, c := range group.chats {
			line += c.name
		}
		got = append(got, line)
	}
	// A worktree's chat is under its project, a project with no chat still
	// shows, and chats of no listed project come last, in the chats' order.
	want := []string{"/p/one:ace", "/p/two:b", "/p/empty:", ":d"}
	if !slices.Equal(got, want) {
		t.Errorf("groups = %q, want %q", got, want)
	}
	if groups := groupByProject([]string{"/p/one"}, chats[:1], func(c chat) string { return c.folder }, projectOf); len(groups) != 1 {
		t.Errorf("no other chats: %d groups, want 1", len(groups))
	}
}

func TestMatchChats(t *testing.T) {
	entries := []searchEntry{
		newSearchEntry("Rework the sidebar", []string{"Projects as groups"}),
		newSearchEntry("Fix the update manifest", []string{"The sidebar flickers when the manifest is signed"}),
		newSearchEntry("Android sounds", []string{"Notification sounds on the phone"}),
	}
	for _, c := range []struct {
		query string
		want  []int
	}{
		{"", []int{0, 1, 2}},            // nothing typed: every chat
		{"SIDEBAR", []int{0, 1}},        // case does not matter; title matches first
		{"sidebar groups", []int{0}},    // every word, in the title or in the messages
		{"manifest flickers", []int{1}}, // words anywhere in the messages
		{"phone sidebar", nil},          // a word found nowhere
		{"  sounds   phone ", []int{2}}, // extra spaces
	} {
		var got []int
		for _, hit := range matchChats(c.query, entries) {
			got = append(got, hit.index)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("matchChats(%q) = %v, want %v", c.query, got, c.want)
		}
	}
	hits := matchChats("flickers", entries)
	if len(hits) != 1 || hits[0].snippet != "The sidebar flickers when the manifest is signed" {
		t.Errorf("snippet = %+v", hits)
	}
}

func TestSearchSnippet(t *testing.T) {
	long := strings.Repeat("word ", 30) + "needle " + strings.Repeat("étoile ", 30)
	snippet := newSearchEntry("", []string{long}).snippet("needle")
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") || !strings.Contains(snippet, "needle") {
		t.Errorf("snippet = %q, want the words around the match, cut on both sides", snippet)
	}
	if !utf8.ValidString(snippet) {
		t.Errorf("snippet %q cuts a character in two", snippet)
	}
	if got := newSearchEntry("", []string{"two\nlines"}).snippet("lines"); got != "two lines" {
		t.Errorf("snippet = %q, want one line", got)
	}
}

func TestMenuMatches(t *testing.T) {
	actions := []menuAction{
		{label: "1loop", group: "/p/1loop"},
		{label: "    claude/sidebar", group: "/p/1loop"},
		{label: "    claude/settings", group: "/p/1loop"},
		{label: "website", group: "/p/website"},
		{label: "Add folder…"},
	}
	for _, c := range []struct {
		text string
		want []bool
	}{
		{"", []bool{true, true, true, true, true}},
		{"sidebar", []bool{true, true, false, false, false}}, // a worktree keeps its project above it
		{"1LOOP", []bool{true, true, true, false, false}},    // a project keeps its worktrees
		{"web", []bool{false, false, false, true, false}},
		{"folder", []bool{false, false, false, false, true}},
		{"1loop settings", []bool{true, false, true, false, false}}, // words from the project and the worktree
	} {
		if got := menuMatches(actions, c.text); !slices.Equal(got, c.want) {
			t.Errorf("menuMatches(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestSearchTag(t *testing.T) {
	for _, c := range []struct {
		query  string
		chosen searchKind
		kind   searchKind
		rest   string
	}{
		{"sidebar", searchAll, searchAll, "sidebar"},
		{"chat: sidebar", searchAll, searchChatsOnly, " sidebar"},
		{"  Settings:sudo", searchAll, searchSettingsOnly, "sudo"},
		{"setting:", searchChatsOnly, searchSettingsOnly, ""}, // a typed tag wins over the chip
		{"sudo", searchSettingsOnly, searchSettingsOnly, "sudo"},
		{"what is chat: about", searchAll, searchAll, "what is chat: about"}, // only at the start
	} {
		kind, rest := searchTag(c.query, c.chosen)
		if kind != c.kind || rest != c.rest {
			t.Errorf("searchTag(%q, %v) = %v, %q; want %v, %q", c.query, c.chosen, kind, rest, c.kind, c.rest)
		}
	}
}

func TestFindAll(t *testing.T) {
	chats := []searchEntry{
		newSearchEntry("Tool permissions for sudo", nil),
		newSearchEntry("Landing page", []string{"hero text"}),
	}
	settings := registry.On(registry.OnDesktop)
	has := func(found []registry.Setting, title string) bool {
		for _, setting := range found {
			if setting.Title == title {
				return true
			}
		}
		return false
	}

	// Both kinds, chats first.
	kind, found := findAll("permissions", searchAll, chats, settings)
	if kind != searchAll || len(found.chats) != 1 || found.chats[0].index != 0 || !has(found.settings, "Tool permissions") {
		t.Errorf("all: kind %v, chats %+v, settings %d", kind, found.chats, len(found.settings))
	}
	// Settings go through their own matcher, which forgives a typo.
	if _, found := findAll("permisions", searchAll, chats, settings); len(found.chats) != 0 || !has(found.settings, "Tool permissions") {
		t.Errorf("typo: %d chats, %d settings", len(found.chats), len(found.settings))
	}
	// A tag leaves out the other kind.
	if _, found := findAll("chat: permissions", searchAll, chats, settings); len(found.chats) != 1 || len(found.settings) != 0 {
		t.Errorf("chat: tag: %d chats, %d settings", len(found.chats), len(found.settings))
	}
	if kind, found := findAll("sudo", searchSettingsOnly, chats, settings); kind != searchSettingsOnly || len(found.chats) != 0 || !has(found.settings, "Tool permissions") {
		t.Errorf("Settings chip: %d chats, %d settings", len(found.chats), len(found.settings))
	}
	// Nothing typed: the chats, and every setting only when asked for.
	if _, found := findAll("", searchAll, chats, settings); len(found.chats) != 2 || len(found.settings) != 0 {
		t.Errorf("empty: %d chats, %d settings", len(found.chats), len(found.settings))
	}
	if _, found := findAll("setting:", searchAll, chats, settings); len(found.chats) != 0 || len(found.settings) != len(settings) {
		t.Errorf("setting: alone: %d chats, %d settings, want all %d", len(found.chats), len(found.settings), len(settings))
	}
	if _, found := findAll("hero", searchAll, chats, settings); len(found.chats) != 1 || found.chats[0].snippet != "hero text" {
		t.Errorf("content: %+v", found.chats)
	}
}
