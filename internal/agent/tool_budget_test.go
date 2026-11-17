// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"1loop/internal/extensions"
)

// The tool list 1loop gives Claude Code, with every capability on and the
// GitHub connector's server as it answered (testdata/github_mcp_tools.json,
// its 118 tools of November 2026), stays within these budgets: the Context
// panel's "MCP tools" count and tokens as 1loop estimates them (#447).
const (
	claudeToolCountBudget = 32
	claudeToolTokenBudget = 8000
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
	definitions = append(definitions, githubDefinitionsList()...)
	for _, definition := range definitions {
		function := definition["function"].(map[string]any)
		name := function["name"].(string)
		if description := function["description"].(string); len(description) > 1000 {
			t.Errorf("%s: description of %d bytes, over 1000", name, len(description))
		}
		// A grouped GitHub tool stands for up to seventeen of the server's.
		limit := 450
		if githubOps(name) != nil {
			limit = 700
		}
		if tokens := schemaTokens([]map[string]any{definition}); tokens > limit {
			t.Errorf("%s: about %d tokens, over %d", name, tokens, limit)
		}
	}
}

func TestGitHubGroupedTools(t *testing.T) {
	tools := githubFixture(t)
	definitions := githubDefinitions(extensions.MCPServer{Name: "1loop-github-connector"}, tools)
	var names []string
	for _, definition := range definitions {
		names = append(names, definition["function"].(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "github_issues,github_pull_requests,github_code,github_actions" {
		t.Fatalf("GitHub tools: %v", names)
	}
	// Every operation runs a tool the server has.
	for _, name := range names {
		for operation, op := range githubOps(name) {
			if op.tool != "" && !slices.ContainsFunc(tools, func(tool extensions.MCPTool) bool { return tool.Name == op.tool }) {
				t.Errorf("%s %s runs %s, not on the server", name, operation, op.tool)
			}
		}
	}
	if len(githubDefinitions(extensions.MCPServer{}, nil)) != 0 {
		t.Error("tools offered without a server")
	}
}

func TestGitHubArguments(t *testing.T) {
	args := map[string]any{"operation": "read", "repository": "Feur-Inc/1loop", "number": 452.0, "per_page": 5.0, "title": "ignored"}
	got, err := githubArguments(githubPullOps["read"], args)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"owner": "Feur-Inc", "repo": "1loop", "pullNumber": 452.0, "perPage": 5.0, "method": "get"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got, _ = githubArguments(githubActionOps["jobs"], map[string]any{"repository": "a/b", "run_id": 7.0})
	if got["resource_id"] != 7.0 || got["method"] != "list_workflow_jobs" {
		t.Fatalf("jobs: %v", got)
	}
	if _, err := githubArguments(githubIssueOps["read"], map[string]any{"repository": "nope"}); err == nil {
		t.Fatal("a repository without owner passed")
	}
}

func TestGitHubAttachmentOperations(t *testing.T) {
	call := ToolCall{ID: "1"}
	call.Function.Name, call.Function.Arguments = githubPullsTool, `{"operation":"upload","repository":"a/b","files":[{"path":"shot.png"}]}`
	inner, ok := githubAttachmentCall(call)
	if !ok || inner.Function.Name != githubUploadTool || !strings.Contains(inner.Function.Arguments, `"shot.png"`) {
		t.Fatalf("upload: %v %v", inner, ok)
	}
	call.Function.Arguments = `{"operation":"read","number":1}`
	if _, ok := githubAttachmentCall(call); ok {
		t.Fatal("read taken for an attachment")
	}
	action, _ := otherAction(githubIssuesTool, `{"operation":"read","repository":"a/b","number":3}`)
	if action.target != "GitHub issues · read · a/b #3" {
		t.Fatalf("summary: %q", action.target)
	}
}
