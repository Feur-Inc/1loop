// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// A draft too long for the prompt is attached, never laid out: a 440 KB one
// kept 1loop from opening (#238).
func TestOversizedDraftIsAttached(t *testing.T) {
	test.NewTempApp(t)
	huge := strings.Repeat(strings.Repeat("a", 1100)+"\n", 400)
	drafts := loadPromptDrafts(filepath.Join(t.TempDir(), "drafts.json"))
	drafts.set("new:/project", huge)
	drafts.set("chat", "short draft")

	entry := newChatEntry()
	entry.Resize(fyne.NewSize(600, 40))
	var attached []string
	entry.oversize = func(text string) { attached = append(attached, text) }
	entry.bindDrafts(drafts)

	start := time.Now()
	entry.switchDraft("new:/project")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("restoring took %v", elapsed)
	}
	if entry.Text != "" || len(attached) != 1 || attached[0] != huge {
		t.Fatalf("prompt %d bytes, attached %d texts", len(entry.Text), len(attached))
	}
	if drafts.get("new:/project") != "" {
		t.Error("the draft was kept: it would be attached again at every start")
	}

	entry.switchDraft("chat")
	if entry.Text != "short draft" || len(attached) != 1 {
		t.Fatalf("a short draft: prompt %q, %d attached", entry.Text, len(attached))
	}

	// Typing past the limit (a key held down) attaches what was typed.
	entry.SetText(strings.Repeat("b", promptMaxLine+1))
	if entry.Text != "" || len(attached) != 2 || drafts.get("chat") != "" {
		t.Fatalf("typed past the limit: prompt %d bytes, %d attached", len(entry.Text), len(attached))
	}
}

func TestTextTooBig(t *testing.T) {
	for text, want := range map[string]bool{
		"":                                           false,
		strings.Repeat("a\n", promptMaxBytes/2):      false,
		strings.Repeat("a\n", promptMaxBytes):        true,
		strings.Repeat("a", promptMaxLine):           false,
		"x\n" + strings.Repeat("a", promptMaxLine+1): true,
	} {
		if got := textTooBig(text); got != want {
			t.Errorf("textTooBig(%d bytes) = %v", len(text), got)
		}
	}
}
