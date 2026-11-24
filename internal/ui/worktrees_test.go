// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"1loop/internal/gitrepo"
)

func TestNoteWorktreesSkipsOpenedWorktree(t *testing.T) {
	ui := &desktopUI{desktopApp: &desktopApp{}}
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
		ui := &desktopUI{desktopApp: &desktopApp{worktrees: test.worktrees}}
		if got := ui.projectName(test.project); got != test.want {
			t.Errorf("%s: projectName(%q) = %q, want %q", test.name, test.project, got, test.want)
		}
	}
}

// Two worktrees can bear one label: their sidebar rows keep keys of their
// own, or the animation lays them on one place and leaves a gap.
func TestWorktreeRowsKeyedByFolder(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: &desktopApp{worktreesShown: map[string]bool{"/src/app": true}}}
	ui.repoFor("/src/app").repo = &gitrepo.Repository{Worktrees: []gitrepo.Worktree{
		{Path: "/src/app", Main: true},
		{Path: "/tmp/a/base"},
		{Path: "/tmp/b/base", Missing: true},
	}}
	rows, keys := ui.worktreeRows("/src/app", func(string) {})
	if len(rows) != len(keys) {
		t.Fatalf("%d rows, %d keys", len(rows), len(keys))
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			t.Fatalf("two rows share the key %q: %v", key, keys)
		}
		seen[key] = true
	}
}
