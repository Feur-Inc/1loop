// SPDX-License-Identifier: MPL-2.0

//go:build !js

package memory

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// What Loopy knows when it answers. A short profile is always there: the
// rules, how to write to them, and what they said of themselves. Choosing
// the facts by how close they are to the message missed the ones that
// matter most ("an idea for dessert?" is not close to "allergic to nuts"):
// measured, a profile of the sure facts did better (docs/LOOPY.md). What
// was only noticed once, and past exchanges, come only when they have to do
// with the message, for a memory brought up out of place makes answers worse.

const profileFacts = 40

// profile is the facts always in mind: rules, style, the rest.
func (m *Memory) profile() (rules, style, about []Fact) {
	for _, fact := range m.state.Facts {
		switch {
		case !fact.Current() || !fact.Sure():
		case fact.Kind == Instruction:
			rules = append(rules, fact)
		case fact.Kind == Style:
			style = append(style, fact)
		default:
			about = append(about, fact)
		}
	}
	latest := func(facts []Fact, n int) []Fact {
		sort.SliceStable(facts, func(i, j int) bool {
			if facts[i].Pinned != facts[j].Pinned {
				return facts[i].Pinned
			}
			if facts[i].Said != facts[j].Said {
				return facts[i].Said
			}
			return facts[i].Seen.After(facts[j].Seen)
		})
		return facts[:min(n, len(facts))]
	}
	return latest(rules, 40), latest(style, 25), latest(about, profileFacts)
}

// Profile is what Loopy always knows. It changes rarely, so a cached prompt
// holds.
func (m *Memory) Profile() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	rules, style, about := m.profile()
	var b strings.Builder
	write := func(title string, lines []string, facts []Fact) {
		if len(lines)+len(facts) == 0 {
			return
		}
		b.WriteString(title + "\n")
		for _, line := range lines {
			b.WriteString("- " + line + "\n")
		}
		for _, fact := range facts {
			fmt.Fprintf(&b, "- [%s] %s\n", fact.ID, fact.Text)
		}
	}
	write("Rules the user gave you, or that their corrections show. Follow them without being told again:", nil, rules)
	write("How to write to them, and for them:", m.state.Style.Describe(), style)
	write("About them. It steers what you propose, plan and choose (what they do not eat, when they are free, who is who…) whenever it bears on the matter; when it does not, leave it out:", nil, about)
	return b.String()
}

// Recall is what a message brings back beyond the profile: facts and past
// exchanges that have to do with it. Empty when nothing does.
func (m *Memory) Recall(message string, skip map[string]bool) string {
	if len(words(message)) == 0 {
		return ""
	}
	m.mu.Lock()
	var b strings.Builder
	always := map[string]bool{}
	rules, style, about := m.profile()
	for _, fact := range slices.Concat(rules, style, about) {
		always[fact.ID] = true
	}
	var facts []Fact
	for _, fact := range m.near(message, 14, true) {
		if !always[fact.ID] && len(facts) < 10 {
			facts = append(facts, fact)
		}
	}
	m.mu.Unlock()
	if len(facts) > 0 {
		b.WriteString("Facts about the user:\n")
		for _, fact := range facts {
			fmt.Fprintf(&b, "- [%s] %s (%s", fact.ID, fact.Text, fact.Since.Format("2006-01-02"))
			if !fact.Sure() {
				b.WriteString(", noticed once: do not count on it")
			}
			b.WriteString(")\n")
		}
	}
	var hits []Hit
	for _, hit := range m.Search(Query{Text: message, Limit: 8, Skip: skip}) {
		if hit.Strong && len(hits) < 4 {
			hits = append(hits, hit)
		}
	}
	if len(hits) > 0 {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Record.Time.Before(hits[j].Record.Time) })
		b.WriteString("Earlier exchanges that may matter (older ones may no longer hold):\n")
		for _, hit := range hits {
			b.WriteString(hit.Record.Format(message, 500) + "\n")
		}
	}
	return b.String()
}

// Format is a record for the model, its parts cut to n runes around what
// the query is about.
func (record Record) Format(query string, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]", record.Time.Local().Format("Mon 2006-01-02 15:04"))
	if record.Event != "" {
		fmt.Fprintf(&b, " %s", excerpt(record.Event, query, n))
	}
	if record.User != "" {
		fmt.Fprintf(&b, " User: %s", excerpt(record.User, query, n))
	}
	if record.Loopy != "" {
		fmt.Fprintf(&b, "\n  You: %s", excerpt(record.Loopy, query, n))
	}
	if record.Final != "" {
		fmt.Fprintf(&b, "\n  Text they sent: %s", excerpt(record.Final, query, n))
	}
	return b.String()
}

// excerpt is the part of a text closest to the query, in words and in
// meaning, n runes long: what was asked about is often far in a long answer.
func excerpt(text, query string, n int) string {
	runes := []rune(text)
	if len(runes) <= n || query == "" {
		return clip(text, n)
	}
	asked := map[string]bool{}
	for _, word := range words(query) {
		asked[word] = true
	}
	meaning := Embed(query)
	best, from := -1.0, 0
	for at := 0; ; at += max(1, n/3) {
		part := string(runes[at:min(len(runes), at+n)])
		held := map[string]bool{}
		for _, word := range words(part) {
			if asked[word] {
				held[word] = true
			}
		}
		score := float64(len(held)) / float64(max(1, len(asked)))
		for _, p := range passages(part) {
			if v := Embed(p); v != nil && meaning != nil {
				score += float64(meaning.Close(v)) / float64(len(passages(part)))
			}
		}
		if score > best+1e-9 {
			best, from = score, at
		}
		if at+n >= len(runes) {
			break
		}
	}
	for back := 0; from > 0 && back < 80 && runes[from-1] != '\n' && runes[from-1] != ' '; back++ { // at a word
		from--
	}
	out := clip(string(runes[from:]), n)
	if from > 0 {
		out = "…" + out
	}
	return out
}

// FormatSearch is an answer to loopy_recall: the facts, those that ended
// included, then the exchanges in order of time.
func (m *Memory) FormatSearch(queries []string, since, until time.Time, limit int) string {
	var b strings.Builder
	m.mu.Lock()
	shown := map[string]bool{}
	byID := map[string]Fact{}
	for _, fact := range m.state.Facts {
		byID[fact.ID] = fact
	}
	var facts []Fact
	for _, query := range queries {
		for _, f := range m.known.search(query, 8, nil) {
			if fact, ok := byID[f.id]; ok && !shown[f.id] && (f.close >= .3 || f.share > 0) {
				shown[f.id] = true
				facts = append(facts, fact)
			}
		}
	}
	m.mu.Unlock()
	if len(facts) > 0 {
		b.WriteString("Facts:\n")
		for _, fact := range facts[:min(len(facts), 12)] {
			fmt.Fprintf(&b, "- [%s] %s: %s (since %s", fact.ID, fact.Kind, fact.Text, fact.Since.Format("2006-01-02"))
			if !fact.Current() {
				fmt.Fprintf(&b, ", until %s: no longer true", fact.Until.Format("2006-01-02"))
			} else if !fact.Sure() {
				b.WriteString(", noticed once")
			}
			b.WriteString(")\n")
		}
	}
	seen := map[string]bool{}
	var hits []Hit
	var found [][]Hit
	for _, query := range queries {
		found = append(found, m.Search(Query{Text: query, Since: since, Until: until, Limit: limit}))
	}
	for i := range limit { // the queries share the places, best first
		for _, list := range found {
			if i < len(list) && !seen[list[i].Record.ID] && len(hits) < limit {
				seen[list[i].Record.ID] = true
				hits = append(hits, list[i])
			}
		}
	}
	if len(hits) > 0 {
		b.WriteString("Exchanges, in order of time:\n" + formatHits(hits, strings.Join(queries, " ")))
	}
	if b.Len() == 0 {
		return "Nothing found. Try other words, or the other language."
	}
	return b.String()
}

// formatHits shows exchanges found, the best first, in order of time: more
// of those at the top.
func formatHits(hits []Hit, query string) string {
	room := map[string]int{}
	for i, hit := range hits {
		switch {
		case i < 3:
			room[hit.Record.ID] = 2000
		case i < 7:
			room[hit.Record.ID] = 900
		default:
			room[hit.Record.ID] = 450
		}
	}
	hits = slices.Clone(hits)
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Record.Time.Before(hits[j].Record.Time) })
	var b strings.Builder
	for _, hit := range hits {
		b.WriteString(hit.Record.Format(query, room[hit.Record.ID]) + "\n")
	}
	return b.String()
}

// Voice is a few of the texts the user sent or wrote, the closest to what
// is to be written: examples of how they write (PROSE, arXiv 2505.23815:
// a description and examples together do best).
func (m *Memory) Voice(about string, n int) []string {
	var out []string
	for _, hit := range m.Search(Query{Text: about, Limit: 40}) {
		if hit.Record.Final != "" && len(out) < n {
			out = append(out, clip(hit.Record.Final, 600))
		}
	}
	return out
}
