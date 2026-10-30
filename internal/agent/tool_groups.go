package agent

// Tools loaded on demand, as Codex defers its own: the browser, terminal and
// desktop tools (about 2000 tokens of schemas, plus their instructions) only
// reach the model once it asks for them with tool_search.
//
// With a ChatGPT account the groups are namespaces of deferred functions and
// the search is OpenAI's own: the request never changes, and the loaded
// tools are placed at the end of the context, so the cache holds. Other
// providers get a tool_search function: a group it loads joins the tool
// list from then on.

import (
	"encoding/json"
	"strings"

	"1loop/internal/extensions"
	"1loop/internal/githubconnector"
)

type toolGroup struct {
	name         string // the namespace (ChatGPT reserves plain names like "browser")
	summary      string // what the search matches
	instructions func(Config) string
	tools        func() []map[string]any
}

func toolGroups() []toolGroup {
	groups := []toolGroup{
		{"panel_browser", "Web browser tabs in this chat's side panel: open pages, read or screenshot them, click, type, run JavaScript, resize.", browserInstructions, browserToolDefinitions},
		{"panel_terminal", "Persistent terminals in this chat's side panel: run commands, type into programs, send keys, read output, capture the screen.", terminalInstructions, terminalToolDefinitions},
		{"local_phone", "Control the local Android phone through the user-authorized ADB connection: inspect the screen, take screenshots, tap, swipe, type, send keys or run Android shell commands.", phoneInstructions, phoneToolDefinitions},
		{"local_debian", "Install Debian packages needed for work in the phone's project, through the trusted installer.", func(Config) string {
			return "\n\nUse install_packages to add necessary Debian programs; project commands should only modify the selected project. Package installation uses the trusted installer and the current approval policy. It does not authorize Android or ADB access."
		}, packageToolDefinitions},
	}
	if definition := computerToolDefinition(); definition != nil {
		groups = append(groups, toolGroup{"desktop_computer", "Control the user's real desktop (screen, mouse, keyboard, windows) once the user shares it.", computerInstructions, func() []map[string]any { return []map[string]any{definition} }})
	}
	return groups
}

func (cfg Config) toolAvailable(name string) bool {
	if cfg.ToolAllowed != nil && !cfg.ToolAllowed(name) {
		return false
	}
	if name == "imagegen" {
		return cfg.Provider == "openai"
	}
	if isGitHubTool(name) {
		return githubconnector.Default.Available()
	}
	if name == "memory" { // off in the settings, or no preferences here
		return cfg.Preferences != nil
	}
	switch groupOfTool(name) {
	case "panel_browser":
		return cfg.Browser != nil
	case "panel_terminal":
		return cfg.Terminal != nil
	case "desktop_computer":
		return cfg.Computer != nil
	case "local_phone":
		return cfg.Phone != nil
	case "local_debian":
		return cfg.Packages != nil
	}
	return true
}

// availableToolGroups are the groups a chat may load: 1loop's, and the
// tools of the user's MCP servers (extensions.go).
func availableToolGroups(cfg Config) []toolGroup {
	groups := builtinToolGroups(cfg)
	for _, group := range mcpGroups(cfg) {
		var definitions []map[string]any
		for _, definition := range group.tools() {
			if cfg.toolAvailable(definition["function"].(map[string]any)["name"].(string)) {
				definitions = append(definitions, definition)
			}
		}
		if len(definitions) > 0 {
			group.tools = func() []map[string]any { return definitions }
			groups = append(groups, group)
		}
	}
	return groups
}

// builtinToolGroups are 1loop's own groups. Claude Code reaches the MCP
// servers by itself, and ChatGPT (Web) keeps the tools it was given.
func builtinToolGroups(cfg Config) []toolGroup {
	var groups []toolGroup
	candidates := toolGroups()
	if group := githubGroup(cfg); group != nil {
		candidates = append(candidates, *group)
	}
	for _, group := range candidates {
		var definitions []map[string]any
		for _, definition := range group.tools() {
			name := definition["function"].(map[string]any)["name"].(string)
			if cfg.toolAvailable(name) {
				definitions = append(definitions, definition)
			}
		}
		if len(definitions) > 0 {
			group.tools = func() []map[string]any { return definitions }
			groups = append(groups, group)
		}
	}
	return groups
}

// groupOfTool is the group a tool belongs to, "" for the core tools.
func groupOfTool(name string) string {
	if isGitHubTool(name) {
		return "github"
	}
	if extensions.IsToolName(name) {
		return mcpGroupOf(name)
	}
	for _, group := range toolGroups() {
		for _, definition := range group.tools() {
			if definition["function"].(map[string]any)["name"] == name {
				return group.name
			}
		}
	}
	return ""
}

func toolSearchInstructions(cfg Config) string {
	var names []string
	for _, group := range availableToolGroups(cfg) {
		names = append(names, group.name+" ("+group.summary+")")
	}
	if len(names) == 0 {
		return ""
	}
	return "\n\nMore tools load on demand with tool_search: " + strings.Join(names, "; ") + ". Load a group only when the task needs it; its instructions come with it."
}

// loadedGroups are the groups a conversation already uses: loaded by a
// tool_search call, or called (a conversation started on another model).
func loadedGroups(messages []Message) map[string]bool {
	loaded := map[string]bool{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == "tool_search" {
				var args struct{ Group string }
				if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil {
					loaded[args.Group] = true
				}
			} else if group := groupOfTool(call.Function.Name); group != "" {
				loaded[group] = true
			}
		}
	}
	return loaded
}

// chatTools is the tool list for the chat completions providers: the core
// tools, the loaded groups after them (a new group only appends), and
// tool_search while a group is left to load.
func chatTools(cfg Config, messages []Message) []map[string]any {
	tools := availableCoreTools(cfg)
	loaded := loadedGroups(messages)
	groups := availableToolGroups(cfg)
	var left []string
	for _, group := range groups {
		if !loaded[group.name] {
			left = append(left, group.name)
		}
	}
	if len(left) > 0 {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name":        "tool_search",
			"description": "Load a group of tools (and its instructions) for the rest of this chat.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{
				"group": map[string]any{"type": "string", "enum": left},
			}, "required": []string{"group"}, "additionalProperties": false},
		}})
	}
	for _, group := range groups {
		if loaded[group.name] {
			tools = append(tools, group.tools()...)
		}
	}
	return tools
}

// searchTools answers the client tool_search: the group's instructions and
// tool names; the tools themselves join the list on the next request.
func searchTools(cfg Config, arguments string) (string, error) {
	var args struct{ Group string }
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	for _, group := range availableToolGroups(cfg) {
		if group.name != args.Group {
			continue
		}
		var names []string
		for _, definition := range group.tools() {
			names = append(names, definition["function"].(map[string]any)["name"].(string))
		}
		return "Loaded " + group.name + ": " + strings.Join(names, ", ") + "." + group.instructions(cfg), nil
	}
	return "", errUnknownGroup(args.Group)
}

// strictTool: 1loop's tools follow OpenAI's strict schemas; an MCP server's
// say "strict": false.
func strictTool(function map[string]any) bool {
	if strict, ok := function["strict"].(bool); ok {
		return strict
	}
	return true
}

type errUnknownGroup string

func (e errUnknownGroup) Error() string { return "No tool group named " + string(e) }

// responsesFunctions are plain function definitions in the Responses shape.
func responsesFunctions(definitions []map[string]any) []map[string]any {
	var tools []map[string]any
	for _, definition := range definitions {
		f := definition["function"].(map[string]any)
		tools = append(tools, map[string]any{"type": "function", "name": f["name"], "description": f["description"], "parameters": f["parameters"], "strict": strictTool(f)})
	}
	return tools
}

// responsesTools is the tool list for ChatGPT: the core functions, each
// group as a namespace of deferred functions, OpenAI's tool search and its
// web search.
func responsesTools(cfg Config) []map[string]any {
	var tools []map[string]any
	function := func(definition map[string]any, deferred bool) map[string]any {
		f := definition["function"].(map[string]any)
		tool := map[string]any{"type": "function", "name": f["name"], "description": f["description"], "parameters": f["parameters"], "strict": strictTool(f)}
		if deferred {
			tool["defer_loading"] = true
		}
		return tool
	}
	for _, definition := range availableCoreTools(cfg) {
		tools = append(tools, function(definition, false))
	}
	groups := availableToolGroups(cfg)
	for _, group := range groups {
		var members []map[string]any
		for _, definition := range group.tools() {
			members = append(members, function(definition, true))
		}
		tools = append(tools, map[string]any{"type": "namespace", "name": group.name, "description": group.summary + group.instructions(cfg), "tools": members})
	}
	if len(groups) > 0 {
		tools = append(tools, map[string]any{"type": "tool_search"})
	}
	return append(tools, map[string]any{"type": "web_search"})
}
