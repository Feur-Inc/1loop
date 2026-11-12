// SPDX-License-Identifier: MPL-2.0

package loopy

// Recall: Loopy searches what it keeps, its facts, its journal and the
// whole conversation, which outlives the agent's compacted history.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Hit is one thing found, with where and when it is from.
type Hit struct {
	Kind string // "fact", "journal", "message"
	Time time.Time
	Text string
}

// Recall finds what holds the most words of query, the best and latest
// first, at most limit.
func (store *Store) Recall(query string, limit int) []Hit {
	words := strings.Fields(strings.ToLower(query))
	score := func(text string) int {
		lower, n := strings.ToLower(text), 0
		for _, word := range words {
			if len(word) > 2 && strings.Contains(lower, word) {
				n++
			}
		}
		return n
	}
	type scored struct {
		hit   Hit
		score int
	}
	var found []scored
	add := func(hit Hit) {
		if s := score(hit.Text); s > 0 {
			found = append(found, scored{hit, s})
		}
	}
	for _, fact := range store.Facts() {
		add(Hit{Kind: "fact", Time: fact.Time, Text: fact.Text + " (" + fact.Category + ", id " + fact.ID + ")"})
	}
	for _, entry := range store.Entries() {
		add(Hit{Kind: "journal", Time: entry.Time, Text: entry.Text})
	}
	for _, message := range store.Snapshot() {
		text := message.Text
		if message.Card != nil {
			text = message.Card.Title + ": " + message.Card.Body
			if message.Card.Answer != "" {
				text += " (answered: " + message.Card.Answer + ")"
			}
		}
		if text != "" {
			add(Hit{Kind: "message from " + message.From, Time: message.Time, Text: text})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		return found[i].hit.Time.After(found[j].hit.Time)
	})
	var hits []Hit
	for i := 0; i < len(found) && i < limit; i++ {
		hits = append(hits, found[i].hit)
	}
	return hits
}

// Format writes hits for the model, one per line.
func Format(hits []Hit) string {
	if len(hits) == 0 {
		return "Nothing found."
	}
	var b strings.Builder
	for _, hit := range hits {
		text := hit.Text
		if len([]rune(text)) > 400 {
			text = string([]rune(text)[:400]) + "…"
		}
		fmt.Fprintf(&b, "- [%s, %s] %s\n", hit.Kind, hit.Time.Local().Format("2 Jan 2006 15:04"), text)
	}
	return b.String()
}
