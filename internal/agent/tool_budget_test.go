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
	definitions := claudeMCPDefinitions(fullConfig())
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

// Outside Claude Code, a turn starts with the core tools, update_todo and
// wrap_up: 1loop's other tools wait in the app_1loop group.
func TestAppToolsLoadOnDemand(t *testing.T) {
	cfg := fullConfig()
	cfg.Provider = "openrouter"
	names := map[string]bool{}
	var search map[string]any
	for _, definition := range chatTools(cfg, nil) {
		function := definition["function"].(map[string]any)
		names[function["name"].(string)] = true
		if function["name"] == "tool_search" {
			search = function
		}
	}
	for _, name := range []string{"shell", "read_file", "update_todo", "wrap_up", "background_task"} {
		if !names[name] {
			t.Errorf("%s is not loaded", name)
		}
	}
	for _, name := range []string{"memory", "settings", "subchat", "chats", "request_permissions"} {
		if names[name] {
			t.Errorf("%s is loaded before tool_search", name)
		}
	}
	if search == nil || !strings.Contains(fmt.Sprint(search["parameters"]), appToolGroup) {
		t.Fatalf("tool_search does not offer %s: %v", appToolGroup, search)
	}
	text, err := searchTools(cfg, `{"group":"`+appToolGroup+`"}`)
	if err != nil || !strings.Contains(text, "memory") || !strings.Contains(text, "Long-term memory") {
		t.Fatalf("tool_search %s: %q %v", appToolGroup, text, err)
	}
	call := ToolCall{}
	call.Function.Name, call.Function.Arguments = "tool_search", `{"group":"`+appToolGroup+`"}`
	loaded := []Message{{Role: "assistant", ToolCalls: []ToolCall{call}}}
	names = map[string]bool{}
	for _, definition := range chatTools(cfg, loaded) {
		names[definition["function"].(map[string]any)["name"].(string)] = true
	}
	if !names["memory"] || !names["subchat"] {
		t.Fatal("the app_1loop tools are missing once loaded")
	}
	if strings.Contains(systemInstructions(cfg), "Long-term memory") {
		t.Error("the memory instructions are still in the system prompt")
	}
}

func TestLongRunCompactionDefault(t *testing.T) {
	for _, cfg := range []Config{{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash-1017"}, {Provider: "compatible", Model: "local", BaseURL: "http://127.0.0.1:8000/v1"}} {
		if limit, _ := CompactDefault(cfg); limit != longRunDefault {
			t.Errorf("%s %s: %d", cfg.Provider, cfg.Model, limit)
		}
	}
	if limit, _ := CompactDefault(Config{Provider: "openrouter", Model: "anthropic/claude-haiku-5.5"}); limit != 100000 {
		t.Errorf("Haiku through OpenRouter: %d", limit)
	}
	if limit, _ := CompactDefault(Config{Provider: "claudecode", Model: "sonnet"}); limit != 0 {
		t.Errorf("Claude Code: %d", limit)
	}
}

func TestOpenRouterSessionID(t *testing.T) {
	body := map[string]any{}
	openRouterRouting(Config{Provider: "openrouter", ContextKey: "chat-1"}, body)
	id, _ := body["session_id"].(string)
	if !strings.HasPrefix(id, "1loop-") || len(id) > 256 {
		t.Fatalf("session_id: %q", id)
	}
	if _, ok := body["provider"]; ok {
		t.Fatal("a provider order without routing set")
	}
	other := map[string]any{}
	openRouterRouting(Config{Provider: "compatible", ContextKey: "chat-1"}, other)
	if len(other) != 0 {
		t.Fatalf("set for another provider: %v", other)
	}
}

// ChatGPT (Web) gets each tool once, the app_1loop ones included, with
// their instructions in its prompt.
func TestWebToolsOnce(t *testing.T) {
	for _, phone := range []bool{false, true} {
		seen := map[string]bool{}
		for _, definition := range webToolDefinitions(phone) {
			name := definition["function"].(map[string]any)["name"].(string)
			if seen[name] {
				t.Errorf("phone %v: %s twice", phone, name)
			}
			seen[name] = true
		}
		if !seen["memory"] || !seen["chats"] {
			t.Errorf("phone %v: memory or chats missing", phone)
		}
	}
	cfg := fullConfig()
	cfg.Provider = "chatgptweb"
	if prompt := webSystemInstructions(cfg, false); !strings.Contains(prompt, "Long-term memory") || !strings.Contains(prompt, "Other chats") {
		t.Error("the web prompt lost the memory or chats instructions")
	}
}

// A failed shell command's result already says how it ended: no error
// line is added, and its output is kept.
func TestFailedShellResultKept(t *testing.T) {
	outcome := ToolOutcome{Name: "shell", Output: "exit 7 · failed · cwd /w\nout\n[stderr]\nerr", Err: fmt.Errorf("exit status 7")}
	if outcome.modelText() != outcome.Output {
		t.Fatalf("model text: %q", outcome.modelText())
	}
	if !completeResult("shell", outcome.Output) || completeResult("shell", "plain") || completeResult("edit_file", outcome.Output) {
		t.Fatal("completeResult")
	}
}
