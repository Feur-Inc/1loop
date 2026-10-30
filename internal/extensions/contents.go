package extensions

// Reading skills and plugins from their folders, as Claude Code lays them out.

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReadSkill reads a skill's SKILL.md front matter:
//
//	---
//	name: pdf
//	description: Extract text and tables from PDF files…
//	---
//
// A skill without a name takes its folder's. The front matter is YAML
// (a description is often a folded "description: >" block); one that is not
// valid YAML is read line by line.
func ReadSkill(dir string) (Skill, bool) {
	file, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, false
	}
	defer file.Close()
	skill := Skill{Name: filepath.Base(dir), Dir: dir}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var lines []string
	for line := 0; scanner.Scan() && line < 200; line++ {
		text := strings.TrimRight(scanner.Text(), "\r")
		if line == 0 {
			if strings.TrimSpace(text) != "---" {
				return skill, true
			}
			continue
		}
		if strings.TrimSpace(text) == "---" {
			break
		}
		lines = append(lines, text)
	}
	var front struct{ Name, Description any }
	if yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &front) == nil {
		if name, ok := front.Name.(string); ok && strings.TrimSpace(name) != "" {
			skill.Name = strings.TrimSpace(name)
		}
		if description, ok := front.Description.(string); ok {
			skill.Description = strings.TrimSpace(description)
		}
		return skill, true
	}
	for _, text := range lines {
		key, value, found := strings.Cut(text, ":")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "name":
			if value != "" {
				skill.Name = value
			}
		case "description":
			skill.Description = value
		}
	}
	return skill, true
}

// FindSkills lists the skill folders under root (a SKILL.md in each), the
// deepest first excluded: a skill's own subfolders are its files.
func FindSkills(root string) []Skill {
	var skills []Skill
	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		dir := filepath.Dir(path)
		if skill, ok := ReadSkill(dir); ok {
			skills = append(skills, skill)
		}
		return filepath.SkipDir // the rest of this folder belongs to the skill
	})
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// PluginManifest is .claude-plugin/plugin.json.
type PluginManifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	Skills      json.RawMessage `json:"skills"`
	MCPServers  json.RawMessage `json:"mcpServers"`
	Hooks       json.RawMessage `json:"hooks"`
}

// PluginContents is what a plugin folder holds.
type PluginContents struct {
	Manifest  PluginManifest
	Skills    []Skill
	MCP       []MCPServer
	HooksFile string   // hooks/hooks.json, or the manifest's
	Hooks     []string // the hook commands, for the confirmation
	Commands  int      // commands/*.md: kept, not offered (no / commands yet)
	Agents    int      // agents/*.md: kept, not offered
}

// IsPlugin tells whether dir holds a plugin.
func IsPlugin(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	return err == nil
}

// ReadPlugin reads a plugin folder: its manifest, skills, MCP servers
// (.mcp.json or the manifest's mcpServers) and hooks.
func ReadPlugin(dir string) PluginContents {
	var contents PluginContents
	if data, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
		json.Unmarshal(data, &contents.Manifest)
	}
	if contents.Manifest.Name == "" {
		contents.Manifest.Name = filepath.Base(dir)
	}
	contents.Skills = FindSkills(filepath.Join(dir, "skills"))
	servers := contents.Manifest.MCPServers
	if path := pathIn(dir, servers); path != "" { // "mcpServers": "./.mcp.json"
		servers, _ = os.ReadFile(path)
	} else if len(servers) == 0 {
		servers, _ = os.ReadFile(filepath.Join(dir, ".mcp.json"))
	}
	contents.MCP = parseMCPServers(servers, dir)
	hooks := filepath.Join(dir, "hooks", "hooks.json")
	if path := pathIn(dir, contents.Manifest.Hooks); path != "" {
		hooks = path
	}
	if data, err := os.ReadFile(hooks); err == nil {
		contents.HooksFile = hooks
		contents.Hooks = hookCommands(data, dir)
	}
	contents.Commands = countMarkdown(filepath.Join(dir, "commands"))
	contents.Agents = countMarkdown(filepath.Join(dir, "agents"))
	return contents
}

// pathIn is a manifest field naming a file of the plugin ("./hooks.json"),
// "" when it holds the value itself.
func pathIn(dir string, raw json.RawMessage) string {
	var path string
	if json.Unmarshal(raw, &path) != nil || path == "" {
		return ""
	}
	full := filepath.Join(dir, filepath.FromSlash(path))
	if rel, err := filepath.Rel(dir, full); err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return full
}

// parseMCPServers reads {"mcpServers": {...}} or the map itself, with the
// plugin's folder for ${CLAUDE_PLUGIN_ROOT}, in the text and in the
// environment.
func parseMCPServers(data []byte, root string) []MCPServer {
	var wrapped struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	servers := map[string]json.RawMessage{}
	if json.Unmarshal(data, &wrapped) == nil && len(wrapped.MCPServers) > 0 {
		servers = wrapped.MCPServers
	} else {
		json.Unmarshal(data, &servers)
	}
	var out []MCPServer
	for name, raw := range servers {
		var entry struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		server := MCPServer{Name: name, Command: expandRoot(entry.Command, root), URL: entry.URL, Headers: entry.Headers, Env: map[string]string{}}
		for _, arg := range entry.Args {
			server.Args = append(server.Args, expandRoot(arg, root))
		}
		for key, value := range entry.Env {
			server.Env[key] = expandRoot(value, root)
		}
		if _, set := server.Env["CLAUDE_PLUGIN_ROOT"]; !set && root != "" {
			server.Env["CLAUDE_PLUGIN_ROOT"] = root // as Claude Code gives it: servers find their files with it
		}
		switch {
		case server.Command != "":
			server.Type = "stdio"
		case entry.URL != "" && entry.Type != "sse":
			server.Type = "http"
		default:
			continue // SSE-only servers are not supported
		}
		out = append(out, server)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func expandRoot(value, root string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "${CLAUDE_PLUGIN_ROOT}", root), "$CLAUDE_PLUGIN_ROOT", root)
}

// hookCommands lists a hooks file's commands, for the confirmation.
func hookCommands(data []byte, root string) []string {
	var file struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	var commands []string
	events := make([]string, 0, len(file.Hooks))
	for event := range file.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		for _, matcher := range file.Hooks[event] {
			for _, hook := range matcher.Hooks {
				if strings.TrimSpace(hook.Command) != "" {
					commands = append(commands, event+": "+expandRoot(hook.Command, root))
				}
			}
		}
	}
	return commands
}

func countMarkdown(dir string) int {
	entries, _ := os.ReadDir(dir)
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			count++
		}
	}
	return count
}
