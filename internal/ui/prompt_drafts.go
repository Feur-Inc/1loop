// SPDX-License-Identifier: MPL-2.0

package ui

// What the user types is never lost: each conversation keeps its own draft,
// written to disk as it is typed (a crash or closing 1loop loses nothing),
// and its own undo history, kept while 1loop runs.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// promptDrafts are the drafts, by key: a chat ID, or newChatDraft + its
// project for the start screen. Each is a file of its own in dir, written
// as it is typed: an edit rewrites its draft only, not every draft.
type promptDrafts struct {
	dir   string
	mu    sync.Mutex
	texts map[string]string
}

const newChatDraft = "new:"

// savedDraft is a draft's file.
type savedDraft struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// loadPromptDrafts reads the drafts of the folder path names without its
// .json, and those of the single file path that earlier versions wrote.
func loadPromptDrafts(path string) *promptDrafts {
	drafts := &promptDrafts{dir: strings.TrimSuffix(path, ".json"), texts: map[string]string{}}
	changed := map[string]bool{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &drafts.texts)
		for key := range drafts.texts {
			changed[key] = true // into a file of its own
		}
	}
	entries, _ := os.ReadDir(drafts.dir)
	for _, entry := range entries {
		var draft savedDraft
		if data, err := os.ReadFile(filepath.Join(drafts.dir, entry.Name())); err == nil && json.Unmarshal(data, &draft) == nil && draft.Key != "" {
			drafts.texts[draft.Key] = draft.Text
			delete(changed, draft.Key)
		}
	}
	// An interrupted remote send is offered again rather than discarded.
	for key, text := range drafts.texts {
		if strings.HasPrefix(key, "pending:") || strings.HasPrefix(key, "sending:") {
			destination := strings.TrimPrefix(key, "pending:")
			if strings.HasPrefix(key, "sending:") {
				_, destination, _ = strings.Cut(key, "|")
			}
			if current := drafts.texts[destination]; current != text {
				if current != "" {
					text = current + "\n\n" + text
				}
				drafts.texts[destination] = text
				changed[destination] = true
			}
			delete(drafts.texts, key)
			changed[key] = true
		}
	}
	saved := true
	for key := range changed {
		saved = drafts.writeLocked(key) && saved
	}
	if _, err := os.Stat(path); err == nil && saved {
		os.Remove(path) // every draft it held now has its own file
	}
	return drafts
}

func (drafts *promptDrafts) get(key string) string {
	drafts.mu.Lock()
	defer drafts.mu.Unlock()
	return drafts.texts[key]
}

// set writes each edit before returning; a sudden process exit cannot lose a debounce window.
func (drafts *promptDrafts) set(key, text string) {
	drafts.mu.Lock()
	defer drafts.mu.Unlock()
	if drafts.texts[key] == text {
		return
	}
	if text == "" {
		delete(drafts.texts, key)
	} else {
		drafts.texts[key] = text
	}
	drafts.writeLocked(key)
}

// move hands a draft to another key: the start screen's draft becomes the
// new chat's.
func (drafts *promptDrafts) move(from, to string) {
	drafts.mu.Lock()
	text, ok := drafts.texts[from]
	drafts.mu.Unlock()
	if ok {
		drafts.set(from, "")
		drafts.set(to, text)
	}
}

// writeLocked writes one draft's file atomically (an interrupted write
// never leaves half a file), or removes it once the draft is empty.
func (drafts *promptDrafts) writeLocked(key string) bool {
	if drafts.dir == "" {
		return false
	}
	sum := sha256.Sum256([]byte(key))
	path := filepath.Join(drafts.dir, hex.EncodeToString(sum[:16])+".json")
	text, ok := drafts.texts[key]
	if !ok {
		err := os.Remove(path)
		return err == nil || os.IsNotExist(err)
	}
	if os.MkdirAll(drafts.dir, 0700) != nil {
		return false
	}
	data, _ := json.Marshal(savedDraft{key, text})
	temporary := path + ".tmp"
	return os.WriteFile(temporary, data, 0600) == nil && os.Rename(temporary, path) == nil
}

// promptHistory is one draft's undo and redo, as successive texts. Typing
// in one burst is a single step, as in an editor.
type promptHistory struct {
	undo, redo []string
	last       time.Time
}

const promptUndoLimit = 200

func (history *promptHistory) record(before string) {
	now := time.Now()
	if len(history.undo) > 0 && now.Sub(history.last) < 700*time.Millisecond {
		history.last = now // the same burst of typing
		history.redo = nil
		return
	}
	history.last = now
	history.undo = append(history.undo, before)
	if len(history.undo) > promptUndoLimit {
		history.undo = history.undo[1:]
	}
	history.redo = nil
}

// bindDrafts attaches the entry to drafts: what is typed is saved under the
// current key, and Ctrl+Z / Ctrl+Y (or Ctrl+Shift+Z) walk its history.
func (entry *chatEntry) bindDrafts(drafts *promptDrafts) {
	entry.drafts = drafts
	entry.histories = map[string]*promptHistory{}
}

// renameDraft gives the shown draft and its history a new key, when the
// start screen's conversation gets its chat ID.
func (entry *chatEntry) renameDraft(key string) {
	if entry.drafts == nil || key == entry.draftKey {
		return
	}
	entry.drafts.move(entry.draftKey, key)
	if history := entry.histories[entry.draftKey]; history != nil {
		entry.histories[key] = history
		delete(entry.histories, entry.draftKey)
	}
	entry.draftKey = key
}

// switchDraft shows the draft of another conversation, its history intact.
func (entry *chatEntry) switchDraft(key string) {
	if entry.drafts == nil || (key == entry.draftKey && entry.draftBound) {
		return
	}
	entry.draftKey, entry.draftBound = key, true
	if draft := entry.drafts.get(key); entry.replace(draft) != draft {
		entry.drafts.set(key, "") // attached instead: restored once
	}
}

// replace sets the text without recording an undo step or a draft change,
// and returns it: a text too long for the prompt is attached instead.
func (entry *chatEntry) replace(text string) string {
	if entry.oversize != nil && textTooBig(text) {
		entry.oversize(text)
		text = ""
	}
	entry.restoring = true
	entry.SetText(text)
	entry.CursorRow = len(entry.textRows()) - 1
	entry.CursorColumn = len([]rune(entry.textRows()[entry.CursorRow]))
	entry.Refresh()
	entry.restoring = false
	entry.previous = text
	return text
}

func (entry *chatEntry) textRows() []string {
	return strings.Split(entry.Text, "\n")
}

func (entry *chatEntry) history() *promptHistory {
	history := entry.histories[entry.draftKey]
	if history == nil {
		history = &promptHistory{}
		entry.histories[entry.draftKey] = history
	}
	return history
}

// changed runs on every change of the text: typed ones become undo steps
// and are saved; replaced ones only move the reference.
func (entry *chatEntry) changed(text string) {
	if !entry.restoring && entry.oversize != nil && textTooBig(text) {
		// Typed or pasted past the limit: the text becomes an attachment
		// and the prompt starts again, before it grows any slower.
		if entry.drafts != nil {
			entry.history().record(entry.previous)
		}
		entry.replace("")
		entry.oversize(text)
		if entry.drafts != nil {
			entry.drafts.set(entry.draftKey, "")
		}
		return
	}
	if entry.drafts == nil {
		return
	}
	if !entry.restoring {
		if text != entry.previous {
			entry.history().record(entry.previous)
		}
		entry.drafts.set(entry.draftKey, text)
	}
	entry.previous = text
}

// clearSent empties the prompt once its message is on its way; Ctrl+Z
// still brings the text back.
func (entry *chatEntry) clearSent() {
	if entry.drafts == nil {
		entry.SetText("")
		return
	}
	entry.history().record(entry.Text)
	entry.history().last = time.Time{}
	entry.replace("")
	entry.drafts.set(entry.draftKey, "")
}

// restoreDraft puts back a text the user wrote (a refused send, a queued
// message taken back for editing) as a step that can be undone.
func (entry *chatEntry) restoreDraft(text string) {
	if entry.drafts == nil {
		entry.SetText(text)
		return
	}
	if current := entry.Text; current != "" && current != text {
		text = current + "\n\n" + text
	}
	entry.history().record(entry.Text)
	entry.history().last = time.Time{}
	entry.drafts.set(entry.draftKey, entry.replace(text))
}

func (entry *chatEntry) undo() {
	history := entry.history()
	if len(history.undo) == 0 {
		return
	}
	text := history.undo[len(history.undo)-1]
	history.undo = history.undo[:len(history.undo)-1]
	history.redo = append(history.redo, entry.Text)
	history.last = time.Time{}
	entry.replace(text)
	entry.drafts.set(entry.draftKey, text)
}

func (entry *chatEntry) redo() {
	history := entry.history()
	if len(history.redo) == 0 {
		return
	}
	text := history.redo[len(history.redo)-1]
	history.redo = history.redo[:len(history.redo)-1]
	history.undo = append(history.undo, entry.Text)
	history.last = time.Time{}
	entry.replace(text)
	entry.drafts.set(entry.draftKey, text)
}

// historyShortcut handles undo and redo for the drafts' own history.
func (entry *chatEntry) historyShortcut(s fyne.Shortcut) bool {
	if entry.drafts == nil {
		return false
	}
	switch shortcut := s.(type) {
	case *fyne.ShortcutUndo:
		entry.undo()
		return true
	case *fyne.ShortcutRedo:
		entry.redo()
		return true
	case *desktop.CustomShortcut:
		if shortcut.KeyName == fyne.KeyZ && shortcut.Modifier == fyne.KeyModifierShortcutDefault|fyne.KeyModifierShift {
			entry.redo()
			return true
		}
	}
	return false
}
