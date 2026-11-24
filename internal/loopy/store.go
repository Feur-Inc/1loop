// SPDX-License-Identifier: MPL-2.0

package loopy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is what Loopy keeps besides its memory and its folder.
type State struct {
	Messages []Message       `json:"messages"`
	Tasks    []Task          `json:"tasks"`
	Routines []Routine       `json:"routines"`
	Rules    []Rule          `json:"rules,omitempty"`
	Accounts []Account       `json:"accounts,omitempty"`
	Settings Settings        `json:"settings"`
	Journal  []Entry         `json:"journal,omitempty"`
	History  json.RawMessage `json:"history,omitempty"` // the agent's own conversation
	Records  map[string]bool `json:"records,omitempty"` // memory records the history still holds
	Session  string          `json:"session,omitempty"` // Claude Code's session
	SeenBy   string          `json:"seenBy,omitempty"`  // the model the history ran with
	// Memory is the facts of earlier versions, moved to the memory.
	Memory []struct {
		Text, Category string
		Pinned         bool
		Time           time.Time `json:"t"`
	} `json:"memory,omitempty"`
}

// Store keeps the state in a file.
type Store struct {
	mu      sync.Mutex
	saving  sync.Mutex // one save at a time: they share the temporary file
	path    string
	blocked bool // the file could not be read: never write over it
	State
}

// Open reads Loopy's state at path; a missing file is a fresh Loopy.
func Open(path string) (*Store, error) {
	store := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || path == "" {
		return store, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &store.State)
	}
	if err != nil {
		store.blocked = true
		return store, fmt.Errorf("Loopy's conversation could not be read: %w", err)
	}
	return store, nil
}

// Keep is how many messages the conversation keeps; the memory keeps every
// exchange.
const Keep = 2000

// Save writes the state, whole or not at all.
func (store *Store) Save() error {
	store.saving.Lock()
	defer store.saving.Unlock()
	store.mu.Lock()
	if n := len(store.Messages); n > Keep {
		store.Messages = append([]Message(nil), store.Messages[n-Keep:]...)
	}
	data, err := json.Marshal(store.State)
	path, blocked := store.path, store.blocked
	store.mu.Unlock()
	if err != nil || path == "" || blocked {
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

// Lock and Unlock guard the state.
func (store *Store) Lock()   { store.mu.Lock() }
func (store *Store) Unlock() { store.mu.Unlock() }

// NewID is a short random identifier.
func NewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
