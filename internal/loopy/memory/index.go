// SPDX-License-Identifier: MPL-2.0

//go:build !js

package memory

// The index finds entries by their words (BM25) and by their meaning
// (embed.go), and joins the two orders (reciprocal rank fusion). Both are
// needed: on LongMemEval each alone misses evidence the other finds
// (docs/LOOPY.md). An entry has several keys (what the user wrote, what
// Loopy answered, the facts drawn from them): the best one counts.

import (
	"math"
	"sort"
	"strings"
)

type entry struct {
	id      string
	counts  map[string]int
	length  int
	vectors []Vector
	gone    bool
}

type index struct {
	entries []*entry
	byID    map[string]int
	posting map[string][]int // word → entries holding it
	total   int              // words
	live    int
}

func newIndex() *index { return &index{byID: map[string]int{}, posting: map[string][]int{}} }

// passages cuts a long text in parts whose meaning is one thing.
func passages(text string) []string {
	fields := strings.Fields(text)
	const size, step = 150, 120
	if len(fields) <= size+30 {
		return []string{text}
	}
	var out []string
	for at := 0; at < len(fields) && len(out) < 24; at += step {
		out = append(out, strings.Join(fields[at:min(len(fields), at+size)], " "))
		if at+size >= len(fields) {
			break
		}
	}
	return out
}

// set adds or replaces an entry's keys; weights tell the keys' order: the
// first is the entry's own words.
func (ix *index) set(id string, keys ...string) {
	ix.remove(id)
	e := &entry{id: id, counts: map[string]int{}}
	for _, key := range keys {
		for _, word := range words(clip(key, 6000)) {
			e.counts[word]++
			e.length++
		}
		for _, part := range passages(clip(key, 6000)) {
			if v := Embed(part); v != nil {
				e.vectors = append(e.vectors, v)
			}
		}
	}
	at := len(ix.entries)
	ix.entries = append(ix.entries, e)
	ix.byID[id] = at
	for word := range e.counts {
		ix.posting[word] = append(ix.posting[word], at)
	}
	ix.total += e.length
	ix.live++
}

func (ix *index) remove(id string) {
	if at, ok := ix.byID[id]; ok {
		e := ix.entries[at]
		e.gone = true
		ix.total -= e.length
		ix.live--
		delete(ix.byID, id)
	}
}

// found is an entry's place in an answer: its fused score, its closeness
// in meaning (0 to 1) and how many of the query's words it holds.
type found struct {
	id    string
	score float64
	close float32
	share float64 // of the query's words, weighed by how telling they are
}

// search orders the entries for one query; keep drops entries.
func (ix *index) search(query string, limit int, keep func(id string) bool) []found {
	if ix.live == 0 {
		return nil
	}
	n := len(ix.entries)
	lexical := make([]float64, n)
	share := make([]float64, n)
	average := math.Max(1, float64(ix.total)/float64(ix.live))
	seen := map[string]bool{}
	whole := 0.0
	for _, word := range words(query) {
		if seen[word] {
			continue
		}
		seen[word] = true
		df := 0
		for _, at := range ix.posting[word] {
			if !ix.entries[at].gone {
				df++
			}
		}
		idf := math.Log(1 + (float64(ix.live)-float64(df)+.5)/(float64(df)+.5))
		whole += idf
		for _, at := range ix.posting[word] {
			e := ix.entries[at]
			if e.gone {
				continue
			}
			c := float64(e.counts[word])
			lexical[at] += idf * c * 2.2 / (c + 1.2*(.25+.75*float64(e.length)/average))
			share[at] += idf
		}
	}
	meaning := make([]float32, n)
	if q := Embed(query); q != nil {
		for at, e := range ix.entries {
			if e.gone {
				continue
			}
			for _, v := range e.vectors {
				meaning[at] = max(meaning[at], q.Close(v))
			}
		}
	}
	order := make([]int, 0, ix.live)
	for at, e := range ix.entries {
		if !e.gone && (keep == nil || keep(e.id)) {
			order = append(order, at)
		}
	}
	scores := make([]float64, n)
	join := func(better func(a, b int) bool, counts func(at int) bool) {
		sorted := append([]int(nil), order...)
		sort.SliceStable(sorted, func(i, j int) bool { return better(sorted[i], sorted[j]) })
		for rank, at := range sorted {
			if !counts(at) {
				break
			}
			scores[at] += 1 / float64(10+rank)
		}
	}
	join(func(a, b int) bool { return lexical[a] > lexical[b] }, func(at int) bool { return lexical[at] > 0 })
	join(func(a, b int) bool { return meaning[a] > meaning[b] }, func(at int) bool { return meaning[at] > 0 })
	sort.SliceStable(order, func(i, j int) bool { return scores[order[i]] > scores[order[j]] })
	var out []found
	for _, at := range order {
		if len(out) == limit || scores[at] == 0 {
			break
		}
		f := found{id: ix.entries[at].id, score: scores[at], close: meaning[at]}
		if whole > 0 {
			f.share = share[at] / whole
		}
		out = append(out, f)
	}
	return out
}
