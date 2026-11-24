// SPDX-License-Identifier: MPL-2.0

//go:build !js

package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// What a message brings back, for Loopy's prompt. Rules and the way to
// answer are always there; the rest only when it has to do with the
// message, for a memory brought up out of place makes answers worse
// (docs/LOOPY.md).

// Profile is what Loopy always knows: the rules, how the user likes things,
// and the pinned facts. It changes rarely, so a cached prompt holds.
func (m *Memory) Profile() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rules, style, pinned []Fact
	for _, fact := range m.state.Facts {
		switch {
		case !fact.Current() || !fact.Sure():
		case fact.Kind == Instruction:
			rules = append(rules, fact)
		case fact.Kind == Style:
			style = append(style, fact)
		case fact.Pinned:
			pinned = append(pinned, fact)
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
	var b strings.Builder
	write := func(title string, facts []Fact) {
		if len(facts) == 0 {
			return
		}
		b.WriteString(title + "\n")
		for _, fact := range facts {
			fmt.Fprintf(&b, "- [%s] %s\n", fact.ID, fact.Text)
		}
	}
	write("Rules the user gave you, or that their corrections show. Follow them without being told again:", latest(rules, 40))
	lines := m.state.Style.Describe()
	style = latest(style, 25)
	if len(style)+len(lines) > 0 {
		b.WriteString("How to write to them:\n")
		for _, line := range lines {
			b.WriteString("- " + line + "\n")
		}
		for _, fact := range style {
			fmt.Fprintf(&b, "- [%s] %s\n", fact.ID, fact.Text)
		}
	}
	write("What you always keep in mind about them:", latest(pinned, 25))
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
	var facts []Fact
	for _, fact := range m.near(message, 14, true) {
		if fact.Pinned || (fact.Kind == Instruction || fact.Kind == Style) && fact.Sure() { // in the profile
			continue
		}
		if len(facts) < 10 {
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
			b.WriteString(hit.Record.Format(500) + "\n")
		}
	}
	return b.String()
}

// Format is a record for the model, its parts cut to n runes.
func (record Record) Format(n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]", record.Time.Local().Format("Mon 2006-01-02 15:04"))
	if record.Event != "" {
		fmt.Fprintf(&b, " %s", clip(record.Event, n))
	}
	if record.User != "" {
		fmt.Fprintf(&b, " User: %s", clip(record.User, n))
	}
	if record.Loopy != "" {
		fmt.Fprintf(&b, "\n  You: %s", clip(record.Loopy, n))
	}
	if record.Final != "" {
		fmt.Fprintf(&b, "\n  Text they sent: %s", clip(record.Final, n))
	}
	return b.String()
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
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Record.Time.Before(hits[j].Record.Time) })
	if len(hits) > 0 {
		b.WriteString("Exchanges, in order of time:\n")
		for _, hit := range hits {
			b.WriteString(hit.Record.Format(700) + "\n")
		}
	}
	if b.Len() == 0 {
		return "Nothing found. Try other words, or the other language."
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
