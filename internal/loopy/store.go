// SPDX-License-Identifier: MPL-2.0

// Package loopy keeps what Loopy, 1loop's personal agent, holds apart from
// the projects: its one continuous conversation (text-message bubbles,
// reactions, cards), its memory of the user, the coding tasks it handed
// over and its routines. It is saved in loopy.json beside 1loop's state and
// knows nothing of the interface (internal/ui/loopy*.go) or of the agent
// (it stores the agent's history as raw JSON), so the server and the phone
// can share it later.
package loopy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Message is one bubble, card or note of the conversation.
type Message struct {
	ID   string    `json:"id"`
	Time time.Time `json:"t"`
	// From is "me" (the user) or "loopy".
	From string `json:"from"`
	Text string `json:"text,omitempty"`
	// Reaction is the emoji the other side put on this message.
	Reaction string `json:"reaction,omitempty"`
	// Card replaces the bubble: a mail draft, a choice, an approval…
	Card *Card `json:"card,omitempty"`
	// Memory is a note under Loopy's bubbles: something it remembered
	// (with its fact's id, for Forget).
	Memory   string `json:"memory,omitempty"`
	MemoryID string `json:"memoryId,omitempty"`
	// Note is a quiet line in the conversation: a compaction, a routine
	// that ran.
	Note string `json:"note,omitempty"`
	// Pending: the user's message waits for the run in progress.
	Pending bool `json:"pending,omitempty"`
}

// Card is something to act on, shown in the conversation and listed under
// "Waiting for you" while it waits.
type Card struct {
	// Kind is "mail", "message" (Slack…), "choice", "approval",
	// "needs_you", "summary", "delegate" or "routine".
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	Body     string   `json:"body,omitempty"`
	Options  []Option `json:"options,omitempty"`
	// Warning is said in red over the card: "This can't be undone".
	Warning string `json:"warning,omitempty"`
	// Actions are its buttons, the first one the main one.
	Actions []string `json:"actions,omitempty"`
	// Answer is the button the user pressed, or the option chosen; the card
	// stops waiting then.
	Answer string `json:"answer,omitempty"`
	// Approval is the agent's approval request the card answers.
	Approval string `json:"approval,omitempty"`
	// Task and Routine are the delegated task or routine it is about.
	Task    string `json:"task,omitempty"`
	Routine string `json:"routine,omitempty"`
}

// Field is a labelled value of a card (To, Subject…).
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Option is one choice of a choice card.
type Option struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Aside  string `json:"aside,omitempty"` // on the right: a price, a time
}

// Waiting says whether the card still waits for the user.
func (card *Card) Waiting() bool {
	return card != nil && card.Answer == "" && len(card.Actions)+len(card.Options) > 0 && card.Kind != "summary"
}

// Fact is one thing Loopy remembers about the user.
type Fact struct {
	ID   string    `json:"id"`
	Text string    `json:"text"`
	Time time.Time `json:"t"`
	// Category is "preference", "person", "place", "work", "routine" or
	// "other".
	Category string `json:"category,omitempty"`
	// Pinned facts are always in Loopy's prompt; the others, the most
	// recent first, while they fit.
	Pinned  bool      `json:"pinned,omitempty"`
	Updated time.Time `json:"updated,omitzero"`
}

// Categories of facts, in the order the memory page lists them.
var Categories = []string{"preference", "person", "place", "work", "routine", "other"}

// Entry is one line of Loopy's journal: what it did, dated.
type Entry struct {
	Time time.Time `json:"t"`
	Text string    `json:"text"`
}

// Task is a coding task Loopy handed to an agent in a project: a normal
// 1loop chat, which Loopy follows.
type Task struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Project string    `json:"project"`
	Brief   string    `json:"brief"`
	Chat    string    `json:"chat,omitempty"` // the project chat, once started
	Status  string    `json:"status"`         // "proposed", "running", "done", "failed", "cancelled"
	Steps   []string  `json:"steps,omitempty"`
	Result  string    `json:"result,omitempty"`
	Time    time.Time `json:"t"`
}

// Routine is a task Loopy runs on a schedule: "daily 08:00", "weekdays
// 09:30", "every 2h", "every 30m".
type Routine struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	When    string    `json:"when"`
	Task    string    `json:"task"`
	On      bool      `json:"on"`
	LastRun time.Time `json:"lastRun,omitzero"`
	Runs    int       `json:"runs,omitempty"`
}

// Settings are Loopy's own choices.
type Settings struct {
	Connection string `json:"connection,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	// Summary is when the morning summary comes, "08:00", "" for never.
	Summary     string    `json:"summary,omitempty"`
	LastSummary time.Time `json:"lastSummary,omitzero"`
	// Shown: the Loopy space was on screen when 1loop closed.
	Shown bool `json:"shown,omitempty"`
	// AskToRemember: Loopy remembers only what the user asks it to; else it
	// also keeps what clearly matters, and says so.
	AskToRemember bool `json:"askToRemember,omitempty"`
	// JournalOff: Loopy keeps no journal of what it did.
	JournalOff bool `json:"journalOff,omitempty"`
}

// Store is Loopy's saved state.
type Store struct {
	mu       sync.Mutex
	path     string
	Messages []Message       `json:"messages"`
	Memory   []Fact          `json:"memory"`
	Tasks    []Task          `json:"tasks"`
	Routines []Routine       `json:"routines"`
	Settings Settings        `json:"settings"`
	Journal  []Entry         `json:"journal,omitempty"`
	History  json.RawMessage `json:"history,omitempty"` // the agent's own conversation
}

// Open reads Loopy's state at path; a missing file is a fresh Loopy.
func Open(path string) *Store {
	store := &Store{path: path}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, store)
	}
	store.path = path
	return store
}

// Save writes the state, whole or not at all.
func (store *Store) Save() error {
	store.mu.Lock()
	data, err := json.MarshalIndent(store, "", "  ")
	path := store.path
	store.mu.Unlock()
	if err != nil || path == "" {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// Lock and Unlock guard the fields for a caller that changes several.
func (store *Store) Lock()   { store.mu.Lock() }
func (store *Store) Unlock() { store.mu.Unlock() }

// NewID is a short random identifier.
func NewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Add appends a message, giving it an id and a time.
func (store *Store) Add(message Message) Message {
	if message.ID == "" {
		message.ID = NewID()
	}
	if message.Time.IsZero() {
		message.Time = time.Now()
	}
	store.mu.Lock()
	store.Messages = append(store.Messages, message)
	store.mu.Unlock()
	return message
}

// Update changes the message with id; false when there is none.
func (store *Store) Update(id string, change func(*Message)) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.Messages {
		if store.Messages[i].ID == id {
			change(&store.Messages[i])
			return true
		}
	}
	return false
}

// Snapshot is a copy of the messages.
func (store *Store) Snapshot() []Message {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Message(nil), store.Messages...)
}

// LastFrom is the latest message from "me" or "loopy".
func (store *Store) LastFrom(from string) (Message, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := len(store.Messages) - 1; i >= 0; i-- {
		if store.Messages[i].From == from && store.Messages[i].Card == nil && store.Messages[i].Memory == "" {
			return store.Messages[i], true
		}
	}
	return Message{}, false
}

// Waiting are the cards still waiting for the user, latest first.
func (store *Store) Waiting() []Message {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []Message
	for i := len(store.Messages) - 1; i >= 0; i-- {
		if store.Messages[i].Card.Waiting() {
			out = append(out, store.Messages[i])
		}
	}
	return out
}

// Remember keeps a fact; one already known is not kept twice.
func (store *Store) Remember(text, category string) Fact {
	text = strings.TrimSpace(text)
	if !validCategory(category) {
		category = "other"
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, fact := range store.Memory {
		if strings.EqualFold(fact.Text, text) {
			return fact
		}
	}
	fact := Fact{ID: NewID(), Text: text, Time: time.Now(), Category: category}
	store.Memory = append(store.Memory, fact)
	return fact
}

func validCategory(category string) bool {
	for _, c := range Categories {
		if c == category {
			return true
		}
	}
	return false
}

// UpdateFact changes a fact's text, category or pin; false when there is
// no fact with id.
func (store *Store) UpdateFact(id string, change func(*Fact)) (Fact, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.Memory {
		if store.Memory[i].ID == id {
			change(&store.Memory[i])
			if !validCategory(store.Memory[i].Category) {
				store.Memory[i].Category = "other"
			}
			store.Memory[i].Updated = time.Now()
			return store.Memory[i], true
		}
	}
	return Fact{}, false
}

// Log adds a line to the journal, unless it is off; it keeps the last
// thousand.
func (store *Store) Log(text string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.Settings.JournalOff || strings.TrimSpace(text) == "" {
		return
	}
	store.Journal = append(store.Journal, Entry{Time: time.Now(), Text: strings.TrimSpace(text)})
	if len(store.Journal) > 1000 {
		store.Journal = store.Journal[len(store.Journal)-1000:]
	}
}

// Entries is a copy of the journal, oldest first.
func (store *Store) Entries() []Entry {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Entry(nil), store.Journal...)
}

// Forget removes the fact with id, or the facts whose text holds what (case
// aside); it returns the facts forgotten.
func (store *Store) Forget(id, what string) []Fact {
	store.mu.Lock()
	defer store.mu.Unlock()
	var kept, gone []Fact
	what = strings.ToLower(strings.TrimSpace(what))
	for _, fact := range store.Memory {
		if (id != "" && fact.ID == id) || (what != "" && strings.Contains(strings.ToLower(fact.Text), what)) {
			gone = append(gone, fact)
		} else {
			kept = append(kept, fact)
		}
	}
	store.Memory = kept
	return gone
}

// Facts is a copy of the memory, oldest first.
func (store *Store) Facts() []Fact {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Fact(nil), store.Memory...)
}

// AddTask keeps a task and returns it with its id.
func (store *Store) AddTask(task Task) Task {
	task.ID, task.Time = NewID(), time.Now()
	store.mu.Lock()
	store.Tasks = append(store.Tasks, task)
	store.mu.Unlock()
	return task
}

// UpdateTask changes the task with id.
func (store *Store) UpdateTask(id string, change func(*Task)) (Task, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.Tasks {
		if store.Tasks[i].ID == id {
			change(&store.Tasks[i])
			return store.Tasks[i], true
		}
	}
	return Task{}, false
}

// TaskList is a copy of the tasks, latest first.
func (store *Store) TaskList() []Task {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := append([]Task(nil), store.Tasks...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// AddRoutine keeps a routine, off until the user turns it on.
func (store *Store) AddRoutine(routine Routine) Routine {
	routine.ID = NewID()
	store.mu.Lock()
	store.Routines = append(store.Routines, routine)
	store.mu.Unlock()
	return routine
}

// UpdateRoutine changes the routine with id.
func (store *Store) UpdateRoutine(id string, change func(*Routine)) (Routine, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.Routines {
		if store.Routines[i].ID == id {
			change(&store.Routines[i])
			return store.Routines[i], true
		}
	}
	return Routine{}, false
}

// RoutineList is a copy of the routines.
func (store *Store) RoutineList() []Routine {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Routine(nil), store.Routines...)
}

// RemoveRoutine deletes the routine with id.
func (store *Store) RemoveRoutine(id string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	kept := store.Routines[:0]
	for _, routine := range store.Routines {
		if routine.ID != id {
			kept = append(kept, routine)
		}
	}
	store.Routines = kept
}
