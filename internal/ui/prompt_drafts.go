package ui

// What the user types is never lost: each conversation keeps its own draft,
// written to disk as it is typed (a crash or closing 1loop loses nothing),
// and its own undo history, kept while 1loop runs.

import (
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
// project for the start screen.
type promptDrafts struct {
	path  string
	mu    sync.Mutex
	texts map[string]string
}

const newChatDraft = "new:"

func loadPromptDrafts(path string) *promptDrafts {
	drafts := &promptDrafts{path: path, texts: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &drafts.texts)
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
			}
			delete(drafts.texts, key)
		}
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
	drafts.flushLocked()
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

// flush writes every draft now, atomically: an interrupted write never
// leaves a half file.
func (drafts *promptDrafts) flush() {
	if drafts.path == "" {
		return
	}
	drafts.mu.Lock()
	defer drafts.mu.Unlock()
	drafts.flushLocked()
}

func (drafts *promptDrafts) flushLocked() {
	data, _ := json.Marshal(drafts.texts)
	if os.MkdirAll(filepath.Dir(drafts.path), 0700) != nil {
		return
	}
	temporary := drafts.path + ".tmp"
	if os.WriteFile(temporary, data, 0600) == nil {
		os.Rename(temporary, drafts.path)
	}
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
	entry.replace(entry.drafts.get(key))
}

// replace sets the text without recording an undo step or a draft change.
func (entry *chatEntry) replace(text string) {
	entry.restoring = true
	entry.SetText(text)
	entry.CursorRow = len(entry.textRows()) - 1
	entry.CursorColumn = len([]rune(entry.textRows()[entry.CursorRow]))
	entry.Refresh()
	entry.restoring = false
	entry.previous = text
}

func (entry *chatEntry) textRows() []string {
	rows := []string{""}
	for _, r := range entry.Text {
		if r == '\n' {
			rows = append(rows, "")
		} else {
			rows[len(rows)-1] += string(r)
		}
	}
	return rows
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
	entry.replace(text)
	entry.drafts.set(entry.draftKey, text)
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
