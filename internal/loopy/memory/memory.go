// SPDX-License-Identifier: MPL-2.0

//go:build !js

// Package memory is Loopy's memory of its user. What it is built on, and
// the measures behind it, are in docs/LOOPY.md:
//   - every exchange is kept as it was said, dated (records.jsonl): nothing
//     is only a summary of what was said;
//   - facts are drawn from what the user wrote, each with the words it
//     comes from, and a fact that changed is ended, not overwritten;
//   - a short profile says how the user writes and wants to be answered;
//   - search is by words and by meaning together (index.go).
//
// Only the user's own words make facts: a page, a mail or a tool's answer
// never does, so nobody else writes in this memory.
package memory

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is one exchange (or event) as it happened.
type Record struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"t"`
	User  string    `json:"user,omitempty"`  // what the user wrote
	Loopy string    `json:"loopy,omitempty"` // what Loopy answered
	Event string    `json:"event,omitempty"` // what Loopy did: a task, a routine, a card answered
	// Draft and Final are a text Loopy proposed and what the user made of it.
	Draft string `json:"draft,omitempty"`
	Final string `json:"final,omitempty"`
	// Gone: the record with this id was forgotten (a line of the log).
	Gone bool `json:"gone,omitempty"`
}

// Kinds of facts.
const (
	Instruction = "instruction" // how Loopy must act: "never book before 10 am"
	Style       = "style"       // how the user talks and wants to be answered
	Preference  = "preference"
	Habit       = "habit"
	Person      = "person"
	Place       = "place"
	Work        = "work"
	Goal        = "goal"
	Other       = "other"
)

// Kinds in the order the memory page lists them.
var Kinds = []string{Instruction, Style, Preference, Habit, Person, Place, Work, Goal, Other}

// Fact is one thing known of the user.
type Fact struct {
	ID    string    `json:"id"`
	Text  string    `json:"text"`
	Kind  string    `json:"kind"`
	Since time.Time `json:"since"`
	Seen  time.Time `json:"seen"` // last confirmed
	// Until is set once the fact is no longer true; By is the fact that
	// replaced it.
	Until time.Time `json:"until,omitzero"`
	By    string    `json:"by,omitempty"`
	// Said: the user's words say it outright. Otherwise it was inferred,
	// Times times.
	Said   bool `json:"said,omitempty"`
	Times  int  `json:"times,omitempty"`
	Pinned bool `json:"pinned,omitempty"`
	// Quote is the user's words it comes from, in the record From.
	Quote string `json:"quote,omitempty"`
	From  string `json:"from,omitempty"`
}

// Current says whether the fact still holds.
func (fact Fact) Current() bool { return fact.Until.IsZero() }

// Sure says whether the fact may steer Loopy on its own: said by the user,
// pinned, or seen more than once.
func (fact Fact) Sure() bool { return fact.Said || fact.Pinned || fact.Times >= 2 }

type saved struct {
	Facts   []Fact `json:"facts"`
	Learned string `json:"learned,omitempty"` // the last record facts were drawn from
	Style   Habits `json:"style"`
	// Tombs are the texts of facts the user forgot: they are not noticed again.
	Tombs []string `json:"tombs,omitempty"`
}

// Memory is a user's memory, in a folder of its own.
type Memory struct {
	mu      sync.Mutex
	dir     string
	records []Record
	byID    map[string]int
	state   saved
	said    *index // the records
	known   *index // the facts
}

var now = time.Now // tests set it

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Open reads the memory kept in dir ("" keeps it in memory only).
func Open(dir string) (*Memory, error) {
	m := &Memory{dir: dir, byID: map[string]int{}, said: newIndex(), known: newIndex()}
	if dir == "" {
		return m, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(m.path("facts.json")); err == nil {
		if err := json.Unmarshal(data, &m.state); err != nil {
			return nil, fmt.Errorf("Loopy's facts could not be read: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.Open(m.path("records.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		defer file.Close()
		lines := bufio.NewReaderSize(file, 1<<16)
		for {
			line, readErr := lines.ReadBytes('\n')
			var record Record
			if json.Unmarshal(line, &record) == nil && record.ID != "" { // a line cut by a crash is skipped
				m.put(record)
			}
			if readErr != nil {
				break
			}
		}
	}
	for _, record := range m.records {
		m.index(record)
	}
	for _, fact := range m.state.Facts {
		m.known.set(fact.ID, fact.Text)
	}
	return m, nil
}

func (m *Memory) path(name string) string { return filepath.Join(m.dir, name) }

// put adds, replaces or removes a record of the list.
func (m *Memory) put(record Record) {
	at, ok := m.byID[record.ID]
	switch {
	case record.Gone:
		if ok {
			m.records[at] = Record{ID: record.ID, Gone: true}
			m.said.remove(record.ID)
		}
	case ok:
		m.records[at] = record
	default:
		m.byID[record.ID] = len(m.records)
		m.records = append(m.records, record)
	}
}

const answerKey = 4000 // of Loopy's answer, what search reads

// index gives a record its keys: what was said, and the facts drawn from
// it (LongMemEval: keys with the facts find more than the words alone).
func (m *Memory) index(record Record) {
	if record.Gone {
		return
	}
	keys := []string{record.User + record.Event, clip(record.Loopy, answerKey)}
	if record.Final != "" {
		keys = append(keys, record.Final)
	}
	var facts []string
	for _, fact := range m.state.Facts {
		if fact.From == record.ID {
			facts = append(facts, fact.Text)
		}
	}
	if len(facts) > 0 {
		keys = append(keys, strings.Join(facts, ". "))
	}
	m.said.set(record.ID, keys...)
}

// Add keeps a record, giving it an id and a time.
func (m *Memory) Add(record Record) (Record, error) {
	if record.ID == "" {
		record.ID = newID()
	}
	if record.Time.IsZero() {
		record.Time = now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.put(record)
	m.index(record)
	if record.User != "" {
		m.state.Style.note(record.User, record.Time)
	}
	return record, m.log(record)
}

// Amend changes a record (Loopy's answer, once it is whole).
func (m *Memory) Amend(id string, change func(*Record)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.byID[id]
	if !ok || m.records[at].Gone {
		return errors.New("no such record")
	}
	record := m.records[at]
	change(&record)
	m.put(record)
	m.index(record)
	return m.log(record)
}

// log appends a line; the last line of an id is the one that counts.
func (m *Memory) log(record Record) error {
	if m.dir == "" {
		return nil
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(m.path("records.jsonl"), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	// A crash may have cut the last line: this one starts on its own.
	if end, _ := file.Seek(0, 2); end > 0 {
		last := make([]byte, 1)
		if _, err := file.ReadAt(last, end-1); err == nil && last[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err = file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// save writes the facts, whole or not at all.
func (m *Memory) save() error {
	if m.dir == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.state, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.path("facts.json.tmp"), data, 0600); err != nil {
		return err
	}
	return os.Rename(m.path("facts.json.tmp"), m.path("facts.json"))
}

// Save writes what Add only counted (the habits of writing).
func (m *Memory) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.save()
}

func validKind(kind string) string {
	if slices.Contains(Kinds, kind) {
		return kind
	}
	return Other
}

// Remember keeps a fact. One that says the same is confirmed instead
// (its id comes back); one it corrects is given as replaces, and ends.
func (m *Memory) Remember(fact Fact, replaces string) (Fact, error) {
	fact.Text = strings.TrimSpace(fact.Text)
	if fact.Text == "" {
		return Fact{}, errors.New("no fact given")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fact = m.remember(fact, replaces)
	return fact, m.save()
}

func (m *Memory) remember(fact Fact, replaces string) Fact {
	fact.Kind = validKind(fact.Kind)
	if at := m.same(fact.Text, replaces); at >= 0 {
		known := &m.state.Facts[at]
		known.Seen, known.Times = now(), known.Times+1
		known.Said = known.Said || fact.Said
		known.Pinned = known.Pinned || fact.Pinned
		return *known
	}
	fact.ID, fact.Times = newID(), max(1, fact.Times)
	if fact.Since.IsZero() {
		fact.Since = now()
	}
	fact.Seen = now()
	if replaces != "" {
		for i := range m.state.Facts {
			if old := &m.state.Facts[i]; old.ID == replaces && old.Current() {
				old.Until, old.By = fact.Since, fact.ID
				fact.Pinned = fact.Pinned || old.Pinned
			}
		}
	}
	m.state.Facts = append(m.state.Facts, fact)
	m.known.set(fact.ID, fact.Text)
	if at, ok := m.byID[fact.From]; ok {
		m.index(m.records[at])
	}
	return fact
}

// same finds a current fact that says the same words, other than except.
func (m *Memory) same(text, except string) int {
	key := strings.Join(words(text), " ")
	for i, fact := range m.state.Facts {
		if fact.Current() && fact.ID != except && (strings.EqualFold(fact.Text, text) || key != "" && strings.Join(words(fact.Text), " ") == key) {
			return i
		}
	}
	return -1
}

// Change edits a fact: its text, kind or pin.
func (m *Memory) Change(id string, change func(*Fact)) (Fact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Facts {
		if m.state.Facts[i].ID == id {
			change(&m.state.Facts[i])
			m.state.Facts[i].Kind = validKind(m.state.Facts[i].Kind)
			m.known.set(id, m.state.Facts[i].Text)
			return m.state.Facts[i], m.save()
		}
	}
	return Fact{}, fmt.Errorf("no fact has the id %s", id)
}

// Forget removes facts for good: by id, or those holding all the words.
// They are not noticed again. withRecord also removes the exchange each
// comes from.
func (m *Memory) Forget(id, holding string, withRecord bool) ([]Fact, error) {
	holding = plain(strings.TrimSpace(holding))
	m.mu.Lock()
	defer m.mu.Unlock()
	var kept, gone []Fact
	for _, fact := range m.state.Facts {
		if id != "" && fact.ID == id || holding != "" && strings.Contains(plain(fact.Text), holding) {
			gone = append(gone, fact)
			m.known.remove(fact.ID)
			m.state.Tombs = append(m.state.Tombs, fact.Text)
		} else {
			kept = append(kept, fact)
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	m.state.Facts = kept
	var err error
	for _, fact := range gone {
		if at, ok := m.byID[fact.From]; ok && withRecord {
			err = errors.Join(err, m.erase(m.records[at].ID))
		} else if ok {
			m.index(m.records[at]) // its key no longer holds the fact
		}
	}
	return gone, errors.Join(err, m.save())
}

// Erase removes an exchange for good: the log is rewritten without it.
func (m *Memory) Erase(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.erase(id)
}

func (m *Memory) erase(id string) error {
	if _, ok := m.byID[id]; !ok {
		return errors.New("no such record")
	}
	m.put(Record{ID: id, Gone: true})
	if m.dir == "" {
		return nil
	}
	var b strings.Builder
	for _, record := range m.records {
		if !record.Gone {
			line, err := json.Marshal(record)
			if err != nil {
				return err
			}
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	if err := os.WriteFile(m.path("records.jsonl.tmp"), []byte(b.String()), 0600); err != nil {
		return err
	}
	return os.Rename(m.path("records.jsonl.tmp"), m.path("records.jsonl"))
}

// Facts are the facts that still hold, or all of them with their history.
func (m *Memory) Facts(all bool) []Fact {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Fact
	for _, fact := range m.state.Facts {
		if all || fact.Current() {
			out = append(out, fact)
		}
	}
	return out
}

// Habits are the user's ways of writing, counted.
func (m *Memory) Habits() Habits {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Style.copy()
}

// Records are the last n exchanges, oldest first.
func (m *Memory) Records(n int) []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for i := len(m.records) - 1; i >= 0 && len(out) < n; i-- {
		if !m.records[i].Gone {
			out = append(out, m.records[i])
		}
	}
	slices.Reverse(out)
	return out
}

// Hit is an exchange found.
type Hit struct {
	Record Record
	// Strong: the exchange clearly is about the question, good enough to
	// bring up unasked.
	Strong bool
	score  float64
}

// Query is what to search the exchanges for.
type Query struct {
	Text  string // words; the moments it names ("last week") lift what was said then
	Since time.Time
	Until time.Time
	Limit int
	Skip  map[string]bool // records already seen
}

// Search finds exchanges, the best first.
func (m *Memory) Search(query Query) []Hit {
	if query.Limit <= 0 {
		query.Limit = 12
	}
	span, named := Moment(query.Text, now())
	m.mu.Lock()
	defer m.mu.Unlock()
	in := func(record Record) bool {
		return !record.Gone && !query.Skip[record.ID] &&
			(query.Since.IsZero() || !record.Time.Before(query.Since)) && (query.Until.IsZero() || record.Time.Before(query.Until))
	}
	var hits []Hit
	for _, f := range m.said.search(query.Text, query.Limit*3, func(id string) bool { return in(m.records[m.byID[id]]) }) {
		record := m.records[m.byID[f.id]]
		hit := Hit{Record: record, score: f.score, Strong: f.close >= strongMeaning || f.share >= strongWords}
		if named && span.Holds(record.Time) {
			hit.score *= 1.5
		}
		hits = append(hits, hit)
	}
	if len(hits) == 0 && (named || !query.Since.IsZero() || !query.Until.IsZero()) { // "what did we do last week?"
		for i := len(m.records) - 1; i >= 0 && len(hits) < query.Limit; i-- {
			if record := m.records[i]; in(record) && (!named || span.Holds(record.Time)) {
				hits = append(hits, Hit{Record: record, score: 1})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	return hits[:min(len(hits), query.Limit)]
}

// Above these, an exchange is about the question (docs/LOOPY.md).
const (
	strongMeaning = .55
	strongWords   = .50
)

// Near are the current facts closest to a text, with how close.
func (m *Memory) Near(text string, limit int) []Fact {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.near(text, limit, false)
}

func (m *Memory) near(text string, limit int, strong bool) []Fact {
	byID := map[string]Fact{}
	for _, fact := range m.state.Facts {
		if fact.Current() {
			byID[fact.ID] = fact
		}
	}
	var out []Fact
	for _, f := range m.known.search(text, limit, func(id string) bool { _, ok := byID[id]; return ok }) {
		if strong && f.close < nearMeaning && f.share < nearWords {
			continue
		}
		out = append(out, byID[f.id])
	}
	return out
}

// Above these a fact has to do with a message: below, it stays out of the
// prompt (a memory brought up out of place makes answers worse).
const (
	nearMeaning = .42
	nearWords   = .25
)
