// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"1loop/internal/gitrepo"
)

func TestNoteWorktreesSkipsOpenedWorktree(t *testing.T) {
	ui := &desktopUI{}
	repo := &gitrepo.Repository{Worktrees: []gitrepo.Worktree{
		{Path: "/src/app", Main: true},
		{Path: "/src/app/.claude/worktrees/fix"},
		{Path: "/src/app/.claude/worktrees/other"},
	}}
	ui.noteWorktrees("/src/app/.claude/worktrees/fix", repo)
	if ui.saveTimer != nil {
		ui.saveTimer.Stop()
	}
	if owner := ui.worktrees["/src/app/.claude/worktrees/fix"]; owner != "" {
		t.Fatalf("the opened worktree is mapped to %q", owner)
	}
	if owner := ui.worktrees["/src/app/.claude/worktrees/other"]; owner != "/src/app/.claude/worktrees/fix" {
		t.Fatalf("the other worktree is mapped to %q", owner)
	}
}

func TestProjectNameCycles(t *testing.T) {
	for _, test := range []struct {
		name      string
		worktrees map[string]string
		project   string
		want      string
	}{
		{"plain project", nil, "/src/app", "app"},
		{"worktree", map[string]string{"/src/fix": "/src/app"}, "/src/fix", "app › fix"},
		{"its own project", map[string]string{"/src/fix": "/src/fix"}, "/src/fix", "fix"},
		{"each other's", map[string]string{"/src/a": "/src/b", "/src/b": "/src/a"}, "/src/a", "b › a"},
	} {
		ui := &desktopUI{worktrees: test.worktrees}
		if got := ui.projectName(test.project); got != test.want {
			t.Errorf("%s: projectName(%q) = %q, want %q", test.name, test.project, got, test.want)
		}
	}
}
