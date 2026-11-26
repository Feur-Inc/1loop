// SPDX-License-Identifier: MPL-2.0

package agent

import "testing"

// Claude Code's breakdown follows what each call used: a compaction in the
// middle of a turn shows at once, not when the turn ends.
func TestContextUsageAt(t *testing.T) {
	last := &ContextUsage{Used: 955000, Window: 1000000, Categories: []ContextCategory{
		{"messages", "Messages", 930000, "used"},
		{"system_tools", "System tools", 16000, "used"},
		{"system_prompt", "System prompt", 9000, "used"},
		{"autocompact_buffer", "Autocompact buffer", 33000, "buffer"},
		{"free_space", "Free space", 12000, "free"},
		{"mcp_tools_deferred", "MCP tools (deferred)", 8000, "deferred"},
	}, MCPTools: []ContextTool{{Name: "tool"}}}
	tokens := func(usage *ContextUsage, id string) int {
		for _, category := range usage.Categories {
			if category.ID == id {
				return category.Tokens
			}
		}
		return -1
	}
	now := last.At(98000)
	if now.Used != 98000 || tokens(now, "messages") != 73000 || tokens(now, "free_space") != 869000 {
		t.Errorf("after a compaction: %+v", now)
	}
	if tokens(now, "system_tools") != 16000 || tokens(now, "mcp_tools_deferred") != 8000 || len(now.MCPTools) != 1 {
		t.Errorf("the rest changed: %+v", now)
	}
	if last.Used != 955000 || tokens(last, "messages") != 930000 || tokens(last, "free_space") != 12000 {
		t.Errorf("the count itself changed: %+v", last)
	}
	if full := last.At(990000); tokens(full, "messages") != 965000 || tokens(full, "free_space") != 0 {
		t.Errorf("nearly full: %+v", full)
	}
	if last.At(0) != last || (*ContextUsage)(nil).At(5) != nil {
		t.Error("nothing known: the count stays")
	}
}
