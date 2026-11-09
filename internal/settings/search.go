// SPDX-License-Identifier: MPL-2.0

package settings

import (
	"sort"
	"strings"
	"unicode"
)

// The search: every word typed must be found in a setting, in its title,
// its keywords, its section or its explanation, whole, as the start of a
// word (it is being typed) or with a typo or two. A word found in the title
// counts most. The phone's copy (SettingsSearch.kt) does the same, and its
// tests are the same cases.

// Match is a setting the search found, with how well it matches.
type Match struct {
	Setting Setting
	Score   int
}

// How much a word found in each part of a setting counts.
const (
	inTitle       = 10
	inKeywords    = 7
	inSection     = 3
	inDescription = 2
)

// Words too common to say anything ("turn off the badge").
var stopWords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "to": true, "for": true,
	"in": true, "on": true, "off": true, "my": true, "and": true, "or": true, "is": true, "how": true,
	"do": true, "i": true, "turn": true, "set": true, "change": true, "make": true, "enable": true, "disable": true}

// Search ranks the settings that match a query, best first; a blank query
// matches none.
func Search(settings []Setting, query string) []Match {
	words := Words(query)
	var meaningful []string
	for _, word := range words {
		if !stopWords[word] {
			meaningful = append(meaningful, word)
		}
	}
	if len(meaningful) > 0 {
		words = meaningful
	}
	if len(words) == 0 {
		return nil
	}
	phrase := strings.Join(words, " ")
	var matches []Match
	for _, setting := range settings {
		if score := scoreSetting(setting, words, phrase); score > 0 {
			matches = append(matches, Match{setting, score})
		}
	}
	// Equal scores keep the order of the list, which is the page's.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	return matches
}

func scoreSetting(setting Setting, words []string, phrase string) int {
	title := Words(setting.Title)
	keywords := Words(strings.Join(setting.Keywords, " "))
	for _, value := range setting.Values {
		keywords = append(keywords, Words(value.Label)...)
	}
	section := Words(SectionTitle(setting.Section))
	description := Words(setting.Description)
	total := 0
	for _, word := range words {
		best := max(
			inTitle*wordScore(word, title),
			inKeywords*wordScore(word, keywords),
			inSection*wordScore(word, section),
			inDescription*wordScore(word, description))
		if best == 0 {
			return 0 // every word must be somewhere
		}
		total += best
	}
	// The title itself, typed whole or from its start, comes first.
	whole := strings.Join(title, " ")
	switch {
	case whole == phrase:
		total += 400
	case strings.HasPrefix(whole, phrase):
		total += 200
	case strings.Contains(whole, phrase):
		total += 100
	}
	return total
}

// wordScore is how well a typed word matches the best of some words: 10 for
// the word itself, 8 for the start of one, less for typos, 0 for none.
func wordScore(typed string, words []string) int {
	best := 0
	for _, word := range words {
		best = max(best, matchWord(typed, word))
	}
	return best
}

func matchWord(typed, word string) int {
	if typed == word {
		return 10
	}
	if strings.HasPrefix(word, typed) {
		return 8
	}
	allowed := typos(len([]rune(typed)))
	if allowed == 0 {
		return 0
	}
	// The whole word misspelled, or its start while it is being typed.
	distance := editDistance(typed, word)
	if start := []rune(word); len(start) > len([]rune(typed)) {
		distance = min(distance, editDistance(typed, string(start[:len([]rune(typed))])))
	}
	if distance > allowed {
		return 0
	}
	return 6 - 2*distance
}

// typos is how many mistakes a typed word of that length may have: none in
// a short one, where any word would be one or two away.
func typos(length int) int {
	switch {
	case length >= 8:
		return 2
	case length >= 4:
		return 1
	}
	return 0
}

// editDistance counts the letters to add, remove, change or swap with the
// next one to go from a to b.
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	rows := make([][]int, len(x)+1)
	for i := range rows {
		rows[i] = make([]int, len(y)+1)
		rows[i][0] = i
	}
	for j := range rows[0] {
		rows[0][j] = j
	}
	for i := 1; i <= len(x); i++ {
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && x[i-1] == y[j-2] && x[i-2] == y[j-1] {
				rows[i][j] = min(rows[i][j], rows[i-2][j-2]+1)
			}
		}
	}
	return rows[len(x)][len(y)]
}

// Words splits text into lower-case words of letters and digits: "Claude
// Code's AGENTS.md" is claude, code, s, agents, md.
func Words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
