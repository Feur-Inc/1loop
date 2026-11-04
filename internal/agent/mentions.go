// SPDX-License-Identifier: MPL-2.0

package agent

// @ mentions, as in Codex: a message names a tool the user wants the model
// to use (@browser_use, @terminal_use…) or one of their MCP servers. The
// composer's menu completes the names (Mentions); the turn whose message
// names one tells the model to use it (mentionNote). The tools are not
// loaded for it: the model finds them as it otherwise would.

import (
	"regexp"
	"slices"
	"strings"

	"1loop/internal/extensions"
)

// Mention is a name the composer offers after "@".
type Mention struct {
	Name  string `json:"name"`  // what follows "@"
	Label string `json:"label"` // its name in the app
	About string `json:"about"`
	tools string // what the model is told to use
}

// Mentions are the names a chat can mention: the tools it has, then the
// user's MCP servers.
func Mentions(cfg Config) []Mention {
	prefix := ""
	if cfg.Provider == "claudecode" {
		prefix = "mcp__1loop_todo__" // 1loop's tools reach Claude Code through its MCP server
	}
	var mentions []Mention
	add := func(name, label, about string, tools ...string) {
		if cfg.toolAvailable(tools[0]) {
			for i := range tools {
				tools[i] = prefix + tools[i]
			}
			mentions = append(mentions, Mention{name, label, about, label + " (" + strings.Join(tools, ", ") + ")"})
		}
	}
	add("browser_use", "Browser Use", "Use the browser in the side panel",
		"browser_tabs", "browser_navigate", "browser_read", "browser_interact", "browser_evaluate", "browser_resize")
	add("terminal_use", "Terminal", "Use a terminal in the side panel",
		"terminal_tabs", "terminal_command", "terminal_write", "terminal_keys", "terminal_read", "terminal_screen")
	add("phone_use", "Phone Use", "Control the phone", "phone_use", "adb_shell")
	if computerToolDefinition() != nil {
		add("computer_use", "Computer Use", "Control the computer's screen, mouse and keyboard", "computer_use")
	}
	add("github", "GitHub", "Use the GitHub connector", "github_view_attachment", "github_upload_attachments")
	add("image_gen", "Image generation", "Generate or edit an image", "imagegen")
	for _, server := range extensions.Enabled().MCP {
		name := mentionName(server.Name)
		if name == "" || slices.ContainsFunc(mentions, func(m Mention) bool { return m.Name == name }) {
			continue
		}
		about := "Use the MCP server " + server.Name
		mentions = append(mentions, Mention{name, server.Name, about, "the MCP server " + server.Name + " (tools " + extensions.ToolName(server.Name, "") + "*)"})
	}
	return mentions
}

// mentionName is how an MCP server's name is typed after "@".
func mentionName(server string) string {
	return strings.Trim(mentionUnsafe.ReplaceAllString(strings.ToLower(server), "_"), "_")
}

var mentionUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)

// mentionWord is a mention in a message: "@" at the start of a word.
var mentionWord = regexp.MustCompile(`(^|[\s(\[{"'])@([A-Za-z0-9_-]+)`)

// mentioned are the chat's mentions a message names, in their order.
func mentioned(cfg Config, text string) []Mention {
	matches := mentionWord.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	all := Mentions(cfg)
	var found []Mention
	for _, match := range matches {
		name := strings.ToLower(match[2])
		if i := slices.IndexFunc(all, func(m Mention) bool { return m.Name == name }); i >= 0 && !slices.ContainsFunc(found, func(m Mention) bool { return m.Name == name }) {
			found = append(found, all[i])
		}
	}
	return found
}

// mentionNote tells the model which tools the user's message asks for.
func mentionNote(cfg Config, text string) string {
	found := mentioned(cfg, text)
	if len(found) == 0 {
		return ""
	}
	var names, tools []string
	for _, mention := range found {
		names, tools = append(names, "@"+mention.Name), append(tools, mention.tools)
	}
	return "\nThe user's message mentions " + strings.Join(names, ", ") + ": for this request, use " + strings.Join(tools, "; ") +
		". If these tools are not loaded yet, find them with tool search first."
}

// lastUserText is the text of the user's latest message.
func lastUserText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && !messages[i].Internal {
			return messages[i].Content
		}
	}
	return ""
}
