package gitrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// NewWorktree says what to check out in a new worktree.
type NewWorktree struct {
	// Kind: "new" makes Branch from Base; "branch" checks out an existing
	// branch (a remote one gets a local copy); "pull" checks out pull
	// request Pull of the GitHub repository.
	Kind   string `json:"kind"`
	Branch string `json:"branch"`
	Base   string `json:"base,omitempty"`
	Pull   int    `json:"pull,omitempty"`
	// For a pull request: its branch, and whether it comes from a fork.
	PullBranch string `json:"pullBranch,omitempty"`
	Fork       bool   `json:"fork,omitempty"`
}

// Created is a worktree made, or the existing one that already has the branch.
type Created struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Existing bool   `json:"existing,omitempty"`
}

// AddWorktree makes a worktree under the project's .worktrees folder. A
// branch already checked out somewhere returns that worktree instead.
func AddWorktree(ctx context.Context, host Host, project string, request NewWorktree) (Created, error) {
	repo, err := Open(ctx, host, project)
	if err != nil {
		return Created{}, err
	}
	branch, start, track := strings.TrimSpace(request.Branch), "", false
	var fetch []string
	switch request.Kind {
	case "new":
		if existing := repo.branch(branch); existing != nil && !existing.Remote {
			return Created{}, fmt.Errorf("A branch named %s already exists. Open it from the branches", branch)
		}
		start = strings.TrimSpace(request.Base)
		if start == "" {
			start = repo.DefaultBranch
			if repo.branch("origin/"+start) != nil || repo.Remote != "" {
				fetch, start = []string{"fetch", "origin", start}, "origin/"+start
			}
		}
	case "branch":
		if remote := repo.branch(branch); remote != nil && remote.Remote {
			_, short, _ := strings.Cut(branch, "/")
			branch, start, track = short, remote.Name, true
		} else if repo.branch(branch) == nil {
			return Created{}, fmt.Errorf("No branch named %s", branch)
		}
	case "pull":
		if request.Pull <= 0 {
			return Created{}, fmt.Errorf("Choose a pull request")
		}
		if request.Fork || request.PullBranch == "" {
			branch = "pr-" + strconv.Itoa(request.Pull)
			fetch = []string{"fetch", "origin", fmt.Sprintf("+refs/pull/%d/head:refs/remotes/origin/pr/%d", request.Pull, request.Pull)}
			start = fmt.Sprintf("origin/pr/%d", request.Pull)
		} else {
			branch = request.PullBranch
			fetch = []string{"fetch", "origin", fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)}
			start, track = "origin/"+branch, true
		}
	default:
		return Created{}, fmt.Errorf("Unknown kind of worktree")
	}
	if branch == "" {
		return Created{}, fmt.Errorf("Name the branch")
	}
	if _, err := host.Git(ctx, repo.Root, "check-ref-format", "--branch", branch); err != nil {
		return Created{}, fmt.Errorf("%s is not a valid branch name", branch)
	}
	for _, worktree := range repo.Worktrees {
		if worktree.Branch == branch && !worktree.Missing {
			return Created{Path: worktree.Path, Branch: branch, Existing: true}, nil
		}
	}
	if fetch != nil {
		fetchCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := host.Git(fetchCtx, repo.Root, fetch...)
		cancel()
		if err != nil && request.Kind == "pull" {
			return Created{}, fmt.Errorf("Could not fetch the pull request: %v", err)
		}
		if err != nil { // offline: start from the local default branch
			start = strings.TrimPrefix(start, "origin/")
		}
	}
	path, err := freeFolder(filepath.Join(repo.Root, Folder), Slug(branch))
	if err != nil {
		return Created{}, err
	}
	if err := exclude(repo.common); err != nil {
		return Created{}, err
	}
	args := []string{"worktree", "add"}
	local := repo.branch(branch)
	switch {
	case local != nil && !local.Remote:
		args = append(args, path, branch)
	case track:
		args = append(args, "--track", "-b", branch, path, start)
	default:
		// A new branch does not push to the branch it started from.
		args = append(args, "--no-track", "-b", branch, path, start)
	}
	if _, err := host.Git(ctx, repo.Root, args...); err != nil {
		return Created{}, err
	}
	return Created{Path: path, Branch: branch}, nil
}

func freeFolder(parent, name string) (string, error) {
	for i := 1; i < 100; i++ {
		candidate := filepath.Join(parent, name)
		if i > 1 {
			candidate = filepath.Join(parent, fmt.Sprintf("%s-%d", name, i))
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("No free folder for this worktree in %s", parent)
}

// exclude keeps .worktrees out of the repository's changes, without a
// tracked change: in the repository's own info/exclude.
func exclude(common string) error {
	path := filepath.Join(common, "info", "exclude")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	line := "/" + Folder + "/"
	for _, existing := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(existing) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "# 1loop's worktrees\n" + line + "\n"
	return os.WriteFile(path, []byte(text), 0o644)
}

// Removal says what to remove with a worktree.
type Removal struct {
	Path string `json:"path"`
	// Force discards its uncommitted changes.
	Force bool `json:"force,omitempty"`
	// DeleteBranch deletes its branch too, only if merged unless Force.
	DeleteBranch bool `json:"deleteBranch,omitempty"`
}

// RemoveWorktree removes a worktree (never the repository's own folder).
// The note says what was kept.
func RemoveWorktree(ctx context.Context, host Host, project string, removal Removal) (note string, err error) {
	repo, err := Open(ctx, host, project)
	if err != nil {
		return "", err
	}
	worktree := repo.Worktree(removal.Path)
	if worktree == nil {
		return "", fmt.Errorf("This worktree is no longer in the repository")
	}
	if worktree.Main {
		return "", fmt.Errorf("The repository's own folder is not a removable worktree")
	}
	if worktree.Locked {
		return "", fmt.Errorf("This worktree is locked (git worktree unlock)")
	}
	if worktree.Missing {
		if _, err := host.Git(ctx, repo.Root, "worktree", "prune"); err != nil {
			return "", err
		}
	} else {
		if worktree.Changes > 0 && !removal.Force {
			return "", fmt.Errorf("This worktree has %d uncommitted change(s)", worktree.Changes)
		}
		args := []string{"worktree", "remove", worktree.Path}
		if removal.Force {
			args = []string{"worktree", "remove", "--force", worktree.Path}
		}
		if _, err := host.Git(ctx, repo.Root, args...); err != nil {
			return "", err
		}
	}
	if removal.DeleteBranch && worktree.Branch != "" && worktree.Branch != repo.DefaultBranch {
		flag := "-d"
		if removal.Force {
			flag = "-D"
		}
		if _, err := host.Git(ctx, repo.Root, "branch", flag, worktree.Branch); err != nil {
			return "Branch " + worktree.Branch + " was kept: it is not merged.", nil
		}
	}
	return "", nil
}
