package gitrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Pull is an open pull request.
type Pull struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Author   string    `json:"author,omitempty"`
	Branch   string    `json:"branch"`
	Base     string    `json:"base"`
	Fork     bool      `json:"fork,omitempty"`
	Draft    bool      `json:"draft,omitempty"`
	Checks   string    `json:"checks,omitempty"` // success, failure, pending; empty when unknown
	Review   string    `json:"review,omitempty"` // approved, changes requested, review required
	Comments int       `json:"comments"`
	Updated  time.Time `json:"updated"`
}

// Issue is an open issue.
type Issue struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Author   string    `json:"author,omitempty"`
	Labels   []string  `json:"labels,omitempty"`
	Comments int       `json:"comments"`
	Updated  time.Time `json:"updated"`
}

// Hub is the GitHub side of a repository.
type Hub struct {
	Repository  string  `json:"repository"` // owner/name
	URL         string  `json:"url"`
	Description string  `json:"description,omitempty"`
	Private     bool    `json:"private,omitempty"`
	Stars       int     `json:"stars"`
	Pulls       []Pull  `json:"pulls"`
	Issues      []Issue `json:"issues"`
	// Signed is false when GitHub was read without an account: checks and
	// private repositories are then unavailable.
	Signed bool `json:"signed"`
}

var githubAPI = "https://api.github.com"

var client = &http.Client{Timeout: 20 * time.Second}

const hubQuery = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){
nameWithOwner url description isPrivate stargazerCount
pullRequests(states:OPEN,first:50,orderBy:{field:UPDATED_AT,direction:DESC}){nodes{number title url isDraft updatedAt headRefName baseRefName isCrossRepository reviewDecision author{login} comments{totalCount} commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}
issues(states:OPEN,first:50,orderBy:{field:UPDATED_AT,direction:DESC}){nodes{number title url updatedAt author{login} comments{totalCount} labels(first:6){nodes{name}}}}}}`

// GitHub reads the open pull requests and issues of owner/name with the
// first authorization that works, else without one (public repositories).
func GitHub(ctx context.Context, owner, name string, authorizations []string) (*Hub, error) {
	var last error
	for _, authorization := range authorizations {
		hub, err := graphQL(ctx, owner, name, authorization)
		if err == nil {
			return hub, nil
		}
		last = err
	}
	hub, err := rest(ctx, owner, name)
	if err != nil && last != nil {
		return nil, last
	}
	return hub, err
}

func graphQL(ctx context.Context, owner, name, authorization string) (*Hub, error) {
	body, _ := json.Marshal(map[string]any{"query": hubQuery, "variables": map[string]string{"owner": owner, "name": name}})
	request, err := http.NewRequestWithContext(ctx, "POST", githubAPI+"/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/json")
	var reply struct {
		Data struct {
			Repository *struct {
				NameWithOwner, URL, Description string
				IsPrivate                       bool
				StargazerCount                  int
				PullRequests                    struct {
					Nodes []struct {
						Number                               int
						Title, URL, HeadRefName, BaseRefName string
						IsDraft, IsCrossRepository           bool
						ReviewDecision                       string
						UpdatedAt                            time.Time
						Author                               *struct{ Login string }
						Comments                             struct{ TotalCount int }
						Commits                              struct {
							Nodes []struct {
								Commit struct {
									StatusCheckRollup *struct{ State string }
								}
							}
						}
					}
				}
				Issues struct {
					Nodes []struct {
						Number     int
						Title, URL string
						UpdatedAt  time.Time
						Author     *struct{ Login string }
						Comments   struct{ TotalCount int }
						Labels     struct{ Nodes []struct{ Name string } }
					}
				}
			}
		}
		Errors []struct{ Message string }
	}
	if err := call(request, &reply); err != nil {
		return nil, err
	}
	repo := reply.Data.Repository
	if repo == nil {
		if len(reply.Errors) > 0 {
			return nil, fmt.Errorf("GitHub: %s", reply.Errors[0].Message)
		}
		return nil, fmt.Errorf("GitHub does not show %s/%s to this account", owner, name)
	}
	hub := &Hub{Repository: repo.NameWithOwner, URL: repo.URL, Description: repo.Description, Private: repo.IsPrivate, Stars: repo.StargazerCount, Signed: true, Pulls: []Pull{}, Issues: []Issue{}}
	for _, node := range repo.PullRequests.Nodes {
		pull := Pull{Number: node.Number, Title: node.Title, URL: node.URL, Branch: node.HeadRefName, Base: node.BaseRefName, Fork: node.IsCrossRepository, Draft: node.IsDraft, Comments: node.Comments.TotalCount, Updated: node.UpdatedAt}
		if node.Author != nil {
			pull.Author = node.Author.Login
		}
		if len(node.Commits.Nodes) > 0 && node.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
			switch node.Commits.Nodes[0].Commit.StatusCheckRollup.State {
			case "SUCCESS":
				pull.Checks = "success"
			case "FAILURE", "ERROR":
				pull.Checks = "failure"
			case "PENDING", "EXPECTED":
				pull.Checks = "pending"
			}
		}
		switch node.ReviewDecision {
		case "APPROVED":
			pull.Review = "approved"
		case "CHANGES_REQUESTED":
			pull.Review = "changes requested"
		case "REVIEW_REQUIRED":
			pull.Review = "review required"
		}
		hub.Pulls = append(hub.Pulls, pull)
	}
	for _, node := range repo.Issues.Nodes {
		issue := Issue{Number: node.Number, Title: node.Title, URL: node.URL, Comments: node.Comments.TotalCount, Updated: node.UpdatedAt}
		if node.Author != nil {
			issue.Author = node.Author.Login
		}
		for _, label := range node.Labels.Nodes {
			issue.Labels = append(issue.Labels, label.Name)
		}
		hub.Issues = append(hub.Issues, issue)
	}
	return hub, nil
}

// rest reads a public repository without an account (no checks).
func rest(ctx context.Context, owner, name string) (*Hub, error) {
	base := githubAPI + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	get := func(path string, into any) error {
		request, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if err != nil {
			return err
		}
		return call(request, into)
	}
	var repo struct {
		FullName    string `json:"full_name"`
		HTMLURL     string `json:"html_url"`
		Description string
		Private     bool
		Stars       int `json:"stargazers_count"`
	}
	if err := get("", &repo); err != nil {
		return nil, err
	}
	hub := &Hub{Repository: repo.FullName, URL: repo.HTMLURL, Description: repo.Description, Private: repo.Private, Stars: repo.Stars, Pulls: []Pull{}, Issues: []Issue{}}
	type user struct{ Login string }
	var pulls []struct {
		Number    int
		Title     string
		HTMLURL   string `json:"html_url"`
		Draft     bool
		UpdatedAt time.Time `json:"updated_at"`
		User      user
		Head      struct {
			Ref  string
			Repo *struct {
				FullName string `json:"full_name"`
			}
		}
		Base struct{ Ref string }
	}
	if err := get("/pulls?state=open&sort=updated&direction=desc&per_page=50", &pulls); err != nil {
		return nil, err
	}
	for _, p := range pulls {
		fork := p.Head.Repo == nil || p.Head.Repo.FullName != repo.FullName
		hub.Pulls = append(hub.Pulls, Pull{Number: p.Number, Title: p.Title, URL: p.HTMLURL, Author: p.User.Login, Branch: p.Head.Ref, Base: p.Base.Ref, Fork: fork, Draft: p.Draft, Updated: p.UpdatedAt})
	}
	var issues []struct {
		Number      int
		Title       string
		HTMLURL     string    `json:"html_url"`
		UpdatedAt   time.Time `json:"updated_at"`
		User        user
		Comments    int
		Labels      []struct{ Name string }
		PullRequest any `json:"pull_request"`
	}
	if err := get("/issues?state=open&sort=updated&direction=desc&per_page=50", &issues); err != nil {
		return nil, err
	}
	for _, i := range issues {
		if i.PullRequest != nil {
			continue
		}
		issue := Issue{Number: i.Number, Title: i.Title, URL: i.HTMLURL, Author: i.User.Login, Comments: i.Comments, Updated: i.UpdatedAt}
		for _, label := range i.Labels {
			issue.Labels = append(issue.Labels, label.Name)
		}
		hub.Issues = append(hub.Issues, issue)
	}
	return hub, nil
}

func call(request *http.Request, into any) error {
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if response.StatusCode != http.StatusOK {
		var reply struct{ Message string }
		_ = json.Unmarshal(data, &reply)
		if response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
			return fmt.Errorf("GitHub's limit for requests without an account is reached. Connect GitHub in Settings → Connectors")
		}
		if reply.Message != "" {
			return fmt.Errorf("GitHub (HTTP %d): %s", response.StatusCode, reply.Message)
		}
		return fmt.Errorf("GitHub answered HTTP %d", response.StatusCode)
	}
	return json.Unmarshal(data, into)
}

// Link puts each worktree's open pull request on it: the one from its
// branch (or pr-N, the branch 1loop names a fork's pull request).
func (repo *Repository) Link(hub *Hub) {
	if hub == nil {
		return
	}
	for i := range repo.Worktrees {
		worktree := &repo.Worktrees[i]
		for j := range hub.Pulls {
			pull := &hub.Pulls[j]
			if worktree.Branch != "" && (!pull.Fork && pull.Branch == worktree.Branch || worktree.Branch == fmt.Sprintf("pr-%d", pull.Number)) {
				worktree.Pull = pull
			}
		}
	}
}
