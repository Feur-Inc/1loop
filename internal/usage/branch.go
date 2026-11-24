// SPDX-License-Identifier: MPL-2.0

package usage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var branches struct {
	sync.Mutex
	seen map[string]branchSeen
}

type branchSeen struct {
	name string
	at   time.Time
}

// Branch is the Git branch checked out in folder ("" outside a repository,
// or the commit's short id when detached). It reads .git/HEAD, a worktree's
// too, without running Git, and remembers it for a few seconds: a turn makes
// many calls.
func Branch(folder string) string {
	if folder == "" {
		return ""
	}
	branches.Lock()
	defer branches.Unlock()
	if seen, ok := branches.seen[folder]; ok && time.Since(seen.at) < 10*time.Second {
		return seen.name
	}
	name := readBranch(folder)
	if branches.seen == nil {
		branches.seen = map[string]branchSeen{}
	}
	branches.seen[folder] = branchSeen{name, time.Now()}
	return name
}

func readBranch(folder string) string {
	for dir := folder; ; {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			gitDir := dotGit
			if !info.IsDir() { // a worktree: "gitdir: <path>"
				data, err := os.ReadFile(dotGit)
				if err != nil {
					return ""
				}
				gitDir = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(dir, gitDir)
				}
			}
			head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
			if err != nil {
				return ""
			}
			text := strings.TrimSpace(string(head))
			if ref, ok := strings.CutPrefix(text, "ref: refs/heads/"); ok {
				return ref
			}
			if len(text) >= 7 {
				return text[:7]
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
