// SPDX-License-Identifier: MPL-2.0

package gitrepo

// References to GitHub issues and pull requests, as the model writes them
// in a conversation (#12, owner/name#12, or their GitHub address), and what
// 1loop shows of them: a short summary on hover, and the whole page (its
// description, comments and reviews, checks, commits and changed files) in
// the side panel. Read through GitHub's REST API with the first
// authorization that works, else without one (public repositories).

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ref names an issue or pull request; Owner and Name are empty for #N, the
// project's own repository.
type Ref struct {
	Owner  string `json:"owner,omitempty"`
	Name   string `json:"name,omitempty"`
	Number int    `json:"number"`
}

func (ref Ref) String() string {
	if ref.Owner == "" {
		return fmt.Sprintf("#%d", ref.Number)
	}
	return fmt.Sprintf("%s/%s#%d", ref.Owner, ref.Name, ref.Number)
}

var (
	refText = regexp.MustCompile(`^(?:([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+))?#([1-9][0-9]{0,6})$`)
	refURL  = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+)/(?:pull|issues)/([1-9][0-9]{0,6})(?:[/?#][^\s]*)?$`)
)

// ParseRef reads #N, owner/name#N or an issue's or pull request's GitHub
// address.
func ParseRef(text string) (Ref, bool) {
	match := refText.FindStringSubmatch(text)
	if match == nil {
		match = refURL.FindStringSubmatch(text)
	}
	if match == nil {
		return Ref{}, false
	}
	number, _ := strconv.Atoi(match[3])
	return Ref{Owner: match[1], Name: strings.TrimSuffix(match[2], ".git"), Number: number}, true
}

// Item is an issue or pull request, as the hover card shows it.
type Item struct {
	Ref     Ref       `json:"ref"`
	Pull    bool      `json:"pull"`
	Title   string    `json:"title"`
	State   string    `json:"state"` // open, closed, merged or draft
	URL     string    `json:"url"`
	Author  string    `json:"author,omitempty"`
	Body    string    `json:"body,omitempty"`
	Labels  []string  `json:"labels,omitempty"`
	Created time.Time `json:"created"`
	// Comments counts the conversation's comments, not reviews.
	Comments int `json:"comments"`
	// Pull requests only.
	Base      string `json:"base,omitempty"`
	Head      string `json:"head,omitempty"`
	HeadSHA   string `json:"headSha,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Files     int    `json:"files,omitempty"`
	Commits   int    `json:"commits,omitempty"`
}

// Detail is the whole page of an issue or pull request.
type Detail struct {
	Item
	Posts      []Post   `json:"posts"` // comments and reviews, oldest first
	CommitList []Commit `json:"commitList,omitempty"`
	Checks     []Check  `json:"checks,omitempty"`
	FileList   []File   `json:"fileList,omitempty"`
}

// Post is a comment, a review, or a review's comment on a line.
type Post struct {
	Kind   string    `json:"kind"`            // comment, review or line
	State  string    `json:"state,omitempty"` // a review's: approved, changes requested, commented
	Author string    `json:"author,omitempty"`
	Body   string    `json:"body,omitempty"`
	Path   string    `json:"path,omitempty"` // a line comment's file
	Line   int       `json:"line,omitempty"`
	At     time.Time `json:"at"`
}

// Commit is one of a pull request's commits.
type Commit struct {
	SHA    string    `json:"sha"`
	Title  string    `json:"title"`
	Author string    `json:"author,omitempty"`
	At     time.Time `json:"at"`
}

// Check is a check run on a pull request's last commit.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`               // queued, in_progress, completed
	Conclusion string `json:"conclusion,omitempty"` // success, failure, neutral, cancelled, skipped…
	URL        string `json:"url,omitempty"`
}

// File is a changed file of a pull request, with its patch.
type File struct {
	Name      string `json:"name"`
	Previous  string `json:"previous,omitempty"` // a renamed file's old name
	Status    string `json:"status"`             // added, removed, modified, renamed…
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"` // empty for a binary or very large file
}

// ErrNoItem says the repository has no issue or pull request by that number.
var ErrNoItem = fmt.Errorf("No issue or pull request by that number")

// reader reads one item's pages with the authorization that worked first.
type reader struct {
	ctx           context.Context
	base          string // /repos/owner/name
	authorization string
}

func (read *reader) get(path string, into any) error {
	request, err := http.NewRequestWithContext(read.ctx, "GET", githubAPI+read.base+path, nil)
	if err != nil {
		return err
	}
	if read.authorization != "" {
		request.Header.Set("Authorization", read.authorization)
	}
	return call(request, into)
}

type restUser struct{ Login string }

type restIssue struct {
	Number      int
	Title       string
	State       string
	HTMLURL     string `json:"html_url"`
	Body        string
	User        restUser
	Comments    int
	CreatedAt   time.Time `json:"created_at"`
	Labels      []struct{ Name string }
	PullRequest *struct{} `json:"pull_request"`
}

type restPull struct {
	Draft        bool
	Merged       bool
	Additions    int
	Deletions    int
	ChangedFiles int `json:"changed_files"`
	Commits      int
	Head         struct {
		Ref string
		SHA string
	}
	Base struct{ Ref string }
}

// openItem reads the item's summary, trying each authorization, then none.
func openItem(ctx context.Context, ref Ref, authorizations []string) (*reader, *Item, error) {
	if ref.Owner == "" || ref.Name == "" || ref.Number <= 0 {
		return nil, nil, fmt.Errorf("Unknown repository for %s", ref)
	}
	base := "/repos/" + ref.Owner + "/" + ref.Name
	var last error
	for _, authorization := range append(append([]string(nil), authorizations...), "") {
		read := &reader{ctx: ctx, base: base, authorization: authorization}
		var issue restIssue
		err := read.get("/issues/"+strconv.Itoa(ref.Number), &issue)
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 410") {
				err = fmt.Errorf("%w: %s", ErrNoItem, ref)
			}
			last = err
			continue
		}
		item := &Item{Ref: ref, Pull: issue.PullRequest != nil, Title: issue.Title, State: issue.State, URL: issue.HTMLURL,
			Author: issue.User.Login, Body: issue.Body, Comments: issue.Comments, Created: issue.CreatedAt}
		for _, label := range issue.Labels {
			item.Labels = append(item.Labels, label.Name)
		}
		if item.Pull {
			var pull restPull
			if err := read.get("/pulls/"+strconv.Itoa(ref.Number), &pull); err != nil {
				return nil, nil, err
			}
			item.Base, item.Head, item.HeadSHA = pull.Base.Ref, pull.Head.Ref, pull.Head.SHA
			item.Additions, item.Deletions, item.Files, item.Commits = pull.Additions, pull.Deletions, pull.ChangedFiles, pull.Commits
			switch {
			case pull.Merged:
				item.State = "merged"
			case pull.Draft && item.State == "open":
				item.State = "draft"
			}
		}
		return read, item, nil
	}
	return nil, nil, last
}

// Summary is what the hover card shows of ref.
func Summary(ctx context.Context, ref Ref, authorizations []string) (*Item, error) {
	_, item, err := openItem(ctx, ref, authorizations)
	return item, err
}

// Details reads the whole page of ref: its comments, and for a pull
// request its reviews and their line comments, commits, checks and files
// (the first hundred of each, as GitHub's first page gives them).
func Details(ctx context.Context, ref Ref, authorizations []string) (*Detail, error) {
	read, item, err := openItem(ctx, ref, authorizations)
	if err != nil {
		return nil, err
	}
	detail := &Detail{Item: *item}
	number := strconv.Itoa(ref.Number)
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		first error
	)
	fetch := func(path string, into any, use func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := read.get(path, into)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if first == nil {
					first = err
				}
				return
			}
			use()
		}()
	}
	var comments []struct {
		User      restUser
		Body      string
		CreatedAt time.Time `json:"created_at"`
	}
	fetch("/issues/"+number+"/comments?per_page=100", &comments, func() {
		for _, comment := range comments {
			detail.Posts = append(detail.Posts, Post{Kind: "comment", Author: comment.User.Login, Body: comment.Body, At: comment.CreatedAt})
		}
	})
	if item.Pull {
		var reviews []struct {
			User        restUser
			Body, State string
			SubmittedAt time.Time `json:"submitted_at"`
		}
		fetch("/pulls/"+number+"/reviews?per_page=100", &reviews, func() {
			for _, review := range reviews {
				state := strings.ToLower(strings.ReplaceAll(review.State, "_", " "))
				if state == "pending" {
					continue
				}
				detail.Posts = append(detail.Posts, Post{Kind: "review", State: state, Author: review.User.Login, Body: review.Body, At: review.SubmittedAt})
			}
		})
		var lines []struct {
			User      restUser
			Body      string
			Path      string
			Line      int
			CreatedAt time.Time `json:"created_at"`
		}
		fetch("/pulls/"+number+"/comments?per_page=100", &lines, func() {
			for _, line := range lines {
				detail.Posts = append(detail.Posts, Post{Kind: "line", Author: line.User.Login, Body: line.Body, Path: line.Path, Line: line.Line, At: line.CreatedAt})
			}
		})
		var commits []struct {
			SHA    string
			Author *restUser
			Commit struct {
				Message string
				Author  struct {
					Name string
					Date time.Time
				}
			}
		}
		fetch("/pulls/"+number+"/commits?per_page=100", &commits, func() {
			for _, commit := range commits {
				title, _, _ := strings.Cut(commit.Commit.Message, "\n")
				author := commit.Commit.Author.Name
				if commit.Author != nil && commit.Author.Login != "" {
					author = commit.Author.Login
				}
				detail.CommitList = append(detail.CommitList, Commit{SHA: commit.SHA, Title: title, Author: author, At: commit.Commit.Author.Date})
			}
		})
		var files []struct {
			Filename, Status, Patch string
			Previous                string `json:"previous_filename"`
			Additions, Deletions    int
		}
		fetch("/pulls/"+number+"/files?per_page=100", &files, func() {
			for _, file := range files {
				detail.FileList = append(detail.FileList, File{Name: file.Filename, Previous: file.Previous, Status: file.Status, Additions: file.Additions, Deletions: file.Deletions, Patch: file.Patch})
			}
		})
		if item.HeadSHA != "" {
			var runs struct {
				CheckRuns []struct {
					Name, Status, Conclusion string
					HTMLURL                  string `json:"html_url"`
				} `json:"check_runs"`
			}
			fetch("/commits/"+item.HeadSHA+"/check-runs?per_page=100", &runs, func() {
				for _, run := range runs.CheckRuns {
					detail.Checks = append(detail.Checks, Check{Name: run.Name, Status: run.Status, Conclusion: run.Conclusion, URL: run.HTMLURL})
				}
			})
		}
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	sort.SliceStable(detail.Posts, func(a, b int) bool { return detail.Posts[a].At.Before(detail.Posts[b].At) })
	sort.SliceStable(detail.Checks, func(a, b int) bool { return detail.Checks[a].Name < detail.Checks[b].Name })
	return detail, nil
}
