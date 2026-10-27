package extensions

// The store: skills and plugins found on GitHub by their topic, with their
// stars, description, picture and README.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The topics repositories use: one per kind (GitHub's search takes no OR
// between topics, and allows ten searches a minute without an account).
var storeTopics = map[string]string{
	"skill":  "agent-skills",
	"plugin": "claude-code-plugin",
}

// Listing is a repository in the store.
type Listing struct {
	Repo        string    `json:"repo"` // owner/name
	Owner       string    `json:"owner"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Image       string    `json:"image"`  // its social preview: the picture it set, or GitHub's card
	Avatar      string    `json:"avatar"` // its owner's
	Stars       int       `json:"stars"`
	Forks       int       `json:"forks"`
	Updated     time.Time `json:"updated"`
	Topics      []string  `json:"topics"`
	License     string    `json:"license,omitempty"`
	Kind        string    `json:"kind"` // "skill" or "plugin"
	Installed   bool      `json:"installed"`
}

// SearchResult is a page of the store.
type SearchResult struct {
	Listings []Listing `json:"listings"`
	Total    int       `json:"total"`
	More     bool      `json:"more"`
}

type cachedSearch struct {
	result SearchResult
	at     time.Time
}

var (
	searchMu    sync.Mutex
	searchCache = map[string]cachedSearch{}
	githubAPI   = "https://api.github.com"
	githubRaw   = "https://raw.githubusercontent.com"
	githubLoad  = "https://codeload.github.com"
	githubHTTP  = &http.Client{Timeout: 60 * time.Second}
)

const storePage = 24

// Search looks for skills or plugins ("skill", "plugin" or "all"), sorted
// by "stars", "updated" or "best" (GitHub's relevance).
func Search(ctx context.Context, kind, query, order string, page int) (SearchResult, error) {
	if page < 1 {
		page = 1
	}
	kinds := []string{kind}
	if kind != "skill" && kind != "plugin" {
		kinds = []string{"skill", "plugin"}
	}
	per := storePage / len(kinds)
	var result SearchResult
	for _, kind := range kinds {
		part, err := searchKind(ctx, kind, query, order, page, per)
		if err != nil {
			return SearchResult{}, err
		}
		result.Listings = append(result.Listings, part.Listings...)
		result.Total += part.Total
		result.More = result.More || part.More
	}
	seen := map[string]bool{}
	unique := result.Listings[:0]
	for _, listing := range result.Listings {
		if !seen[listing.Repo] {
			seen[listing.Repo] = true
			unique = append(unique, listing)
		}
	}
	result.Listings = unique
	switch order {
	case "updated":
		sort.SliceStable(result.Listings, func(i, j int) bool { return result.Listings[i].Updated.After(result.Listings[j].Updated) })
	case "stars":
		sort.SliceStable(result.Listings, func(i, j int) bool { return result.Listings[i].Stars > result.Listings[j].Stars })
	}
	markInstalled(result.Listings)
	return result, nil
}

func searchKind(ctx context.Context, kind, query, order string, page, per int) (SearchResult, error) {
	key := fmt.Sprintf("%s|%s|%s|%d|%d", kind, strings.TrimSpace(query), order, page, per)
	searchMu.Lock()
	if cached, ok := searchCache[key]; ok && time.Since(cached.at) < 10*time.Minute {
		searchMu.Unlock()
		return cached.result, nil
	}
	searchMu.Unlock()
	values := url.Values{}
	values.Set("q", strings.TrimSpace(query+" topic:"+storeTopics[kind]+" fork:false"))
	values.Set("per_page", strconv.Itoa(per))
	values.Set("page", strconv.Itoa(page))
	if order == "stars" || order == "updated" {
		values.Set("sort", order)
		values.Set("order", "desc")
	}
	var reply struct {
		Total int `json:"total_count"`
		Items []struct {
			FullName    string    `json:"full_name"`
			Name        string    `json:"name"`
			Description string    `json:"description"`
			HTMLURL     string    `json:"html_url"`
			Stars       int       `json:"stargazers_count"`
			Forks       int       `json:"forks_count"`
			Pushed      time.Time `json:"pushed_at"`
			Topics      []string  `json:"topics"`
			Owner       struct {
				Login  string `json:"login"`
				Avatar string `json:"avatar_url"`
			} `json:"owner"`
			License *struct {
				SPDX string `json:"spdx_id"`
			} `json:"license"`
		} `json:"items"`
	}
	if err := githubJSON(ctx, githubAPI+"/search/repositories?"+values.Encode(), &reply); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Total: reply.Total, More: page*per < reply.Total && page*per < 1000}
	for _, item := range reply.Items {
		listing := Listing{Repo: item.FullName, Owner: item.Owner.Login, Name: item.Name, Description: item.Description,
			URL: item.HTMLURL, Image: "https://opengraph.githubassets.com/1/" + item.FullName, Avatar: item.Owner.Avatar,
			Stars: item.Stars, Forks: item.Forks, Updated: item.Pushed, Topics: item.Topics, Kind: kind}
		if item.License != nil && item.License.SPDX != "NOASSERTION" {
			listing.License = item.License.SPDX
		}
		result.Listings = append(result.Listings, listing)
	}
	searchMu.Lock()
	searchCache[key] = cachedSearch{result: result, at: time.Now()}
	searchMu.Unlock()
	return result, nil
}

// markInstalled flags what is installed already.
func markInstalled(listings []Listing) {
	registry, err := Load()
	if err != nil {
		return
	}
	installed := map[string]bool{}
	for _, skill := range registry.Skills {
		installed[strings.ToLower(skill.Source)] = true
	}
	for _, plugin := range registry.Plugins {
		installed[strings.ToLower(plugin.Repo)] = true
	}
	for i := range listings {
		listings[i].Installed = installed[strings.ToLower(listings[i].Repo)]
	}
}

func githubRequest(ctx context.Context, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "1loop")
	if strings.HasPrefix(address, githubAPI) {
		request.Header.Set("Accept", "application/vnd.github+json")
		if token := firstEnv("GITHUB_TOKEN", "GH_TOKEN"); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return githubHTTP.Do(request)
}

func githubJSON(ctx context.Context, address string, out any) error {
	response, err := githubRequest(ctx, address)
	if err != nil {
		return fmt.Errorf("GitHub cannot be reached: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		if response.Header.Get("X-RateLimit-Remaining") == "0" {
			wait := ""
			if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
				wait = fmt.Sprintf(" in %d s", max(1, int(time.Until(time.Unix(reset, 0)).Seconds())+1))
			}
			return fmt.Errorf("GitHub's search limit is reached: try again%s", wait)
		}
	}
	if response.StatusCode >= 300 {
		var problem struct{ Message string }
		json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem)
		return fmt.Errorf("GitHub answered %d %s", response.StatusCode, problem.Message)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out)
}

// Readme is a repository's README (Markdown), "" when it has none.
func Readme(ctx context.Context, repo string) (string, error) {
	if !validRepo(repo) {
		return "", fmt.Errorf("Not a GitHub repository: %s", repo)
	}
	for _, name := range []string{"README.md", "readme.md", "Readme.md", "README"} {
		response, err := githubRequest(ctx, githubRaw+"/"+repo+"/HEAD/"+name)
		if err != nil {
			return "", fmt.Errorf("GitHub cannot be reached: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(response.Body, 256<<10))
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return string(data), nil
		}
	}
	return "", nil
}

func validRepo(repo string) bool {
	owner, name, found := strings.Cut(repo, "/")
	return found && owner != "" && name != "" && !strings.ContainsAny(repo, " \\?#") && !strings.Contains(name, "/") && !strings.Contains(repo, "..")
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
