// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"
	"time"

	"1loop/internal/agent"
)

// A command's state says how it ended and how long it ran (#597).
func TestBackgroundState(t *testing.T) {
	start := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		task   agent.BackgroundTaskInfo
		status string
		state  string
	}{
		{agent.BackgroundTaskInfo{Running: true, Started: start}, "in_progress", "Running · 42s"},
		{agent.BackgroundTaskInfo{Started: start, Ended: start.Add(3 * time.Second)}, "completed", "Done in 3s"},
		{agent.BackgroundTaskInfo{Started: start, Ended: start.Add(70 * time.Second), ExitCode: 2}, "pending", "Failed with code 2 after 1m10s"},
		{agent.BackgroundTaskInfo{Started: start, Ended: start.Add(5 * time.Second), Stopped: true, ExitCode: -1}, "pending", "Stopped after 5s"},
	}
	for _, c := range cases {
		status, state := backgroundState(c.task, start.Add(42*time.Second))
		if status != c.status || state != c.state {
			t.Errorf("%+v: %s %q, want %s %q", c.task, status, state, c.status, c.state)
		}
	}
}

// With no command yet, the tab says what it is for.
func TestBackgroundTabEmpty(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	chat := window.chatByID("root")
	window.openLocalChat(chat)
	window.panel.openBackground(chat, "")
	panel := window.panel.of(chat)
	tab := panel.tabs[panel.active]
	if tab.kind != "commands" || tab.title != "Background" || len(tab.backgroundView.Objects) != 1 {
		t.Fatalf("tab %s %q with %d objects", tab.kind, tab.title, len(tab.backgroundView.Objects))
	}
}
