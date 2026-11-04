package agent

// The context window broken down by category, shown under the task list on
// the desktop and the phone as Claude Code's /context shows it (#106).
// Claude Code counts its own (its get_context_usage control request); for
// 1loop's agent the numbers come from its request and its estimates.

import (
	"encoding/json"
	"slices"
	"strings"
)

// ContextUsage is a chat's context window by category.
type ContextUsage struct {
	Used       int               `json:"used"`
	Window     int               `json:"window"`
	Categories []ContextCategory `json:"categories"`
	MCPTools   []ContextTool     `json:"mcpTools,omitempty"`
	// MCPToolCount is how many MCP tools there are when the list itself is
	// left out (Summary).
	MCPToolCount int `json:"mcpToolCount,omitempty"`
}

// ChatContextUsage is a chat's breakdown: Claude Code's last count for a
// Claude Code chat (nil before its first turn ends), else 1loop's own.
func ChatContextUsage(cfg Config, history []Message, claude *ContextUsage, classic bool) *ContextUsage {
	if cfg.Provider == "claudecode" {
		return claude
	}
	usage := ContextBreakdown(cfg, history, classic)
	if usage.Window <= 0 {
		return nil
	}
	return &usage
}

// Summary is the breakdown without the MCP tools' list, only their count:
// what every state update carries; the list is asked when it is opened.
func (usage *ContextUsage) Summary() *ContextUsage {
	if usage == nil {
		return nil
	}
	summary := *usage
	summary.MCPToolCount, summary.MCPTools = len(usage.MCPTools), nil
	return &summary
}

// ContextCategory is one row: Kind is "used" (in the window), "free",
// "buffer" (kept for compaction) or "deferred" (tool schemas the model does
// not see until it loads them: listed apart, outside the window).
type ContextCategory struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
	Kind   string `json:"kind"`
}

// ContextTool is an MCP tool and the size of its schema.
type ContextTool struct {
	Name   string `json:"name"`
	Server string `json:"server"`
	Tokens int    `json:"tokens"`
	Loaded bool   `json:"loaded"`
}

// contextOrder is the rows' order, Claude Code's ids included; an unknown
// id goes before the buffer.
var contextOrder = []string{"messages", "system_tools", "mcp_tools", "system_prompt", "skills", "mcp_instructions",
	"memory_files", "custom_agents", "since_update", "autocompact_buffer", "compact_buffer", "free_space",
	"mcp_tools_deferred", "system_tools_deferred"}

func sortContextCategories(categories []ContextCategory) {
	rank := func(id string) int {
		if i := slices.Index(contextOrder, id); i >= 0 {
			return i
		}
		return slices.Index(contextOrder, "since_update")
	}
	slices.SortStableFunc(categories, func(a, b ContextCategory) int { return rank(a.ID) - rank(b.ID) })
}

func schemaTokens(definitions []map[string]any) int {
	if len(definitions) == 0 {
		return 0
	}
	raw, _ := json.Marshal(definitions)
	return (len(raw) + 3) / 4
}

func textTokens(text string) int {
	return (len(text) + 3) / 4
}

// ContextBreakdown is 1loop's own agent's window by category: what its
// requests carry (instructions, the loaded tools, the conversation). The
// conversation is the provider's last count minus the rest, and what came
// after that count is "Since last update", estimated. Compaction is forced
// at its last level (classic: at its single threshold): the space above is
// the autocompact buffer.
func ContextBreakdown(cfg Config, history []Message, classic bool) ContextUsage {
	usage := ContextUsage{Window: ModelContextWindow(cfg)}
	if usage.Window <= 0 {
		return usage
	}
	skills := textTokens(skillInstructions(cfg))
	system := textTokens(systemInstructions(cfg)) - skills
	loaded := loadedGroups(history)
	tools := schemaTokens(availableCoreTools(cfg))
	var deferredTools, mcp, deferredMCP int
	for _, group := range availableToolGroups(cfg) {
		if !strings.HasPrefix(group.name, "mcp_") {
			if n := schemaTokens(group.tools()); loaded[group.name] {
				tools += n
			} else {
				deferredTools += n
			}
			continue
		}
		for _, definition := range group.tools() {
			name := definition["function"].(map[string]any)["name"].(string)
			tool := ContextTool{Name: name, Server: strings.TrimPrefix(group.name, "mcp_"), Tokens: schemaTokens([]map[string]any{definition}), Loaded: loaded[group.name]}
			usage.MCPTools = append(usage.MCPTools, tool)
			if tool.Loaded {
				mcp += tool.Tokens
			} else {
				deferredMCP += tool.Tokens
			}
		}
	}
	messages, since := HistoryEstimate(history), 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Usage > 0 && !history[i].UsageEstimated {
			// The count covers the request that produced this answer: the
			// answer and all that follows are estimated.
			messages = max(0, history[i].Usage-system-skills-tools-mcp)
			since = HistoryEstimate(history[i:])
			break
		}
	}
	threshold := contextThreshold(cfg, .9)
	if classic {
		threshold = contextThreshold(cfg, .85)
	}
	used := []ContextCategory{
		{"messages", "Messages", messages, "used"},
		{"system_tools", "System tools", tools, "used"},
		{"mcp_tools", "MCP tools", mcp, "used"},
		{"system_prompt", "System prompt", system, "used"},
		{"skills", "Skills", skills, "used"},
		{"since_update", "Since last update", since, "used"},
	}
	for _, category := range used {
		usage.Used += category.Tokens
	}
	buffer := max(0, usage.Window-threshold)
	usage.Categories = append(used,
		ContextCategory{"autocompact_buffer", "Autocompact buffer", buffer, "buffer"},
		ContextCategory{"free_space", "Free space", max(0, usage.Window-usage.Used-buffer), "free"},
		ContextCategory{"mcp_tools_deferred", "MCP tools (deferred)", deferredMCP, "deferred"},
		ContextCategory{"system_tools_deferred", "System tools (deferred)", deferredTools, "deferred"},
	)
	return usage
}

// claudeContextUsage is the answer to Claude Code's get_context_usage.
type claudeContextUsage struct {
	TotalTokens int `json:"totalTokens"`
	MaxTokens   int `json:"maxTokens"`
	Categories  []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Tokens int    `json:"tokens"`
		Kind   string `json:"kind"`
	} `json:"categories"`
	MCPTools []struct {
		Name       string `json:"name"`
		ServerName string `json:"serverName"`
		Tokens     int    `json:"tokens"`
		IsLoaded   bool   `json:"isLoaded"`
	} `json:"mcpTools"`
}

func (answer claudeContextUsage) usage() ContextUsage {
	usage := ContextUsage{Used: answer.TotalTokens, Window: answer.MaxTokens}
	for _, category := range answer.Categories {
		usage.Categories = append(usage.Categories, ContextCategory{category.ID, category.Name, category.Tokens, category.Kind})
	}
	sortContextCategories(usage.Categories)
	for _, tool := range answer.MCPTools {
		usage.MCPTools = append(usage.MCPTools, ContextTool{tool.Name, tool.ServerName, tool.Tokens, tool.IsLoaded})
	}
	return usage
}
