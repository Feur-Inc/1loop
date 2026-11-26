// SPDX-License-Identifier: MPL-2.0

package loopy

import "time"

// AutoLink is a script of Loopy's own (Python, the browser included) that
// reaches a service or does a job for it, without the model: run by a tool,
// by the user, or by itself.
type AutoLink struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	About string `json:"about"`           // what it does
	Input string `json:"input,omitempty"` // what it takes, in words
	// What it may do, as the user allowed it.
	Sites     []string `json:"sites,omitempty"`     // "api.example.com"; "*" for any
	Secrets   []string `json:"secrets,omitempty"`   // names of the vault's entries
	Rights    []string `json:"rights,omitempty"`    // LinkRights
	Libraries []string `json:"libraries,omitempty"` // PyPI's
	// When it runs by itself ("every 5m", "daily 08:00"), "" never.
	When string `json:"when,omitempty"`
	// Do is what Loopy does when the script wakes it.
	Do string `json:"do,omitempty"`
	// Ask: the user confirms every run; else it runs by itself.
	Ask bool `json:"ask,omitempty"`
	// Allowed once the user said yes to it; On: its schedule runs.
	Allowed bool      `json:"allowed"`
	On      bool      `json:"on"`
	Running bool      `json:"running,omitempty"`
	Waits   string    `json:"waits,omitempty"` // what its next run waits for: "the vault", "your answer"
	Runs    []LinkRun `json:"runs,omitempty"`  // the latest last
	Failed  int       `json:"failed,omitempty"`
	LastRun time.Time `json:"lastRun,omitzero"`
	Changed time.Time `json:"changed,omitzero"`
	// Next is a change that waits for the user's yes.
	Next *AutoLink `json:"next,omitempty"`
	// Seen are the keys it woke Loopy or started an agent with.
	Seen []string `json:"seen,omitempty"`
}

// LinkRun is a time an AutoLink ran.
type LinkRun struct {
	Time    time.Time `json:"t"`
	Why     string    `json:"why"` // "Loopy", "schedule", "you", "test"
	Took    int64     `json:"ms,omitempty"`
	Error   string    `json:"error,omitempty"`
	Outcome string    `json:"outcome,omitempty"`
	Files   []string  `json:"files,omitempty"` // in Loopy's folder
}

// LinkRights are what an AutoLink may do beyond its sites, and how a card
// says it.
var LinkRights = []struct{ Name, Says string }{
	{"github", "Read GitHub with your connector"},
	{"github_write", "Write on GitHub in your name"},
	{"agent", "Start coding agents in your projects"},
	{"act", "Change things on its sites (send, book, buy…)"},
}

// LinkCode is an AutoLink's script, and the one that waits for a yes.
type LinkCode struct {
	Code string `json:"code"`
	Next string `json:"next,omitempty"`
}

// Chat is one of 1loop's, for Loopy to name.
type Chat struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Project string    `json:"project,omitempty"`
	Running bool      `json:"running,omitempty"`
	Time    time.Time `json:"t,omitzero"`
}
