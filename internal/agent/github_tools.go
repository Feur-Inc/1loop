// SPDX-License-Identifier: MPL-2.0

package agent

// The GitHub connector's server has over a hundred tools (about 35000
// tokens of schemas). A chat gets the ones coding work uses, with shorter
// descriptions, and reaches all the others through github_more: listed,
// described and called on demand (#447).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"1loop/internal/core"
	"1loop/internal/extensions"
)

const githubMoreTool = "github_more"

// githubCoreTools are the server's tools a chat gets as tools of their own:
// issues, pull requests and their reviews, files and commits, and Actions.
var githubCoreTools = map[string]bool{
	"issue_read": true, "issue_write": true, "add_issue_comment": true, "search_issues": true,
	"pull_request_read": true, "create_pull_request": true, "update_pull_request": true, "search_pull_requests": true,
	"merge_pull_request": true, "pull_request_review_write": true, "add_comment_to_pending_review": true, "add_reply_to_pull_request_comment": true,
	"get_file_contents": true, "search_code": true, "list_commits": true, "get_commit": true, "list_branches": true,
	"actions_list": true, "actions_get": true, "get_job_logs": true, "actions_run_trigger": true,
}

// githubDefinitions are the connector's definitions for a chat: the
// attachment tools, the core tools in short, and github_more.
func githubDefinitions(server extensions.MCPServer, tools []extensions.MCPTool) []map[string]any {
	definitions := githubAttachmentTools()
	group := mcpToolGroup(server, tools)
	for i, definition := range group.tools() {
		if !githubCoreTools[tools[i].Name] {
			continue
		}
		function := definition["function"].(map[string]any)
		function["name"] = "github_" + tools[i].Name
		function["description"] = shortText(function["description"].(string), 240)
		function["parameters"] = compactSchema(function["parameters"])
		definitions = append(definitions, definition)
	}
	return append(definitions, githubMoreDefinition())
}

func githubMoreDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        githubMoreTool,
		"description": "The GitHub connector's other tools (repositories, releases, labels, discussions, gists, notifications, projects, stars, teams, security alerts, Copilot, automations…): list them, describe one, or call it.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"operation": map[string]any{"type": "string", "enum": []string{"list", "describe", "call"}},
			"query":     map[string]any{"type": "string", "description": "list: words to filter by."},
			"tool":      map[string]any{"type": "string", "description": "describe, call: the tool's name."},
			"arguments": map[string]any{"type": "object", "description": "call: the tool's arguments."},
		}, "required": []string{"operation"}},
		"strict": false, // arguments are the called tool's
	}}
}

// callGitHubMore answers github_more: a list of the other tools, one's
// schema, or its call, approved like the tool itself.
func (backend *App) callGitHubMore(ctx context.Context, cfg Config, server extensions.MCPServer, tools []extensions.MCPTool, call ToolCall, begin func(), emit emitter) (string, []core.ImagePart, error) {
	var args struct {
		Operation, Query, Tool string
		Arguments              json.RawMessage
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "", nil, err
	}
	if args.Operation == "list" {
		return githubToolList(tools, args.Query), nil, nil
	}
	name := strings.TrimPrefix(strings.TrimPrefix(args.Tool, "mcp__1loop_todo__"), "github_")
	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		switch args.Operation {
		case "describe":
			schema := tool.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			return tool.Name + ": " + strings.TrimSpace(tool.Description) + "\nParameters: " + string(schema), nil, nil
		case "call":
			inner := ToolCall{ID: call.ID}
			inner.Function.Name = "github_" + tool.Name
			inner.Function.Arguments = "{}"
			if len(args.Arguments) > 0 && string(args.Arguments) != "null" {
				inner.Function.Arguments = string(args.Arguments)
			}
			text, images, err := backend.callMCPTool(ctx, cfg, server, tool, inner, begin, emit)
			return text, images, githubAuthorizationError(ctx, server, err)
		}
		return "", nil, fmt.Errorf("operation is list, describe or call")
	}
	return "", nil, fmt.Errorf("No GitHub tool named %q; list them with operation list", args.Tool)
}

// githubToolList names the tools outside the core with their first
// sentence, those matching every word of query.
func githubToolList(tools []extensions.MCPTool, query string) string {
	words := strings.Fields(strings.ToLower(query))
	var lines []string
	for _, tool := range tools {
		if githubCoreTools[tool.Name] || "github_"+tool.Name == githubUploadTool || "github_"+tool.Name == githubViewTool {
			continue
		}
		line := tool.Name + ": " + shortText(tool.Description, 120)
		if tool.Annotations.ReadOnly {
			line += " (read-only)"
		}
		matched := true
		for _, word := range words {
			matched = matched && strings.Contains(strings.ToLower(line), word)
		}
		if matched {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "No other GitHub tool matches " + query
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// shortText is text on one line, cut at the last sentence or word that fits
// in limit bytes.
func shortText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if end := strings.LastIndex(cut, ". "); end > limit/3 {
		return cut[:end+1]
	}
	if end := strings.LastIndex(cut, " "); end > 0 {
		cut = cut[:end]
	}
	return strings.TrimRight(cut, ",;:") + "…"
}

// compactSchema shortens a server's schema: property descriptions in one
// line of at most 120 bytes, no enum of more than 12 values (GitHub's event
// names, sort keys or result fields, which the model knows or the server
// checks), and none of the server's own x- keywords.
func compactSchema(value any) any {
	switch node := value.(type) {
	case map[string]any:
		compact := make(map[string]any, len(node))
		for key, child := range node {
			if strings.HasPrefix(key, "x-") || key == "$schema" {
				continue
			}
			if text, ok := child.(string); ok && key == "description" {
				compact[key] = shortText(text, 120)
				continue
			}
			if values, ok := child.([]any); ok && key == "enum" && len(values) > 12 {
				continue
			}
			if key == "properties" {
				// Property names are not keywords: keep them all, whatever
				// they are called.
				properties := map[string]any{}
				if children, ok := child.(map[string]any); ok {
					for name, property := range children {
						properties[name] = compactSchema(property)
					}
				}
				compact[key] = properties
				continue
			}
			compact[key] = compactSchema(child)
		}
		return compact
	case []any:
		compact := make([]any, len(node))
		for i, child := range node {
			compact[i] = compactSchema(child)
		}
		return compact
	}
	return value
}
