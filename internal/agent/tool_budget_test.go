// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"1loop/internal/extensions"
)

// The tool list 1loop gives Claude Code, with every capability on and the
// GitHub connector's server as it answered (testdata/github_mcp_tools.json,
// its 118 tools of November 2026), stays within these budgets: the Context
// panel's "MCP tools" count and tokens as 1loop estimates them (#447).
const (
	claudeToolCountBudget = 52
	claudeToolTokenBudget = 13000
)

func githubFixture(t *testing.T) []extensions.MCPTool {
	t.Helper()
	data, err := os.ReadFile("testdata/github_mcp_tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved []struct {
		Name, Description string
		InputSchema       json.RawMessage
		ReadOnly          bool
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	tools := make([]extensions.MCPTool, len(saved))
	for i, tool := range saved {
		tools[i] = extensions.MCPTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}
		tools[i].Annotations.ReadOnly = tool.ReadOnly
	}
	return tools
}

// fullConfig turns every tool of a Claude Code chat on: a scratch chat that
// may move, with the panels, the phone, packages, settings, subchats, chats
// and memory.
func fullConfig() Config {
	handler := func(context.Context, string, json.RawMessage) (PanelToolResult, error) { return PanelToolResult{}, nil }
	return Config{Provider: "claudecode", Workspace: "/tmp/chat", Scratch: "/tmp/chat",
		Move:    &WorkspaceMove{Pick: func(context.Context, string) (string, error) { return "", nil }},
		Browser: handler, Terminal: handler, Phone: handler, Packages: handler,
		Settings: &SettingsAccess{}, Subchats: &SubchatAccess{}, Chats: &ChatAccess{},
		Preferences: func(context.Context, func(string) (string, error)) error { return nil },
		Todo:        &TodoState{}}
}

func claudeToolTokens(definitions []map[string]any) int {
	data, _ := json.Marshal(claudeMCPToolList(definitions))
	return (len(data) + 3) / 4
}

func TestClaudeToolListBudget(t *testing.T) {
	definitions := claudeMCPDefinitions(fullConfig())
	if !hasTool(definitions, "browser_tabs") || !hasTool(definitions, "phone_use") || !hasTool(definitions, "request_directory") || !hasTool(definitions, "chats") {
		t.Fatalf("the full configuration misses tools: %d", len(definitions))
	}
	own := claudeToolTokens(definitions)
	github := githubDefinitions(extensions.MCPServer{Name: "1loop-github-connector"}, githubFixture(t))
	definitions = append(definitions, github...)
	count, tokens := len(definitions), claudeToolTokens(definitions)
	t.Logf("Claude Code tools: %d, about %d tokens (1loop's own %d, GitHub %d tools %d)", count, tokens, own, len(github), tokens-own)
	if count > claudeToolCountBudget || tokens > claudeToolTokenBudget {
		t.Fatalf("Claude Code gets %d tools, about %d tokens: over the budget of %d tools, %d tokens", count, tokens, claudeToolCountBudget, claudeToolTokenBudget)
	}
}

// The budget would catch the GitHub connector's whole list coming back: the
// list before #447 gave every tool of the server, as it describes it.
func TestClaudeToolBudgetCatchesTheWholeGitHubList(t *testing.T) {
	tools := githubFixture(t)
	group := mcpToolGroup(extensions.MCPServer{Name: "1loop-github-connector"}, tools)
	definitions := append(claudeMCPDefinitions(fullConfig()), githubAttachmentTools()...)
	for i, definition := range group.tools() {
		definition["function"].(map[string]any)["name"] = "github_" + tools[i].Name
		definitions = append(definitions, definition)
	}
	if count, tokens := len(definitions), claudeToolTokens(definitions); count <= claudeToolCountBudget || tokens <= claudeToolTokenBudget {
		t.Fatalf("the whole GitHub list (%d tools, %d tokens) passes the budget", count, tokens)
	}
}

// Each of 1loop's own tools stays short, so a long description stands out.
func TestOwnToolDescriptionsAreShort(t *testing.T) {
	definitions := append(claudeMCPDefinitions(fullConfig()), coreToolDefinitions()...)
	definitions = append(definitions, githubAttachmentTools()...)
	for _, definition := range definitions {
		function := definition["function"].(map[string]any)
		name := function["name"].(string)
		if description := function["description"].(string); len(description) > 1000 {
			t.Errorf("%s: description of %d bytes, over 1000", name, len(description))
		}
		if tokens := schemaTokens([]map[string]any{definition}); tokens > 450 {
			t.Errorf("%s: about %d tokens, over 450", name, tokens)
		}
	}
}

func TestGitHubCoreAndMore(t *testing.T) {
	tools := githubFixture(t)
	definitions := githubDefinitions(extensions.MCPServer{Name: "1loop-github-connector"}, tools)
	names := map[string]bool{}
	for _, definition := range definitions {
		names[definition["function"].(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"github_issue_read", "github_pull_request_read", "github_actions_list", "github_get_job_logs", githubMoreTool, githubUploadTool, githubViewTool} {
		if !names[name] {
			t.Errorf("%s is missing", name)
		}
	}
	for name := range githubCoreTools {
		if !names["github_"+name] {
			t.Errorf("core tool %s is not on the server's list", name)
		}
	}
	if names["github_star_repository"] || names["github_create_automation"] {
		t.Error("a specialised tool is in the list")
	}
	list := githubToolList(tools, "")
	if !strings.Contains(list, "star_repository: ") || strings.Contains(list, "issue_read: ") {
		t.Errorf("github_more's list:\n%s", list)
	}
	if list := githubToolList(tools, "gist"); !strings.Contains(list, "create_gist") || strings.Contains(list, "star_repository") {
		t.Errorf("filtered list:\n%s", list)
	}
}

func TestCompactSchemaKeepsProperties(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"description": map[string]any{"type": "string", "description": strings.Repeat("word ", 80), "x-mcp-header": "d"},
		"x-name":      map[string]any{"type": "string"},
	}, "required": []any{"description"}}
	compact := compactSchema(schema).(map[string]any)
	properties := compact["properties"].(map[string]any)
	if _, ok := properties["x-name"]; !ok {
		t.Fatal("a property named x-name was dropped")
	}
	property := properties["description"].(map[string]any)
	if _, ok := property["x-mcp-header"]; ok || len(property["description"].(string)) > 125 {
		t.Fatalf("property not compacted: %v", property)
	}
	if got := shortText("One. Two three four five six seven eight nine ten.", 30); got != "One." && !strings.HasSuffix(got, "…") {
		t.Fatalf("shortText: %q", got)
	}
}

func TestGitHubMoreListsAndDescribes(t *testing.T) {
	tools := githubFixture(t)
	backend := &App{}
	run := func(arguments string) (string, error) {
		call := ToolCall{ID: "1"}
		call.Function.Name, call.Function.Arguments = githubMoreTool, arguments
		text, _, err := backend.callGitHubMore(context.Background(), Config{}, extensions.MCPServer{}, tools, call, func() {}, func(string, any) {})
		return text, err
	}
	if text, err := run(`{"operation":"list","query":"star"}`); err != nil || !strings.Contains(text, "star_repository") {
		t.Fatalf("list: %q %v", text, err)
	}
	if text, err := run(`{"operation":"describe","tool":"github_create_gist"}`); err != nil || !strings.Contains(text, `"filename"`) {
		t.Fatalf("describe: %q %v", text, err)
	}
	if _, err := run(`{"operation":"call","tool":"no_such_tool","arguments":{}}`); err == nil || !strings.Contains(err.Error(), "list them") {
		t.Fatalf("unknown tool: %v", err)
	}
}
