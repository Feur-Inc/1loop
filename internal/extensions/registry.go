// Package extensions holds what the user adds to 1loop's agent: Agent Skills,
// plugins in Claude Code's format, and MCP servers 1loop connects to as a
// client. Everything lives in one folder of the user's configuration:
//
//	~/.config/1loop/extensions/
//	  extensions.json        what is installed, enabled, and the MCP servers
//	  skills/<name>/SKILL.md  skills installed on their own
//	  plugins/<id>/           plugins, as their repository ships them
//
// 1loop's own agent and Claude Code both use them (see the agent package).
package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Skill is an Agent Skill: a folder with a SKILL.md whose front matter gives
// its name and what it is for.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Dir         string    `json:"dir"`
	Source      string    `json:"source,omitempty"` // the GitHub repository it came from
	Enabled     bool      `json:"enabled"`
	Installed   time.Time `json:"installed"`
	Plugin      string    `json:"plugin,omitempty"` // set when a plugin brings it (not stored)
}

// Plugin is a plugin in Claude Code's format: skills, MCP servers and hooks
// shipped together. Its commands and agents are kept but not offered: 1loop
// has no / commands yet.
type Plugin struct {
	ID          string    `json:"id"` // owner/repo, or owner/repo:name for a marketplace's plugin
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Version     string    `json:"version,omitempty"`
	Repo        string    `json:"repo"`
	Dir         string    `json:"dir"`
	Enabled     bool      `json:"enabled"`
	Installed   time.Time `json:"installed"`
}

// MCPServer is a server 1loop connects to: a command it starts (stdio) or a
// URL (Streamable HTTP).
type MCPServer struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"` // "stdio" or "http"
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled bool              `json:"enabled"`
	Plugin  string            `json:"plugin,omitempty"` // set when a plugin brings it (not stored)
}

// Registry is extensions.json.
type Registry struct {
	Skills  []Skill     `json:"skills"`
	Plugins []Plugin    `json:"plugins"`
	MCP     []MCPServer `json:"mcp"`
}

var (
	mu      sync.Mutex
	dirFunc = defaultDir
)

func defaultDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "extensions")
}

// Dir is the extensions folder.
func Dir() string { return dirFunc() }

// SetDir moves the folder (tests).
func SetDir(dir string) { dirFunc = func() string { return dir } }

func registryPath() string { return filepath.Join(Dir(), "extensions.json") }

// Load reads what is installed. A missing file is an empty registry.
func Load() (Registry, error) {
	mu.Lock()
	defer mu.Unlock()
	return load()
}

func load() (Registry, error) {
	var registry Registry
	data, err := os.ReadFile(registryPath())
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return registry, fmt.Errorf("%s: %w", registryPath(), err)
	}
	return registry, nil
}

func save(registry Registry) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	temp := registryPath() + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, registryPath())
}

// Update changes the registry under the lock and writes it.
func Update(change func(*Registry) error) error {
	mu.Lock()
	defer mu.Unlock()
	registry, err := load()
	if err != nil {
		return err
	}
	if err := change(&registry); err != nil {
		return err
	}
	return save(registry)
}

var mcpName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// CheckMCP says what is wrong with a server the user describes.
func CheckMCP(server MCPServer) error {
	if !mcpName.MatchString(server.Name) {
		return fmt.Errorf("Name it with letters, digits, - or _ (40 at most)")
	}
	switch server.Type {
	case "stdio":
		if strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("Enter the command that starts the server")
		}
	case "http":
		if !strings.HasPrefix(server.URL, "https://") && !strings.HasPrefix(server.URL, "http://") {
			return fmt.Errorf("Enter the server's http:// or https:// address")
		}
	default:
		return fmt.Errorf("Choose a command (stdio) or an address (http)")
	}
	return nil
}

// SaveMCP adds a server, or replaces the one with its name (previous names
// another one being renamed).
func SaveMCP(server MCPServer, previous string) error {
	if err := CheckMCP(server); err != nil {
		return err
	}
	server.Plugin = ""
	return Update(func(registry *Registry) error {
		at := -1
		for i, existing := range registry.MCP {
			if existing.Name == server.Name && existing.Name != previous {
				return fmt.Errorf("A server is already named %s", server.Name)
			}
			if existing.Name == previous || (previous == "" && existing.Name == server.Name) {
				at = i
			}
		}
		if at >= 0 {
			registry.MCP[at] = server
		} else {
			registry.MCP = append(registry.MCP, server)
		}
		return nil
	})
}

// RemoveMCP forgets a server the user added.
func RemoveMCP(name string) error {
	return Update(func(registry *Registry) error {
		kept := registry.MCP[:0]
		for _, server := range registry.MCP {
			if server.Name != name {
				kept = append(kept, server)
			}
		}
		registry.MCP = kept
		return nil
	})
}

// SetEnabled turns a skill, plugin or MCP server on or off. kind is
// "skill", "plugin" or "mcp".
func SetEnabled(kind, id string, enabled bool) error {
	return Update(func(registry *Registry) error {
		switch kind {
		case "skill":
			for i := range registry.Skills {
				if registry.Skills[i].Name == id {
					registry.Skills[i].Enabled = enabled
					return nil
				}
			}
		case "plugin":
			for i := range registry.Plugins {
				if registry.Plugins[i].ID == id {
					registry.Plugins[i].Enabled = enabled
					return nil
				}
			}
		case "mcp":
			for i := range registry.MCP {
				if registry.MCP[i].Name == id {
					registry.MCP[i].Enabled = enabled
					return nil
				}
			}
		}
		return fmt.Errorf("Nothing installed is called %s", id)
	})
}

// Active is what the agent uses: the enabled skills and MCP servers, those
// of the enabled plugins included, and the plugins' hooks files.
type Active struct {
	Skills  []Skill
	MCP     []MCPServer
	Plugins []Plugin
	Hooks   []PluginHooks
}

// PluginHooks is a plugin's hooks file and the folder its commands name as
// ${CLAUDE_PLUGIN_ROOT}.
type PluginHooks struct {
	Plugin string
	Root   string
	File   string
}

// Enabled reads what is active now.
func Enabled() Active {
	registry, err := Load()
	if err != nil {
		return Active{}
	}
	var active Active
	for _, skill := range registry.Skills {
		if skill.Enabled {
			active.Skills = append(active.Skills, skill)
		}
	}
	for _, server := range registry.MCP {
		if server.Enabled {
			active.MCP = append(active.MCP, server)
		}
	}
	for _, plugin := range registry.Plugins {
		if !plugin.Enabled {
			continue
		}
		active.Plugins = append(active.Plugins, plugin)
		contents := ReadPlugin(plugin.Dir)
		for _, skill := range contents.Skills {
			skill.Plugin, skill.Enabled = plugin.ID, true
			active.Skills = append(active.Skills, skill)
		}
		for _, server := range contents.MCP {
			server.Plugin, server.Enabled = plugin.ID, true
			active.MCP = append(active.MCP, server)
		}
		if contents.HooksFile != "" {
			active.Hooks = append(active.Hooks, PluginHooks{Plugin: plugin.ID, Root: plugin.Dir, File: contents.HooksFile})
		}
	}
	sort.SliceStable(active.Skills, func(i, j int) bool { return active.Skills[i].Name < active.Skills[j].Name })
	return active
}
