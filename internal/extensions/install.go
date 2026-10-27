package extensions

// Installing from GitHub in two steps: Prepare downloads the repository into
// a staging folder and says what installing it would do (the confirmation);
// Install then copies what the user kept. Nothing runs during either.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"1loop/internal/core"
)

// Plan is what installing a repository would do.
type Plan struct {
	ID       string        `json:"id"`
	Repo     string        `json:"repo"`
	Kind     string        `json:"kind"` // "skills", "plugin" or "marketplace"
	Skills   []PlanSkill   `json:"skills"`
	Plugins  []PlanPlugin  `json:"plugins"`
	Folders  []string      `json:"folders"`  // written or replaced
	Commands []string      `json:"commands"` // what may run on this computer
	Risks    []string      `json:"risks"`
	Notes    []string      `json:"notes"`
	Size     int64         `json:"size"`
	staged   string        // the extracted repository
	sources  []stagedSkill // where each planned item comes from
	created  time.Time
}

// PlanSkill is a skill installed on its own.
type PlanSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Folder      string   `json:"folder"`
	Scripts     []string `json:"scripts"`
	Replaces    bool     `json:"replaces"` // an installed skill has its name
}

// PlanPlugin is a plugin, with what it brings.
type PlanPlugin struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version,omitempty"`
	Folder      string    `json:"folder"`
	Skills      []string  `json:"skills"`
	MCP         []PlanMCP `json:"mcp"`
	Hooks       []string  `json:"hooks"`
	Scripts     []string  `json:"scripts"`
	Commands    int       `json:"commands"`
	Agents      int       `json:"agents"`
	Replaces    bool      `json:"replaces"`
}

// PlanMCP is an MCP server a plugin starts or reaches.
type PlanMCP struct {
	Name string `json:"name"`
	Run  string `json:"run"` // the command line, or the address
}

type stagedSkill struct {
	kind, id, dir string
}

var (
	plansMu sync.Mutex
	plans   = map[string]*Plan{}
)

const maxDownload = 200 << 20

// Prepare downloads a repository and plans its installation.
func Prepare(ctx context.Context, repo string) (*Plan, error) {
	if !validRepo(repo) {
		return nil, fmt.Errorf("Not a GitHub repository: %s", repo)
	}
	cleanStaging()
	plan := &Plan{ID: core.Identifier()[:16], Repo: repo, created: time.Now(), Skills: []PlanSkill{}, Plugins: []PlanPlugin{}}
	plan.staged = filepath.Join(Dir(), "staging", plan.ID)
	size, err := downloadRepo(ctx, repo, plan.staged)
	if err != nil {
		os.RemoveAll(plan.staged)
		return nil, err
	}
	plan.Size = size
	registry, _ := Load()
	root := plan.staged
	switch {
	case fileExists(filepath.Join(root, ".claude-plugin", "marketplace.json")):
		plan.Kind = "marketplace"
		if err := plan.addMarketplace(ctx, root, registry); err != nil {
			os.RemoveAll(plan.staged)
			return nil, err
		}
	case IsPlugin(root):
		plan.Kind = "plugin"
		plan.addPlugin(repo, root, registry)
	default:
		plan.Kind = "skills"
		for _, skill := range FindSkills(root) {
			name := safeName(skill.Name)
			folder := filepath.Join(Dir(), "skills", name)
			replaces := false
			for _, installed := range registry.Skills {
				replaces = replaces || installed.Name == skill.Name
			}
			plan.Skills = append(plan.Skills, PlanSkill{Name: skill.Name, Description: skill.Description, Folder: folder, Scripts: scripts(skill.Dir), Replaces: replaces})
			plan.sources = append(plan.sources, stagedSkill{"skill", skill.Name, skill.Dir})
		}
	}
	if len(plan.Skills) == 0 && len(plan.Plugins) == 0 {
		os.RemoveAll(plan.staged)
		return nil, fmt.Errorf("%s holds no skill (a SKILL.md) and no plugin (.claude-plugin/plugin.json)", repo)
	}
	plan.explain()
	plansMu.Lock()
	plans[plan.ID] = plan
	plansMu.Unlock()
	return plan, nil
}

func (plan *Plan) addPlugin(id, dir string, registry Registry) {
	contents := ReadPlugin(dir)
	planned := PlanPlugin{ID: id, Name: contents.Manifest.Name, Description: contents.Manifest.Description, Version: contents.Manifest.Version,
		Folder: filepath.Join(Dir(), "plugins", safeName(id)), Skills: []string{}, MCP: []PlanMCP{}, Hooks: contents.Hooks,
		Scripts: scripts(dir), Commands: contents.Commands, Agents: contents.Agents}
	if planned.Hooks == nil {
		planned.Hooks = []string{}
	}
	final := planned.Folder
	for _, skill := range contents.Skills {
		planned.Skills = append(planned.Skills, skill.Name)
	}
	for _, server := range ReadPlugin(dir).MCP {
		run := server.URL
		if server.Type == "stdio" {
			run = strings.TrimSpace(server.Command + " " + strings.Join(server.Args, " "))
		}
		// Shown with the folder it will have once installed.
		planned.MCP = append(planned.MCP, PlanMCP{Name: server.Name, Run: strings.ReplaceAll(run, dir, final)})
	}
	for i, hook := range planned.Hooks {
		planned.Hooks[i] = strings.ReplaceAll(hook, dir, final)
	}
	for _, installed := range registry.Plugins {
		planned.Replaces = planned.Replaces || installed.ID == id
	}
	plan.Plugins = append(plan.Plugins, planned)
	plan.sources = append(plan.sources, stagedSkill{"plugin", id, dir})
}

// addMarketplace plans a marketplace's plugins: those in the repository
// itself, and those it names on GitHub.
func (plan *Plan) addMarketplace(ctx context.Context, root string, registry Registry) error {
	var market struct {
		Name    string        `json:"name"`
		Plugins []marketEntry `json:"plugins"`
	}
	data, _ := os.ReadFile(filepath.Join(root, ".claude-plugin", "marketplace.json"))
	if err := json.Unmarshal(data, &market); err != nil {
		return fmt.Errorf("%s: its marketplace.json cannot be read: %w", plan.Repo, err)
	}
	fetched := 0
	for _, entry := range market.Plugins {
		var path string
		var source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
		}
		dir := ""
		switch {
		case json.Unmarshal(entry.Source, &path) == nil:
			dir = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "./")))
			if rel, err := filepath.Rel(root, dir); err != nil || strings.HasPrefix(rel, "..") {
				dir = ""
			}
		case json.Unmarshal(entry.Source, &source) == nil && source.Source == "github" && validRepo(source.Repo) && fetched < 10:
			fetched++
			dir = filepath.Join(plan.staged+"-deps", safeName(source.Repo))
			if _, err := downloadRepo(ctx, source.Repo, dir); err != nil {
				plan.Notes = append(plan.Notes, entry.Name+": "+err.Error())
				dir = ""
			}
		}
		if dir == "" || !dirExists(dir) {
			plan.Notes = append(plan.Notes, entry.Name+": its source is not in this repository or on GitHub, so it is not offered")
			continue
		}
		id := plan.Repo + ":" + entry.Name
		// Each plugin gets a folder of its own: several may share the
		// repository's root and pick their skills in it.
		assembled := filepath.Join(plan.staged+"-plugins", safeName(id))
		if err := assemble(entry, dir, assembled); err != nil {
			plan.Notes = append(plan.Notes, entry.Name+": "+err.Error())
			continue
		}
		before := len(plan.Plugins)
		plan.addPlugin(id, assembled, registry)
		added := &plan.Plugins[before]
		if added.Name == filepath.Base(dir) && entry.Name != "" {
			added.Name = entry.Name
		}
		if added.Description == "" {
			added.Description = entry.Description
		}
		if added.Version == "" {
			added.Version = entry.Version
		}
	}
	return nil
}

// marketEntry is a plugin of a marketplace.json. Besides its source, it may
// list its own parts (with "strict": false, a plugin without plugin.json).
type marketEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	Source      json.RawMessage `json:"source"`
	Skills      json.RawMessage `json:"skills"`
	Commands    json.RawMessage `json:"commands"`
	Agents      json.RawMessage `json:"agents"`
	Hooks       json.RawMessage `json:"hooks"`
	MCPServers  json.RawMessage `json:"mcpServers"`
}

// assemble builds a marketplace plugin's folder: its source folder, or
// the parts the entry lists, with a plugin.json saying what it is.
func assemble(entry marketEntry, source, target string) error {
	lists := len(entry.Skills)+len(entry.Commands)+len(entry.Agents) > 0
	if !lists {
		if err := copyTree(source, target); err != nil {
			return err
		}
	}
	copyListed := func(raw json.RawMessage, into string) error {
		var paths []string
		var one string
		if json.Unmarshal(raw, &one) == nil && one != "" {
			paths = []string{one}
		} else {
			json.Unmarshal(raw, &paths)
		}
		for _, path := range paths {
			from := filepath.Join(source, filepath.FromSlash(strings.TrimPrefix(path, "./")))
			if rel, err := filepath.Rel(source, from); err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			info, err := os.Stat(from)
			if err != nil {
				continue
			}
			to := filepath.Join(target, into, filepath.Base(from))
			if info.IsDir() {
				if err := copyTree(from, to); err != nil {
					return err
				}
			} else if data, err := os.ReadFile(from); err == nil {
				os.MkdirAll(filepath.Dir(to), 0o700)
				if err := os.WriteFile(to, data, info.Mode().Perm()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, part := range []struct {
		raw  json.RawMessage
		into string
	}{{entry.Skills, "skills"}, {entry.Commands, "commands"}, {entry.Agents, "agents"}} {
		if err := copyListed(part.raw, part.into); err != nil {
			return err
		}
	}
	manifest := map[string]any{}
	if data, err := os.ReadFile(filepath.Join(target, ".claude-plugin", "plugin.json")); err == nil {
		json.Unmarshal(data, &manifest)
	}
	for key, value := range map[string]string{"name": entry.Name, "description": entry.Description, "version": entry.Version} {
		if _, set := manifest[key]; !set && value != "" {
			manifest[key] = value
		}
	}
	for key, raw := range map[string]json.RawMessage{"hooks": entry.Hooks, "mcpServers": entry.MCPServers} {
		var path string
		if len(raw) == 0 {
			continue
		}
		if json.Unmarshal(raw, &path) == nil && lists {
			// A file of the source: copied beside the manifest.
			from := filepath.Join(source, filepath.FromSlash(strings.TrimPrefix(path, "./")))
			if data, err := os.ReadFile(from); err == nil {
				name := filepath.Base(from)
				os.WriteFile(filepath.Join(target, name), data, 0o600)
				manifest[key] = "./" + name
			}
			continue
		}
		manifest[key] = raw
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.MkdirAll(filepath.Join(target, ".claude-plugin"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(target, ".claude-plugin", "plugin.json"), data, 0o600)
}

// explain lists the folders, commands and risks for the confirmation.
func (plan *Plan) explain() {
	plan.Folders = []string{}
	plan.Commands = []string{}
	plan.Risks = []string{}
	if plan.Notes == nil {
		plan.Notes = []string{}
	}
	for _, skill := range plan.Skills {
		plan.Folders = append(plan.Folders, skill.Folder)
	}
	var hooks, mcp, scriptCount, commands, agents int
	for _, plugin := range plan.Plugins {
		plan.Folders = append(plan.Folders, plugin.Folder)
		for _, server := range plugin.MCP {
			plan.Commands = append(plan.Commands, "MCP server "+server.Name+": "+server.Run)
			mcp++
		}
		for _, hook := range plugin.Hooks {
			plan.Commands = append(plan.Commands, "Hook "+hook)
			hooks++
		}
		scriptCount += len(plugin.Scripts)
		commands += plugin.Commands
		agents += plugin.Agents
	}
	for _, skill := range plan.Skills {
		scriptCount += len(skill.Scripts)
	}
	plan.Folders = append(plan.Folders, registryPath())
	plan.Risks = append(plan.Risks, "It comes from github.com/"+plan.Repo+", which 1loop does not review. Install only what you trust.")
	if mcp > 0 {
		plan.Risks = append(plan.Risks, fmt.Sprintf("%s start on this computer, with your account's rights, as soon as the agent can use them.", plural(mcp, "MCP server")))
	}
	if hooks > 0 {
		plan.Risks = append(plan.Risks, fmt.Sprintf("%s run automatically around the agent's tools and messages, without asking.", plural(hooks, "hook command")))
	}
	if scriptCount > 0 {
		plan.Risks = append(plan.Risks, fmt.Sprintf("It includes %s the model may run through the shell, with your approval like any command.", plural(scriptCount, "script")))
	}
	plan.Risks = append(plan.Risks, "Skills are instructions the model follows: they can steer what it does in your projects.")
	if commands+agents > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf("%s and %s are kept but not offered in 1loop (no / commands yet); Claude Code sessions can use them.", plural(commands, "command"), plural(agents, "agent")))
	}
	for _, skill := range plan.Skills {
		if skill.Replaces {
			plan.Notes = append(plan.Notes, "The installed skill "+skill.Name+" is replaced.")
		}
	}
	for _, plugin := range plan.Plugins {
		if plugin.Replaces {
			plan.Notes = append(plan.Notes, "The installed plugin "+plugin.Name+" is replaced.")
		}
	}
}

// Install copies the planned skills and plugins the user kept (all of
// them when keep is empty), then forgets the plan.
func Install(planID string, keep []string) (Registry, error) {
	plansMu.Lock()
	plan := plans[planID]
	delete(plans, planID)
	plansMu.Unlock()
	if plan == nil {
		return Registry{}, fmt.Errorf("This installation expired: search again")
	}
	defer os.RemoveAll(plan.staged)
	defer os.RemoveAll(plan.staged + "-deps")
	defer os.RemoveAll(plan.staged + "-plugins")
	wanted := map[string]bool{}
	for _, id := range keep {
		wanted[id] = true
	}
	var registry Registry
	err := Update(func(current *Registry) error {
		now := time.Now()
		for i, source := range plan.sources {
			if len(wanted) > 0 && !wanted[source.id] {
				continue
			}
			switch source.kind {
			case "skill":
				skill := plan.Skills[skillIndex(plan, i)]
				if err := replaceDir(source.dir, skill.Folder); err != nil {
					return err
				}
				read, _ := ReadSkill(skill.Folder)
				entry := Skill{Name: skill.Name, Description: read.Description, Dir: skill.Folder, Source: plan.Repo, Enabled: true, Installed: now}
				current.Skills = replaceSkill(current.Skills, entry)
			case "plugin":
				planned := plan.Plugins[pluginIndex(plan, source.id)]
				if err := replaceDir(source.dir, planned.Folder); err != nil {
					return err
				}
				entry := Plugin{ID: planned.ID, Name: planned.Name, Description: planned.Description, Version: planned.Version,
					Repo: plan.Repo, Dir: planned.Folder, Enabled: true, Installed: now}
				current.Plugins = replacePlugin(current.Plugins, entry)
			}
		}
		registry = *current
		return nil
	})
	return registry, err
}

// Remove uninstalls a skill or a plugin: its folder and its entry.
func Remove(kind, id string) error {
	return Update(func(registry *Registry) error {
		switch kind {
		case "skill":
			for i, skill := range registry.Skills {
				if skill.Name == id {
					if err := removeInside(skill.Dir); err != nil {
						return err
					}
					registry.Skills = append(registry.Skills[:i], registry.Skills[i+1:]...)
					return nil
				}
			}
		case "plugin":
			for i, plugin := range registry.Plugins {
				if plugin.ID == id {
					if err := removeInside(plugin.Dir); err != nil {
						return err
					}
					registry.Plugins = append(registry.Plugins[:i], registry.Plugins[i+1:]...)
					return nil
				}
			}
		}
		return fmt.Errorf("Nothing installed is called %s", id)
	})
}

// removeInside deletes a folder of the extensions folder, never anything
// outside it.
func removeInside(dir string) error {
	rel, err := filepath.Rel(Dir(), dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("%s is not in %s: not removed", dir, Dir())
	}
	return os.RemoveAll(dir)
}

func replaceDir(from, to string) error {
	if err := removeInside(to); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	// Copied, not moved: a skill may be listed by several plugins.
	return copyTree(from, to)
}

// CopyDir copies a folder's files into to.
func CopyDir(from, to string) error { return copyTree(from, to) }

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func skillIndex(plan *Plan, source int) int {
	n := 0
	for i := 0; i < source; i++ {
		if plan.sources[i].kind == "skill" {
			n++
		}
	}
	return n
}

func pluginIndex(plan *Plan, id string) int {
	for i, plugin := range plan.Plugins {
		if plugin.ID == id {
			return i
		}
	}
	return 0
}

func replaceSkill(skills []Skill, skill Skill) []Skill {
	for i := range skills {
		if skills[i].Name == skill.Name {
			skills[i] = skill
			return skills
		}
	}
	return append(skills, skill)
}

func replacePlugin(plugins []Plugin, plugin Plugin) []Plugin {
	for i := range plugins {
		if plugins[i].ID == plugin.ID {
			plugins[i] = plugin
			return plugins
		}
	}
	return append(plugins, plugin)
}

// downloadRepo extracts a repository's default branch into dir.
func downloadRepo(ctx context.Context, repo, dir string) (int64, error) {
	response, err := githubRequest(ctx, githubLoad+"/"+repo+"/tar.gz/HEAD")
	if err != nil {
		return 0, fmt.Errorf("GitHub cannot be reached: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("%s is not a public GitHub repository", repo)
	}
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub answered %d for %s", response.StatusCode, repo)
	}
	unzipped, err := gzip.NewReader(io.LimitReader(response.Body, maxDownload))
	if err != nil {
		return 0, err
	}
	archive := tar.NewReader(unzipped)
	var total int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("%s: the download was cut: %w", repo, err)
		}
		// Every path is under one top folder (owner-repo-sha/): dropped.
		_, rest, found := strings.Cut(header.Name, "/")
		if !found || rest == "" {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rest))
		if rel, err := filepath.Rel(dir, target); err != nil || strings.HasPrefix(rel, "..") {
			continue // never outside the staging folder
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return 0, err
			}
		case tar.TypeReg:
			total += header.Size
			if total > maxDownload {
				return 0, fmt.Errorf("%s is larger than %d MB", repo, maxDownload>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return 0, err
			}
			mode := os.FileMode(0o600)
			if header.FileInfo().Mode().Perm()&0o111 != 0 {
				mode = 0o700
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return 0, err
			}
			_, err = io.Copy(out, archive)
			out.Close()
			if err != nil {
				return 0, err
			}
		} // links and devices are left out
	}
	return total, nil
}

var scriptExtension = regexp.MustCompile(`\.(sh|bash|zsh|py|js|mjs|cjs|ts|rb|pl|ps1|bat|cmd|exe)$`)

// scripts lists the files of dir a shell could run (by their extension or
// their executable bit), relative to it.
func scripts(dir string) []string {
	found := []string{}
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			if entry != nil && entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if scriptExtension.MatchString(strings.ToLower(entry.Name())) || info.Mode().Perm()&0o111 != 0 {
			rel, _ := filepath.Rel(dir, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// cleanStaging removes downloads older than half an hour.
func cleanStaging() {
	plansMu.Lock()
	defer plansMu.Unlock()
	for id, plan := range plans {
		if time.Since(plan.created) > 30*time.Minute {
			os.RemoveAll(plan.staged)
			os.RemoveAll(plan.staged + "-deps")
			os.RemoveAll(plan.staged + "-plugins")
			delete(plans, id)
		}
	}
}

var unsafeFolder = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(name string) string {
	name = strings.Trim(unsafeFolder.ReplaceAllString(name, "-"), "-.")
	if name == "" {
		name = "extension"
	}
	return name
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
