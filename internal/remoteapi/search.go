// SPDX-License-Identifier: MPL-2.0

package remoteapi

// Chats found by their title, or else by their title and messages together,
// with the words around the match (#700): the desktop's search (Ctrl+K) and
// GET /api/search, which the phone asks, since it holds the messages of the
// open chat only.

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// SearchLimit is how many chats a search shows at most: past that, more
// words narrow it.
const SearchLimit = 50

// SearchEntry is a chat as search reads it, lowered once per search.
type SearchEntry struct {
	ID, Title, Text   string
	lowTitle, lowText string
}

func NewSearchEntry(id, title string, texts []string) SearchEntry {
	text := strings.Join(texts, "\n")
	return SearchEntry{ID: id, Title: title, Text: text, lowTitle: strings.ToLower(title), lowText: strings.ToLower(text)}
}

// ChatTexts is what search reads of a chat: its messages and answers.
func ChatTexts(items []agent.ChatItem) []string {
	var texts []string
	for _, item := range items {
		if item.Kind == "user" || item.Kind == "assistant" {
			texts = append(texts, item.Text)
		}
	}
	return texts
}

// ChatHit is a chat search found: by its title, or by its messages, with
// the words around the first match.
type ChatHit struct {
	Index   int    `json:"-"`
	ID      string `json:"id"`
	Snippet string `json:"snippet,omitempty"` // "" for a title match
}

// MatchChats finds the chats whose title, or else whose title and messages
// together, hold every word of the query: title matches first, each in the
// chats' order. An empty query matches every chat.
func MatchChats(query string, entries []SearchEntry) []ChatHit {
	words := strings.Fields(strings.ToLower(query))
	var byTitle, byText []ChatHit
	for i, entry := range entries {
		if containsAll(entry.lowTitle, words) {
			byTitle = append(byTitle, ChatHit{Index: i, ID: entry.ID})
		} else if len(words) > 0 && containsAll(entry.lowTitle+"\n"+entry.lowText, words) {
			// Show a word the title does not explain.
			word := words[0]
			for _, w := range words {
				if !strings.Contains(entry.lowTitle, w) {
					word = w
					break
				}
			}
			byText = append(byText, ChatHit{Index: i, ID: entry.ID, Snippet: entry.Snippet(word)})
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

// Snippet is the text around the first place word is found, on one line.
func (entry SearchEntry) Snippet(word string) string {
	at := strings.Index(entry.lowText, word)
	if at < 0 {
		return ""
	}
	text := entry.Text
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

// SearchChats is GET /api/search?q=: the chats found, at most SearchLimit,
// as {"chats": [{id, snippet}], "more": n}. A query with no word finds none:
// the phone lists every chat itself.
func SearchChats(w http.ResponseWriter, r *http.Request, entries []SearchEntry) {
	hits := []ChatHit{}
	more := 0
	if query := r.URL.Query().Get("q"); strings.TrimSpace(query) != "" {
		hits = MatchChats(query, entries)
		if len(hits) > SearchLimit {
			hits, more = hits[:SearchLimit], len(hits)-SearchLimit
		}
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"chats": hits, "more": more})
}
