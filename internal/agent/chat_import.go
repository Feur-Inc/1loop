package agent

// Chats from Claude Code and Codex, read from their own session files so
// they go on in 1loop. Listing reads only the start (and, for Claude Code,
// the end) of each file: Codex keeps gigabytes of them. A chat is read
// whole only when it is imported.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"1loop/internal/core"
)

// ImportSession is a conversation another tool kept on this computer.
type ImportSession struct {
	Source  string // "claudecode" or "codex"
	ID      string // the tool's own session ID
	Title   string
	Folder  string // the folder it worked in: the chat's 1loop project
	Updated time.Time
	Path    string
}

// SourceName is how 1loop names the tool a session comes from.
func (session ImportSession) SourceName() string {
	if session.Source == "codex" {
		return "Codex"
	}
	return "Claude Code"
}

// ImportedChat is a session as 1loop keeps a chat.
type ImportedChat struct {
	History []Message
	Items   []ChatItem
	Model   string // the model it last used, when the file says
}

const (
	importHeadBytes = 4 << 20   // where a title and a folder are looked for
	importTailBytes = 256 << 10 // where Claude Code writes its latest title
	importTitleMax  = 80
)

func claudeHome() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func codexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// FindImportSessions lists the Claude Code and Codex sessions found on this
// computer, newest first. A session without a folder or a message is left out.
func FindImportSessions() []ImportSession {
	sessions := append(claudeSessions(filepath.Join(claudeHome(), "projects")), codexSessions(codexHome())...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Updated.After(sessions[j].Updated) })
	return sessions
}

// ReadImportSession reads a whole session.
func ReadImportSession(session ImportSession) (ImportedChat, error) {
	file, err := os.Open(session.Path)
	if err != nil {
		return ImportedChat{}, err
	}
	defer file.Close()
	if session.Source == "codex" {
		return readCodexSession(file, session.Folder)
	}
	return readClaudeSession(file, session.Folder)
}

// eachLine calls visit with every line of r, however long, until it returns
// false or limit bytes are read (0: no limit).
func eachLine(r io.Reader, limit int64, visit func([]byte) bool) {
	if limit > 0 {
		r = io.LimitReader(r, limit)
	}
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 && !visit(line) {
			return
		}
		if err != nil {
			return
		}
	}
}

func importTitle(text string) string {
	text = strings.Join(strings.Fields(FirstLine(strings.TrimSpace(text))), " ")
	if runes := []rune(text); len(runes) > importTitleMax {
		text = string(runes[:importTitleMax-1]) + "…"
	}
	return text
}

// ---------------------------------------------------------------------------
// Claude Code: ~/.claude/projects/<folder>/<session>.jsonl, one entry a line.
// Entries form a tree (parentUuid): rewinds and edits leave branches, and the
// chat is the branch that ends with the last entry written.

type claudeEntry struct {
	Type          string `json:"type"`
	UUID          string `json:"uuid"`
	ParentUUID    string `json:"parentUuid"`
	LogicalParent string `json:"logicalParentUuid"` // across a compaction
	Sidechain     bool   `json:"isSidechain"`       // a subagent's work
	Meta          bool   `json:"isMeta"`
	CompactRecap  bool   `json:"isCompactSummary"`
	Cwd           string `json:"cwd"`
	Entrypoint    string `json:"entrypoint"` // sdk-cli: run by a program (1loop's own Claude Code turns)
	CustomTitle   string `json:"customTitle"`
	Summary       string `json:"summary"` // the title older versions wrote
	Message       *struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

// claudeUserPrompt is what the user typed in an entry, without what Claude
// Code adds around slash commands and reminders.
func claudeUserPrompt(content json.RawMessage) (text string, images []core.ImagePart, results []claudeBlock) {
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return claudeUserText(plain), nil, nil
	}
	var blocks []struct {
		claudeBlock
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	_ = json.Unmarshal(content, &blocks)
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if part := claudeUserText(block.Text); part != "" {
				parts = append(parts, part)
			}
		case "image":
			if block.Source.Type == "base64" && block.Source.Data != "" {
				images = append(images, core.ImagePart{Mime: block.Source.MediaType, Data: block.Source.Data})
			}
		case "tool_result":
			results = append(results, block.claudeBlock)
		}
	}
	return strings.Join(parts, "\n\n"), images, results
}

func claudeUserText(text string) string {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"<command-", "<local-command-", "<system-reminder>", "Caveat: ", "[Request interrupted by user"} {
		if strings.HasPrefix(trimmed, prefix) {
			return ""
		}
	}
	return trimmed
}

func claudeSessions(root string) []ImportSession {
	folders, _ := os.ReadDir(root)
	var sessions []ImportSession
	for _, folder := range folders {
		if !folder.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(root, folder.Name(), "*.jsonl"))
		for _, path := range files {
			if session, ok := claudeSessionInfo(path); ok {
				sessions = append(sessions, session)
			}
		}
	}
	return sessions
}

func claudeSessionInfo(path string) (ImportSession, bool) {
	file, err := os.Open(path)
	if err != nil {
		return ImportSession{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ImportSession{}, false
	}
	session := ImportSession{Source: "claudecode", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Updated: info.ModTime(), Path: path}
	prompt, sidechain, first := "", false, true
	eachLine(file, importHeadBytes, func(line []byte) bool {
		var entry claudeEntry
		if json.Unmarshal(line, &entry) != nil {
			return true
		}
		if entry.Type == "user" || entry.Type == "assistant" {
			if first { // older versions kept each subagent in a file of its own
				sidechain, first = entry.Sidechain || entry.Entrypoint == "sdk-cli", false
			}
			if session.Folder == "" {
				session.Folder = entry.Cwd
			}
		}
		if entry.Type == "user" && !entry.Meta && !entry.CompactRecap && entry.Message != nil && prompt == "" {
			prompt, _, _ = claudeUserPrompt(entry.Message.Content)
		}
		return session.Folder == "" || prompt == ""
	})
	if sidechain || session.Folder == "" || prompt == "" {
		return ImportSession{}, false
	}
	// The title the user gave, or Claude Code's, is rewritten as the chat goes:
	// the last one is near the end.
	if offset := info.Size() - importTailBytes; offset > 0 {
		_, _ = file.Seek(offset, io.SeekStart)
	} else {
		_, _ = file.Seek(0, io.SeekStart)
	}
	eachLine(file, 0, func(line []byte) bool {
		var entry claudeEntry
		if json.Unmarshal(line, &entry) == nil {
			if entry.Type == "custom-title" && entry.CustomTitle != "" {
				session.Title = entry.CustomTitle
			} else if entry.Type == "summary" && entry.Summary != "" && session.Title == "" {
				session.Title = entry.Summary
			}
		}
		return true
	})
	if session.Title == "" {
		session.Title = prompt
	}
	session.Title = importTitle(session.Title)
	return session, true
}

func readClaudeSession(r io.Reader, folder string) (ImportedChat, error) {
	var entries []*claudeEntry
	first := map[string]int{} // Claude Code writes some entries again after a compaction
	var messages []int        // where the user's and the model's entries are
	eachLine(r, 0, func(line []byte) bool {
		entry := &claudeEntry{}
		if json.Unmarshal(line, entry) != nil || entry.UUID == "" || entry.Sidechain {
			return true
		}
		// Every entry links the chain (attachments, system notes…); only
		// the messages are read from it.
		if entry.Type == "user" || entry.Type == "assistant" {
			messages = append(messages, len(entries))
		} else {
			entry.Message, entry.ToolUseResult = nil, nil
		}
		if _, ok := first[entry.UUID]; !ok {
			first[entry.UUID] = len(entries)
		}
		entries = append(entries, entry)
		return true
	})
	var branch []*claudeEntry
	if len(messages) > 0 {
		at, seen := messages[len(messages)-1], map[int]bool{}
		for at >= 0 && !seen[at] {
			seen[at] = true
			entry := entries[at]
			branch = append(branch, entry)
			if entry.Type == "system" && entry.LogicalParent != "" && entry.ParentUUID == "" {
				// A compaction: what came before ends with the last message
				// written before it (its own link may loop on a rewritten entry).
				at = sort.SearchInts(messages, at) - 1
				if at >= 0 {
					at = messages[at]
				}
				continue
			}
			parent, ok := first[entry.ParentUUID]
			if entry.ParentUUID == "" || !ok {
				break
			}
			at = parent
		}
	}
	if len(branch) == 0 {
		return ImportedChat{}, fmt.Errorf("this Claude Code session has no messages")
	}
	var chat ImportedChat
	pending := map[string]ToolCall{}
	answer := ""
	flush := func() {
		if strings.TrimSpace(answer) != "" {
			chat.Items = append(chat.Items, ChatItem{Kind: "assistant", Text: strings.TrimSpace(answer)})
			chat.History = append(chat.History, Message{Role: "assistant", Content: strings.TrimSpace(answer)})
		}
		answer = ""
	}
	for i := len(branch) - 1; i >= 0; i-- {
		entry := branch[i]
		if entry.Message == nil || entry.Meta || entry.CompactRecap {
			continue
		}
		if entry.Type == "assistant" {
			if entry.Message.Model != "" && entry.Message.Model != "<synthetic>" {
				chat.Model = entry.Message.Model
			}
			var blocks []claudeBlock
			_ = json.Unmarshal(entry.Message.Content, &blocks)
			for _, block := range blocks {
				switch block.Type {
				case "text":
					answer += block.Text
				case "tool_use":
					flush()
					call := ToolCall{ID: block.ID, Type: "function"}
					call.Function.Name, call.Function.Arguments = claudeTool(folder, block.Name, block.Input)
					pending[block.ID] = call
				}
			}
			continue
		}
		text, images, results := claudeUserPrompt(entry.Message.Content)
		for _, result := range results {
			call, ok := pending[result.ToolUseID]
			if !ok {
				continue
			}
			delete(pending, result.ToolUseID)
			item := ChatItem{Kind: "tool", Tool: call.Function.Name, Args: call.Function.Arguments, Text: claudeText(result.Content)}
			if result.IsError {
				item.Failed = FirstLine(item.Text)
			} else if item.Tool == "edit_file" {
				path, _, _ := ToolArgs(item.Args)
				item.Diff = claudeDiff(path, entry.ToolUseResult)
			}
			chat.Items = append(chat.Items, item)
			chat.History = append(chat.History, Message{Role: "assistant", ToolCalls: []ToolCall{call}},
				Message{Role: "tool", ToolCallID: call.ID, Content: item.Text})
		}
		if text != "" || len(images) > 0 {
			flush()
			chat.Items = append(chat.Items, ChatItem{ID: core.Identifier()[:16], Kind: "user", Text: text, Images: images})
			chat.History = append(chat.History, Message{Role: "user", Content: text, Images: images})
		}
	}
	flush()
	return chat, nil
}

// ---------------------------------------------------------------------------
// Codex: ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl. The first line says
// where the session worked; its title is in session_index.jsonl. Current
// versions log each finished step ("item_completed"); older ones only the
// user's and the agent's messages and the raw tool calls.

type codexLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexMeta struct {
	ID     string          `json:"id"`
	Cwd    string          `json:"cwd"`
	Source json.RawMessage `json:"source"`
}

type codexEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"` // user_message, agent_message
	Model   string `json:"model"`   // turn_context
	Item    *struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Command          []string                    `json:"command"`
		AggregatedOutput string                      `json:"aggregated_output"`
		ExitCode         *int                        `json:"exit_code"`
		Duration         struct{ Secs, Nanos int64 } `json:"duration"`
		Changes          map[string]struct {
			Type        string `json:"type"`
			UnifiedDiff string `json:"unified_diff"`
		} `json:"changes"`
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Result    *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	} `json:"item"`
	// response_item calls and their outputs (older versions)
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
}

func (event codexEvent) text() string {
	if event.Item == nil {
		return event.Message
	}
	var parts []string
	for _, part := range event.Item.Content {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func codexSessions(home string) []ImportSession {
	titles := map[string]string{}
	if index, err := os.Open(filepath.Join(home, "session_index.jsonl")); err == nil {
		eachLine(index, 0, func(line []byte) bool {
			var entry struct {
				ID   string `json:"id"`
				Name string `json:"thread_name"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.ID != "" && entry.Name != "" {
				titles[entry.ID] = entry.Name // the last name given wins
			}
			return true
		})
		index.Close()
	}
	var sessions []ImportSession
	_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if session, ok := codexSessionInfo(path, titles); ok {
			sessions = append(sessions, session)
		}
		return nil
	})
	return sessions
}

func codexSessionInfo(path string, titles map[string]string) (ImportSession, bool) {
	file, err := os.Open(path)
	if err != nil {
		return ImportSession{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ImportSession{}, false
	}
	session := ImportSession{Source: "codex", Updated: info.ModTime(), Path: path}
	prompt, subagent := "", false
	eachLine(file, importHeadBytes, func(line []byte) bool {
		var entry codexLine
		if json.Unmarshal(line, &entry) != nil {
			return true
		}
		switch entry.Type {
		case "session_meta":
			var meta codexMeta
			_ = json.Unmarshal(entry.Payload, &meta)
			session.ID, session.Folder = meta.ID, meta.Cwd
			subagent = len(meta.Source) > 0 && meta.Source[0] == '{' // a subagent's thread
		case "event_msg":
			var event codexEvent
			_ = json.Unmarshal(entry.Payload, &event)
			if event.Type == "user_message" || event.Type == "item_completed" && event.Item != nil && event.Item.Type == "UserMessage" {
				prompt = event.text()
			}
		}
		return prompt == ""
	})
	if subagent || session.ID == "" || session.Folder == "" || prompt == "" {
		return ImportSession{}, false
	}
	session.Title = titles[session.ID]
	if session.Title == "" {
		session.Title = prompt
	}
	session.Title = importTitle(session.Title)
	return session, true
}

func readCodexSession(r io.Reader, folder string) (ImportedChat, error) {
	var events []codexEvent
	var calls []codexEvent // response_item calls and outputs, for older versions
	// Each turn is read in the format it was written in: a session resumed
	// with another Codex version mixes them, turn by turn.
	var turns []int         // the turn of each event
	steps := map[int]bool{} // the turns written as completed items
	turn := 0
	eachLine(r, 0, func(line []byte) bool {
		var entry codexLine
		if json.Unmarshal(line, &entry) != nil {
			return true
		}
		var event codexEvent
		switch entry.Type {
		case "event_msg", "turn_context":
			if json.Unmarshal(entry.Payload, &event) != nil {
				return true
			}
			if entry.Type == "turn_context" {
				event.Type = "turn_context"
				turn++
			}
			steps[turn] = steps[turn] || event.Type == "item_completed"
			events = append(events, event)
			turns = append(turns, turn)
		case "response_item":
			if json.Unmarshal(entry.Payload, &event) == nil && event.Type != "message" && event.Type != "reasoning" {
				calls = append(calls, event)
				events = append(events, event)
				turns = append(turns, turn)
			}
		}
		return true
	})
	var chat ImportedChat
	addUser := func(text string) {
		if text = strings.TrimSpace(text); text != "" {
			chat.Items = append(chat.Items, ChatItem{ID: core.Identifier()[:16], Kind: "user", Text: text})
			chat.History = append(chat.History, Message{Role: "user", Content: text})
		}
	}
	addAnswer := func(text string) {
		if text = strings.TrimSpace(text); text != "" {
			chat.Items = append(chat.Items, ChatItem{Kind: "assistant", Text: text})
			chat.History = append(chat.History, Message{Role: "assistant", Content: text})
		}
	}
	addTool := func(item ChatItem) {
		id := "import-" + core.Identifier()[:12]
		call := ToolCall{ID: id, Type: "function"}
		call.Function.Name, call.Function.Arguments = item.Tool, item.Args
		output := item.Text
		if item.Diff != "" { // the model continuing the chat sees what the edit changed
			output += "\n" + item.Diff
		}
		chat.Items = append(chat.Items, item)
		chat.History = append(chat.History, Message{Role: "assistant", ToolCalls: []ToolCall{call}},
			Message{Role: "tool", ToolCallID: id, Content: output})
	}
	outputs := map[string]string{}
	for _, call := range calls {
		if call.CallID != "" && strings.HasSuffix(call.Type, "_output") {
			outputs[call.CallID] = codexOutput(call.Output)
		}
	}
	for i, event := range events {
		if event.Type == "turn_context" && event.Model != "" {
			chat.Model = event.Model
		}
		if steps[turns[i]] {
			if event.Type != "item_completed" || event.Item == nil {
				continue
			}
			switch event.Item.Type {
			case "UserMessage":
				addUser(event.text())
			case "AgentMessage":
				addAnswer(event.text())
			case "CommandExecution":
				item := ChatItem{Kind: "tool", Tool: "shell", Args: encodeArgs(map[string]string{"command": codexCommand(event.Item.Command)}),
					Text: event.Item.AggregatedOutput, Millis: event.Item.Duration.Secs*1000 + event.Item.Duration.Nanos/1e6}
				if code := event.Item.ExitCode; code != nil && *code != 0 {
					item.Failed = fmt.Sprintf("Exit code %d", *code)
				}
				addTool(item)
			case "FileChange":
				paths := make([]string, 0, len(event.Item.Changes))
				for path := range event.Item.Changes {
					paths = append(paths, path)
				}
				sort.Strings(paths)
				for _, path := range paths {
					change := event.Item.Changes[path]
					shown := path
					if rel, err := filepath.Rel(folder, path); err == nil && !strings.HasPrefix(rel, "..") {
						shown = rel
					}
					args := map[string]string{"path": shown}
					if change.Type == "add" {
						args["old_text"] = "" // a new file
					}
					diff := change.UnifiedDiff
					if diff != "" && !strings.HasPrefix(diff, "---") {
						diff = fmt.Sprintf("--- a/%s\n+++ b/%s\n%s", shown, shown, diff)
					}
					addTool(ChatItem{Kind: "tool", Tool: "edit_file", Args: encodeArgs(args), Text: "Edited " + shown, Diff: diff})
				}
			case "McpToolCall":
				item := ChatItem{Kind: "tool", Tool: event.Item.Tool, Args: string(event.Item.Arguments)}
				if result := event.Item.Result; result != nil {
					var parts []string
					for _, part := range result.Content {
						if part.Text != "" {
							parts = append(parts, part.Text)
						}
					}
					item.Text = strings.Join(parts, "\n")
					if result.IsError {
						item.Failed = FirstLine(item.Text)
					}
				}
				addTool(item)
			}
			continue
		}
		switch event.Type {
		case "user_message":
			addUser(event.Message)
		case "agent_message":
			addAnswer(event.Message)
		case "function_call", "custom_tool_call", "local_shell_call":
			args := event.Arguments
			if args == "" {
				args = encodeArgs(map[string]string{"input": event.Input})
			}
			addTool(ChatItem{Kind: "tool", Tool: event.Name, Args: args, Text: outputs[event.CallID]})
		}
	}
	if len(chat.History) == 0 {
		return ImportedChat{}, fmt.Errorf("this Codex session has no messages")
	}
	return chat, nil
}

// codexCommand is the command line the user would type: Codex runs it
// through a shell ("bash -lc …").
func codexCommand(command []string) string {
	if n := len(command); n >= 3 && (command[n-2] == "-lc" || command[n-2] == "-c") {
		return command[n-1]
	}
	return strings.Join(command, " ")
}

// codexOutput is a call's output: plain text, or JSON with an "output" field.
func codexOutput(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return string(raw)
	}
	var wrapped struct {
		Output string `json:"output"`
	}
	if json.Unmarshal([]byte(text), &wrapped) == nil && wrapped.Output != "" {
		return wrapped.Output
	}
	return text
}

func encodeArgs(args map[string]string) string {
	data, _ := json.Marshal(args)
	return string(data)
}
