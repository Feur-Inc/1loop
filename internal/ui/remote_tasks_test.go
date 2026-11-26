// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"1loop/internal/remoteapi"
)

// A server chat's Tasks tab is drawn as this computer's: a title with the
// count done, and each task's text without its raw status (#542).
func TestRemoteTasksDrawnLikeLocalOnes(t *testing.T) {
	test.NewTempApp(t)
	started := time.Now().Add(-9 * time.Second).UnixMilli()
	remote := &remoteDesktop{}
	remote.meta.Todo = []remoteapi.Todo{
		{ID: "1", Text: "Push the three branches", Status: "completed", Started: started, Completed: time.Now().UnixMilli()},
		{ID: "2", Text: "Open PRs into dev", Status: "in_progress", Started: started},
		{ID: "3", Text: "Fix #507", Status: "pending"},
	}
	items := remote.todoItems()
	if len(items) != 3 || items[0].CompletedAt == nil || items[1].StartedAt == nil || items[2].StartedAt != nil {
		t.Fatalf("items: %+v", items)
	}
	box, times := container.NewVBox(), map[string]*canvas.Text{}
	marks := drawTaskList(box, items, func() bool { return false }, times)
	if len(marks) != 3 || len(times) != 3 {
		t.Fatalf("%d marks, %d clocks", len(marks), len(times))
	}
	var texts []string
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch value := object.(type) {
		case *canvas.Text:
			texts = append(texts, value.Text)
		case *fyne.Container:
			for _, child := range value.Objects {
				walk(child)
			}
		}
	}
	walk(box)
	found := map[string]bool{}
	for _, text := range texts {
		found[text] = true
		if len(text) > 11 && text[:11] == "in_progress" {
			t.Fatalf("raw status drawn: %q", text)
		}
	}
	if !found["Tasks"] || !found["1 of 3 done"] {
		t.Fatalf("texts drawn: %q", texts)
	}
}
