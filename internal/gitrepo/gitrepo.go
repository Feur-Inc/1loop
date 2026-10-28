// Package gitrepo is 1loop's view of a project's Git repository: its
// worktrees, branches and GitHub remote. Git itself is the source of truth,
// so worktrees made outside 1loop show up too. 1loop makes its own in the
// project, under .worktrees/, where every execution target can reach them.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"1loop/internal/platform"
)

// Folder is where 1loop makes worktrees, inside the project.
const Folder = ".worktrees"

var ErrNotRepository = errors.New("This project is not a Git repository")

// Worktree is one checkout of the repository.
type Worktree struct {
	Path     string    `json:"path"`
	Branch   string    `json:"branch,omitempty"` // empty when detached
	Head     string    `json:"head"`
	Main     bool      `json:"main,omitempty"` // the repository's own folder
	Locked   bool      `json:"locked,omitempty"`
	Missing  bool      `json:"missing,omitempty"` // its folder is gone (git can prune it)
	Changes  int       `json:"changes"`           // files changed or untracked
	Ahead    int       `json:"ahead"`
	Behind   int       `json:"behind"`
	Upstream string    `json:"upstream,omitempty"`
	Subject  string    `json:"subject,omitempty"` // of the last commit
	Updated  time.Time `json:"updated"`           // the last commit's date
	Pull     *Pull     `json:"pull,omitempty"`    // its open pull request, when known
}

// Branch is a local branch, or a remote one without a local copy.
type Branch struct {
	Name     string    `json:"name"`
	Remote   bool      `json:"remote,omitempty"`
	Upstream string    `json:"upstream,omitempty"`
	Ahead    int       `json:"ahead"`
	Behind   int       `json:"behind"`
	Subject  string    `json:"subject,omitempty"`
	Updated  time.Time `json:"updated"`
	Worktree string    `json:"worktree,omitempty"` // where it is checked out
}

// Repository is what the worktree and repository views show.
type Repository struct {
	Root          string     `json:"root"` // the main worktree
	Current       string     `json:"current,omitempty"`
	DefaultBranch string     `json:"defaultBranch,omitempty"`
	Remote        string     `json:"remote,omitempty"` // origin's address
	Owner         string     `json:"owner,omitempty"`  // on GitHub
	Name          string     `json:"name,omitempty"`
	Worktrees     []Worktree `json:"worktrees"`
	Branches      []Branch   `json:"branches"`
	common        string
}

// Host runs Git where a project lives.
type Host interface {
	Git(ctx context.Context, dir string, args ...string) (string, error)
}

type local struct{}

// Local runs the git of this computer.
var Local Host = local{}

func (local) Git(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	platform.PrepareProcess(command)
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		command.Env = append(command.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("Git is not installed")
		}
		message := strings.TrimSpace(stderr.String())
		message = strings.TrimPrefix(message, "fatal: ")
		if message == "" {
			message = err.Error()
		}
		return string(out), errors.New(message)
	}
	return string(out), nil
}

// Open reads the repository a project belongs to.
func Open(ctx context.Context, host Host, project string) (*Repository, error) {
	top, err := host.Git(ctx, project, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return nil, ErrNotRepository
		}
		return nil, err
	}
	common, err := host.Git(ctx, project, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	repo := &Repository{common: filepath.Clean(strings.TrimSpace(common)), Root: filepath.Clean(strings.TrimSpace(top))}
	list, err := host.Git(ctx, project, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	repo.Worktrees = parseWorktrees(list)
	if len(repo.Worktrees) > 0 {
		repo.Root = repo.Worktrees[0].Path
		repo.Current = repo.Worktrees[0].Branch
	}
	if url, err := host.Git(ctx, repo.Root, "remote", "get-url", "origin"); err == nil {
		repo.Remote = strings.TrimSpace(url)
		repo.Owner, repo.Name = GitHubRepository(repo.Remote)
	}
	if head, err := host.Git(ctx, repo.Root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		repo.DefaultBranch = strings.TrimPrefix(strings.TrimSpace(head), "origin/")
	}
	refs, err := host.Git(ctx, repo.Root, "for-each-ref", "--format=%(refname)%09%(committerdate:unix)%09%(upstream:short)%09%(upstream:track,nobracket)%09%(subject)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	repo.Branches = parseBranches(refs, repo.Worktrees)
	if repo.DefaultBranch == "" {
		for _, name := range []string{"main", "master", "dev", repo.Current} {
			if repo.branch(name) != nil {
				repo.DefaultBranch = name
				break
			}
		}
	}
	for i := range repo.Worktrees {
		worktree := &repo.Worktrees[i]
		if worktree.Missing {
			continue
		}
		if branch := repo.branch(worktree.Branch); branch != nil && !branch.Remote {
			worktree.Upstream, worktree.Ahead, worktree.Behind = branch.Upstream, branch.Ahead, branch.Behind
			worktree.Subject, worktree.Updated = branch.Subject, branch.Updated
		} else if line, err := host.Git(ctx, worktree.Path, "log", "-1", "--format=%ct%x09%s"); err == nil {
			worktree.Updated, worktree.Subject = commitLine(line)
		}
		if status, err := host.Git(ctx, worktree.Path, "status", "--porcelain"); err == nil {
			worktree.Changes = len(strings.FieldsFunc(status, func(r rune) bool { return r == '\n' }))
		}
	}
	return repo, nil
}

func (repo *Repository) branch(name string) *Branch {
	for i := range repo.Branches {
		if repo.Branches[i].Name == name {
			return &repo.Branches[i]
		}
	}
	return nil
}

// Worktree is the worktree at path, nil if the repository has none there.
func (repo *Repository) Worktree(path string) *Worktree {
	for i := range repo.Worktrees {
		if samePath(repo.Worktrees[i].Path, path) {
			return &repo.Worktrees[i]
		}
	}
	return nil
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if strings.EqualFold(a, b) && filepath.Separator == '\\' {
		return true
	}
	return a == b
}

func parseWorktrees(list string) []Worktree {
	var worktrees []Worktree
	for _, block := range strings.Split(strings.ReplaceAll(list, "\r\n", "\n"), "\n\n") {
		var worktree Worktree
		bare := false
		for _, line := range strings.Split(block, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				worktree.Path = filepath.Clean(value)
			case "HEAD":
				worktree.Head = value
			case "branch":
				worktree.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare":
				bare = true
			case "locked":
				worktree.Locked = true
			case "prunable":
				worktree.Missing = true
			}
		}
		if worktree.Path != "" && !bare {
			worktrees = append(worktrees, worktree)
		}
	}
	if len(worktrees) > 0 {
		worktrees[0].Main = true
	}
	return worktrees
}

var trackPattern = regexp.MustCompile(`(ahead|behind) (\d+)`)

func parseBranches(refs string, worktrees []Worktree) []Branch {
	local := map[string]bool{}
	var branches []Branch
	for _, line := range strings.Split(strings.TrimSpace(refs), "\n") {
		fields := strings.SplitN(line, "\t", 5)
		if len(fields) < 5 {
			continue
		}
		branch := Branch{Upstream: fields[2], Subject: fields[4]}
		branch.Updated, _ = commitLine(fields[1] + "\t")
		for _, match := range trackPattern.FindAllStringSubmatch(fields[3], -1) {
			count, _ := strconv.Atoi(match[2])
			if match[1] == "ahead" {
				branch.Ahead = count
			} else {
				branch.Behind = count
			}
		}
		if name, ok := strings.CutPrefix(fields[0], "refs/heads/"); ok {
			branch.Name = name
			local[name] = true
			for _, worktree := range worktrees {
				if worktree.Branch == name {
					branch.Worktree = worktree.Path
				}
			}
		} else if name, ok := strings.CutPrefix(fields[0], "refs/remotes/"); ok && !strings.HasSuffix(name, "/HEAD") && strings.Contains(name, "/") {
			branch.Name, branch.Remote = name, true
		} else {
			continue
		}
		branches = append(branches, branch)
	}
	// A remote branch already copied locally is the local one.
	kept := branches[:0]
	for _, branch := range branches {
		if branch.Remote {
			_, short, _ := strings.Cut(branch.Name, "/")
			if local[short] {
				continue
			}
		}
		kept = append(kept, branch)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Remote != kept[j].Remote {
			return !kept[i].Remote
		}
		return kept[i].Updated.After(kept[j].Updated)
	})
	return kept
}

func commitLine(line string) (time.Time, string) {
	stamp, subject, _ := strings.Cut(strings.TrimSpace(line), "\t")
	seconds, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64)
	if err != nil {
		return time.Time{}, subject
	}
	return time.Unix(seconds, 0), subject
}

var githubRemote = regexp.MustCompile(`^(?:https://(?:[^@/]+@)?github\.com/|ssh://git@github\.com/|git@github\.com:)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// GitHubRepository is owner and name when address is a GitHub repository.
func GitHubRepository(address string) (owner, name string) {
	match := githubRemote.FindStringSubmatch(strings.TrimSpace(address))
	if match == nil {
		return "", ""
	}
	return match[1], match[2]
}

var unsafeSlug = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Slug is a folder name for a branch.
func Slug(branch string) string {
	slug := strings.Trim(unsafeSlug.ReplaceAllString(strings.ReplaceAll(branch, "/", "-"), "-"), "-.")
	if slug == "" {
		slug = "worktree"
	}
	if len(slug) > 60 {
		slug = strings.TrimRight(slug[:60], "-.")
	}
	return slug
}

// IssueBranch names the branch of a worktree started for an issue.
func IssueBranch(number int, title string) string {
	slug := strings.ToLower(Slug(title))
	if len(slug) > 40 {
		slug = slug[:40]
		if cut := strings.LastIndex(slug, "-"); cut > 10 { // a whole word
			slug = slug[:cut]
		}
		slug = strings.TrimRight(slug, "-.")
	}
	return fmt.Sprintf("issue-%d-%s", number, slug)
}

// Sorts of the worktree list.
const (
	SortRecent = "recent" // the latest commit first
	SortName   = "name"
	SortPull   = "pull" // those with a pull request first, newest pull request first
)

// Sorted is the worktrees in a sort's order, the repository's own folder first.
func Sorted(worktrees []Worktree, by string) []Worktree {
	sorted := append([]Worktree(nil), worktrees...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Main != b.Main {
			return a.Main
		}
		switch by {
		case SortName:
			return strings.ToLower(a.Label()) < strings.ToLower(b.Label())
		case SortPull:
			if (a.Pull != nil) != (b.Pull != nil) {
				return a.Pull != nil
			}
			if a.Pull != nil && a.Pull.Number != b.Pull.Number {
				return a.Pull.Number > b.Pull.Number
			}
		}
		return a.Updated.After(b.Updated)
	})
	return sorted
}

// Label names a worktree: its branch, or its folder when detached.
func (worktree Worktree) Label() string {
	if worktree.Branch != "" {
		return worktree.Branch
	}
	return filepath.Base(worktree.Path) + " (detached)"
}
