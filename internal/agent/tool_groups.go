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
	}
	if definition := computerToolDefinition(); definition != nil {
		groups = append(groups, toolGroup{"desktop_computer", "Control the user's real desktop (screen, mouse, keyboard, windows) once the user shares it.", computerInstructions, func() []map[string]any { return []map[string]any{definition} }})
	}
	return groups
}

// groupOfTool is the group a tool belongs to, "" for the core tools.
func groupOfTool(name string) string {
	for _, group := range toolGroups() {
		for _, definition := range group.tools() {
			if definition["function"].(map[string]any)["name"] == name {
				return group.name
			}
		}
	}
	return ""
}

func toolSearchInstructions() string {
	var names []string
	for _, group := range toolGroups() {
		names = append(names, group.name+" ("+group.summary+")")
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
func chatTools(messages []Message) []map[string]any {
	tools := coreToolDefinitions()
	loaded := loadedGroups(messages)
	var left []string
	for _, group := range toolGroups() {
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
	for _, group := range toolGroups() {
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
	for _, group := range toolGroups() {
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

type errUnknownGroup string

func (e errUnknownGroup) Error() string { return "No tool group named " + string(e) }

// responsesTools is the tool list for ChatGPT: the core functions, each
// group as a namespace of deferred functions, OpenAI's tool search and its
// web search.
func responsesTools(cfg Config) []map[string]any {
	var tools []map[string]any
	function := func(definition map[string]any, deferred bool) map[string]any {
		f := definition["function"].(map[string]any)
		tool := map[string]any{"type": "function", "name": f["name"], "description": f["description"], "parameters": f["parameters"], "strict": true}
		if deferred {
			tool["defer_loading"] = true
		}
		return tool
	}
	for _, definition := range coreToolDefinitions() {
		tools = append(tools, function(definition, false))
	}
	for _, group := range toolGroups() {
		var members []map[string]any
		for _, definition := range group.tools() {
			members = append(members, function(definition, true))
		}
		tools = append(tools, map[string]any{"type": "namespace", "name": group.name, "description": group.summary + group.instructions(cfg), "tools": members})
	}
	return append(tools, map[string]any{"type": "tool_search"}, map[string]any{"type": "web_search"})
}
