// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// A state save with 200 chats and a 4,000-row one (#475): ui is what the
// interface's thread does (persist returns once the copies are taken), all
// the whole save, encoding and writing included.
func BenchmarkPerfSave(b *testing.B) {
	ui, _ := perfWindow(b, 4000)
	ui.statePath = filepath.Join(b.TempDir(), "state.json")
	for i := range 200 {
		ui.chats = append(ui.chats, &conversation{ID: fmt.Sprint("c", i), Title: fmt.Sprint("Chat ", i), Items: perfItems(6),
			Config: agent.Config{Workspace: fmt.Sprintf("/tmp/project-%02d", i%30)}})
	}
	b.Run("ui", func(b *testing.B) {
		for range b.N {
			ui.persist(false)
			b.StopTimer()
			for {
				ui.saveLock.Lock()
				done := ui.saveDone == ui.saveCount
				ui.saveLock.Unlock()
				if done {
					break
				}
				time.Sleep(time.Millisecond)
			}
			b.StartTimer()
		}
	})
	b.Run("all", func(b *testing.B) {
		for range b.N {
			ui.persist(true)
		}
	})
}

// A save cut off before its end (the process killed while it writes) leaves
// the last complete state in place, read back whole at the next start: the
// file is written beside it and renamed over it only once complete (#475).
func TestSaveCutOffKeepsTheLastState(t *testing.T) {
	ui, _ := twoWindows(t)
	ui.windows = ui.windows[:1]
	ui.chats = []*conversation{{ID: "a", Title: "Kept", Items: perfItems(40)}}
	ui.persist(true)
	err := core.WriteFileStreamed(ui.statePath, 0600, true, func(w io.Writer) error {
		w.Write([]byte(`{"chats":[{"id":"a","title":"Half`))
		return errors.New("killed")
	})
	if err == nil {
		t.Fatal("the cut-off save reported no error")
	}
	state, err := readSavedState(ui.statePath)
	if err != nil || len(state.Chats) != 1 || state.Chats[0].Title != "Kept" || len(state.Chats[0].Items) != 40 {
		t.Fatalf("after a cut-off save: %v, %d chats", err, len(state.Chats))
	}
}
