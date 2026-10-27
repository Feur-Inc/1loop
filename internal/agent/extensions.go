package agent

// The user's extensions (internal/extensions) in the agent: skills it loads
// with the skill tool, each MCP server's tools as a group that loads on
// demand, and the plugins' hooks. Claude Code gets the same through its own
// options (claudeExtensionArgs).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"1loop/internal/core"
	"1loop/internal/extensions"
)

// skillToolDefinition is offered while skills are installed and enabled.
func skillToolDefinition() map[string]any {
	skills := extensions.Enabled().Skills
	if len(skills) == 0 {
		return nil
	}
	var names []string
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "skill",
		"description": "Load an installed skill: its SKILL.md instructions, or another file of the skill (file, relative to its folder). Load a skill as soon as the task matches its description, before working on it.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"name": map[string]any{"type": "string", "enum": names},
			"file": map[string]any{"type": []string{"string", "null"}, "description": "A file of the skill, e.g. reference/forms.md; null for SKILL.md."},
		}, "required": []string{"name", "file"}, "additionalProperties": false},
	}}
}

// skillInstructions list the skills in the system prompt: their name and
// description only, the rest loads with the skill tool.
func skillInstructions(cfg Config) string {
	if !cfg.toolAvailable("skill") {
		return ""
	}
	skills := extensions.Enabled().Skills
	if len(skills) == 0 {
		return ""
	}
	var lines []string
	for _, skill := range skills {
		lines = append(lines, "- "+skill.Name+": "+strings.TrimSpace(skill.Description))
	}
	return "\n\nSkills the user installed (load one with the skill tool when the task matches it; its scripts run with shell from the folder skill gives):\n" + strings.Join(lines, "\n")
}

// loadSkill answers the skill tool.
func loadSkill(arguments string) (string, error) {
	var args struct {
		Name string  `json:"name"`
		File *string `json:"file"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	for _, skill := range extensions.Enabled().Skills {
		if skill.Name != args.Name {
			continue
		}
		name := "SKILL.md"
		if args.File != nil && strings.TrimSpace(*args.File) != "" {
			name = filepath.FromSlash(strings.TrimSpace(*args.File))
		}
		path := filepath.Join(skill.Dir, name)
		if rel, err := filepath.Rel(skill.Dir, path); err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("%s is not a file of the skill", name)
		}
		text, err := fileText(path)
		if err != nil {
			return "", err
		}
		out := "Skill " + skill.Name + ", folder " + skill.Dir + "\n\n" + text
		if name == "SKILL.md" {
			if files := skillFiles(skill.Dir); len(files) > 0 {
				out += "\n\nIts other files: " + strings.Join(files, ", ")
			}
		}
		return out, nil
	}
	return "", fmt.Errorf("No enabled skill is named %s", args.Name)
}

func skillFiles(dir string) []string {
	var files []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || len(files) >= 60 {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if rel != "SKILL.md" {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// mcpGroupName is a server's tool group.
func mcpGroupName(server string) string {
	return "mcp_" + strings.Trim(extensions.ToolName(server, "")[len("mcp__"):], "_")
}

// mcpGroupOf is the group of an MCP tool name (mcp__server__tool).
func mcpGroupOf(name string) string {
	rest := strings.TrimPrefix(name, "mcp__")
	server, _, _ := strings.Cut(rest, "__")
	return "mcp_" + server
}

// mcpGroups are the enabled MCP servers' tools, a group each. A server that
// does not answer is left out of this request.
func mcpGroups(cfg Config) []toolGroup {
	var groups []toolGroup
	for _, server := range extensions.Enabled().MCP {
		server := server
		tools, err := extensions.Servers.Tools(context.Background(), server, mcpDir(cfg))
		if err != nil || len(tools) == 0 {
			continue
		}
		var definitions []map[string]any
		var names []string
		for _, tool := range tools {
			names = append(names, tool.Name)
			parameters := map[string]any{"type": "object", "properties": map[string]any{}}
			if len(tool.InputSchema) > 0 {
				json.Unmarshal(tool.InputSchema, &parameters)
			}
			definitions = append(definitions, map[string]any{"type": "function", "function": map[string]any{
				"name":        extensions.ToolName(server.Name, tool.Name),
				"description": strings.TrimSpace(tool.Description),
				"parameters":  parameters,
				"strict":      false, // a server's schemas are not OpenAI's strict subset
			}})
		}
		summary := "Tools of the MCP server " + server.Name
		if server.Plugin != "" {
			summary += " (plugin " + server.Plugin + ")"
		}
		summary += ": " + strings.Join(names, ", ") + "."
		groups = append(groups, toolGroup{name: mcpGroupName(server.Name), summary: summary,
			instructions: func(Config) string { return "" },
			tools:        func() []map[string]any { return definitions }})
	}
	return groups
}

func mcpDir(cfg Config) string {
	if cfg.Workspace != "" {
		return cfg.Workspace
	}
	home, _ := os.UserHomeDir()
	return home
}

// callMCP runs an MCP server's tool: asked first, unless the server says
// it only reads.
func (backend *App) callMCP(ctx context.Context, cfg Config, call ToolCall, begin func(), emit emitter) (output string, images []core.ImagePart, err error) {
	for _, server := range extensions.Enabled().MCP {
		tools, connectErr := extensions.Servers.Tools(ctx, server, mcpDir(cfg))
		if connectErr != nil {
			continue
		}
		for _, tool := range tools {
			if extensions.ToolName(server.Name, tool.Name) != call.Function.Name {
				continue
			}
			if !tool.Annotations.ReadOnly && !backend.confirm(ctx, emit, "Use "+server.Name+" · "+tool.Name+"?", call.Function.Arguments) {
				return "", nil, fmt.Errorf("The user denied this MCP tool call")
			}
			begin()
			result, err := extensions.Servers.Call(ctx, server, mcpDir(cfg), tool.Name, json.RawMessage(call.Function.Arguments))
			if err != nil {
				return "", nil, err
			}
			var parts []string
			for _, content := range result.Content {
				switch content.Type {
				case "text":
					parts = append(parts, content.Text)
				case "image":
					if _, decodeErr := base64.StdEncoding.DecodeString(content.Data); decodeErr == nil {
						images = append(images, core.ImagePart{Mime: content.MimeType, Data: content.Data})
					}
				case "resource":
					if content.Resource != nil {
						parts = append(parts, content.Resource.URI+"\n"+content.Resource.Text)
					}
				}
			}
			output = strings.Join(parts, "\n")
			if result.IsError {
				return output, images, fmt.Errorf("%s", strings.TrimSpace(output))
			}
			return output, images, nil
		}
	}
	return "", nil, fmt.Errorf("The MCP server of %s is not connected", call.Function.Name)
}

// claudeExtensionArgs gives Claude Code the user's extensions for this
// session, its own settings untouched: the MCP servers the user added
// (--mcp-config), the enabled plugins and the skills installed on their own
// (--plugin-dir; those skills through a plugin 1loop writes).
func claudeExtensionArgs() (args []string, cleanup func()) {
	cleanup = func() {}
	active := extensions.Enabled()
	servers := map[string]any{}
	for _, server := range active.MCP {
		if server.Plugin != "" {
			continue // Claude Code starts a plugin's servers itself
		}
		entry := map[string]any{}
		if server.Type == "http" {
			entry["type"], entry["url"] = "http", server.URL
			if len(server.Headers) > 0 {
				entry["headers"] = server.Headers
			}
		} else {
			entry["command"], entry["args"] = server.Command, server.Args
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		}
		servers[server.Name] = entry
	}
	var files []string
	if len(servers) > 0 {
		data, _ := json.Marshal(map[string]any{"mcpServers": servers})
		if file, err := os.CreateTemp("", "1loop-mcp-*.json"); err == nil {
			file.Write(data)
			file.Close()
			files = append(files, file.Name())
			args = append(args, "--mcp-config", file.Name())
		}
	}
	for _, plugin := range active.Plugins {
		args = append(args, "--plugin-dir", plugin.Dir)
	}
	var own []extensions.Skill
	for _, skill := range active.Skills {
		if skill.Plugin == "" {
			own = append(own, skill)
		}
	}
	if len(own) > 0 {
		if dir, err := os.MkdirTemp("", "1loop-skills-"); err == nil {
			files = append(files, dir)
			manifest := filepath.Join(dir, ".claude-plugin", "plugin.json")
			os.MkdirAll(filepath.Dir(manifest), 0o700)
			os.WriteFile(manifest, []byte(`{"name":"1loop-skills","description":"The skills installed in 1loop"}`), 0o600)
			os.MkdirAll(filepath.Join(dir, "skills"), 0o700)
			for _, skill := range own {
				link := filepath.Join(dir, "skills", filepath.Base(skill.Dir))
				if os.Symlink(skill.Dir, link) != nil {
					extensions.CopyDir(skill.Dir, link) // Windows lets only some accounts make links
				}
			}
			args = append(args, "--plugin-dir", dir)
		}
	}
	cleanup = func() {
		for _, path := range files {
			os.RemoveAll(path) // a temporary file or folder of its own: the skills' links or copies only
		}
	}
	return args, cleanup
}

// pluginHookFiles are the enabled plugins' hooks, with the folder their
// commands call ${CLAUDE_PLUGIN_ROOT}.
func pluginHookFiles() []extensions.PluginHooks {
	return extensions.Enabled().Hooks
}

// claudeToolNames are Claude Code's names of 1loop's tools: a plugin's hooks
// are written for them.
var claudeToolNames = map[string][]string{
	"shell":          {"Bash"},
	"edit_file":      {"Edit", "Write", "MultiEdit"},
	"apply_patch":    {"Edit", "Write", "MultiEdit"},
	"read_file":      {"Read"},
	"list_directory": {"LS", "Glob"},
}
