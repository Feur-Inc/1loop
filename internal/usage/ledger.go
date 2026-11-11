// SPDX-License-Identifier: MPL-2.0

// Package usage keeps 1loop's own record of what every model call cost:
// a small append-only ledger of token counts, priced from a table shipped
// with 1loop (or the user's overrides), and the reports the Usage page
// shows. It holds numbers and identifiers only, never what was said, and
// lives beside the chats on this computer or on the server that ran them,
// so deleting or compacting a chat keeps its history of spend.
package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Record is one model call. Input counts the prompt tokens read fresh: the
// ones read from the provider's cache are CacheRead, the ones written to
// it CacheWrite. Reasoning is the part of Output spent thinking.
type Record struct {
	Time       time.Time `json:"t"`
	Connection string    `json:"conn,omitempty"` // the connection's id
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model"`
	Chat       string    `json:"chat,omitempty"`
	Parent     string    `json:"parent,omitempty"` // a subchat's parent chat
	Turn       string    `json:"turn,omitempty"`   // one run of the chat: a prompt and the work it caused
	Project    string    `json:"project,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Tokens
	// Cost is what the provider itself said the call cost (OpenRouter), in
	// US dollars; nil prices it from the table.
	Cost *float64 `json:"cost,omitempty"`
}

// Tokens are a call's token counts, as its provider reported them.
type Tokens struct {
	Input      int `json:"in,omitempty"`
	Output     int `json:"out,omitempty"`
	CacheRead  int `json:"cr,omitempty"`
	CacheWrite int `json:"cw,omitempty"`
	Reasoning  int `json:"rs,omitempty"`
}

// Total is every token the call read or wrote.
func (tokens Tokens) Total() int {
	return tokens.Input + tokens.Output + tokens.CacheRead + tokens.CacheWrite
}

func (tokens *Tokens) add(other Tokens) {
	tokens.Input += other.Input
	tokens.Output += other.Output
	tokens.CacheRead += other.CacheRead
	tokens.CacheWrite += other.CacheWrite
	tokens.Reasoning += other.Reasoning
}

// Ledger is the record of calls, one JSON line each in its file. Lines are
// only ever added.
type Ledger struct {
	mu      sync.Mutex
	path    string
	records []Record            // by time
	byChat  map[string][]Record // a chat's calls, and its subchats' under the parent's id with "/"
	added   []func(Record)
}

// Open reads the ledger at path; a missing file is an empty ledger, and a
// line that cannot be read (a write cut short) is skipped.
func Open(path string) *Ledger {
	ledger := &Ledger{path: path}
	file, err := os.Open(path)
	if err != nil {
		return ledger
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var record Record
		if json.Unmarshal(scanner.Bytes(), &record) == nil && !record.Time.IsZero() {
			ledger.records = append(ledger.records, record)
		}
	}
	sort.SliceStable(ledger.records, func(i, j int) bool { return ledger.records[i].Time.Before(ledger.records[j].Time) })
	for _, record := range ledger.records {
		ledger.index(record)
	}
	return ledger
}

// Path is the ledger's file.
func (ledger *Ledger) Path() string { return ledger.path }

// Add keeps a call. A call that used no tokens is not one.
func (ledger *Ledger) Add(record Record) {
	if ledger == nil || record.Total() == 0 {
		return
	}
	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	record.Time = record.Time.UTC().Truncate(time.Millisecond)
	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	ledger.mu.Lock()
	ledger.index(record)
	ledger.records = append(ledger.records, record)
	for i := len(ledger.records) - 1; i > 0 && ledger.records[i].Time.Before(ledger.records[i-1].Time); i-- {
		ledger.records[i], ledger.records[i-1] = ledger.records[i-1], ledger.records[i]
	}
	if ledger.path != "" && os.MkdirAll(filepath.Dir(ledger.path), 0700) == nil {
		if file, err := os.OpenFile(ledger.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
			file.Write(append(line, '\n'))
			file.Close()
		}
	}
	listeners := append([]func(Record){}, ledger.added...)
	ledger.mu.Unlock()
	for _, listener := range listeners {
		listener(record)
	}
}

// OnAdd calls listener after each call is kept (the tray's today, a
// chat's total).
func (ledger *Ledger) OnAdd(listener func(Record)) {
	ledger.mu.Lock()
	ledger.added = append(ledger.added, listener)
	ledger.mu.Unlock()
}

// Between returns the calls made from from (included) to to (excluded);
// a zero bound is open.
func (ledger *Ledger) Between(from, to time.Time) []Record {
	if ledger == nil {
		return nil
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	start := 0
	if !from.IsZero() {
		start = sort.Search(len(ledger.records), func(i int) bool { return !ledger.records[i].Time.Before(from) })
	}
	end := len(ledger.records)
	if !to.IsZero() {
		end = sort.Search(len(ledger.records), func(i int) bool { return !ledger.records[i].Time.Before(to) })
	}
	if start >= end {
		return nil
	}
	return append([]Record(nil), ledger.records[start:end]...)
}

func (ledger *Ledger) index(record Record) {
	if record.Chat == "" {
		return
	}
	if ledger.byChat == nil {
		ledger.byChat = map[string][]Record{}
	}
	ledger.byChat[record.Chat] = append(ledger.byChat[record.Chat], record)
	if record.Parent != "" {
		ledger.byChat[record.Parent+"/"] = append(ledger.byChat[record.Parent+"/"], record)
	}
}

// ChatSpend is what a chat spent since it began, and what its subchats did.
type ChatSpend struct {
	Totals
	Subchats *Totals `json:"subchats,omitempty"`
}

// ChatSpend adds a chat's calls up; nil for a chat that made none.
func (ledger *Ledger) ChatSpend(prices *Prices, chat string) *ChatSpend {
	if ledger == nil || chat == "" {
		return nil
	}
	ledger.mu.Lock()
	own, subchats := ledger.byChat[chat], ledger.byChat[chat+"/"]
	ledger.mu.Unlock()
	if len(own) == 0 && len(subchats) == 0 {
		return nil
	}
	spend := &ChatSpend{Totals: Sum(prices, own)}
	if len(subchats) > 0 {
		totals := Sum(prices, subchats)
		spend.Subchats = &totals
	}
	return spend
}

// Text is the chat's line under its context breakdown.
func (spend *ChatSpend) Text() string {
	if spend == nil {
		return ""
	}
	text := "Spent in this chat: " + amount(spend.Totals)
	if spend.Calls > 0 {
		text += fmt.Sprintf(" · %s tokens in %d model calls", Count(spend.Total()), spend.Calls)
	}
	if spend.Subchats != nil {
		text += " · its subchats: " + amount(*spend.Subchats)
	}
	return text
}

// amount is a cost, said to be API-equivalent when a plan paid for it, or
// the tokens when nothing could be priced.
func amount(totals Totals) string {
	if totals.Cost == 0 && totals.Unpriced > 0 {
		return Count(totals.Total()) + " tokens, no known price"
	}
	text := Money(totals.Cost)
	if totals.PlanCost > 0 && totals.PlanCost >= totals.Cost*0.99 {
		text += " API-equivalent"
	}
	return text
}
