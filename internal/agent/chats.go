// SPDX-License-Identifier: MPL-2.0

package agent

// Messages between chats: an agent sends a message to another chat of the
// same 1loop (desktop or server), addressed by the chat's id, whether it is
// one of its subchats, its parent or any other chat. It is the one way
// chats talk: a parent's follow-up to its subchat, a subchat's question to
// its parent and the report a turn sends back when it ends all go through
// it (the host's wake).
//
// The message reaches the other chat's agent labelled with the sender (it
// is not the user's), joins its turn if it is working or starts one, and
// that turn reports its end to the sender. A report asks for no report
// back, except from a subchat, whose every turn its parent or its own
// tasks caused reports to the parent; so A → B → report → A ends there.
// Hops count the messages between chats since the user last wrote:
// past MaxChatHops the tool refuses to send more, which stops two agents
// from talking to each other forever.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"1loop/internal/core"
)

// MaxChatHops is how many messages between chats can follow one another
// without the user before the chats tool refuses to send another.
const MaxChatHops = 8

// ChatSender is the chat a message or a report came from.
type ChatSender struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Hops: the messages between chats that led to this one since the user
	// last wrote, this one included.
	Hops int `json:"hops,omitempty"`
	// Report: the end of the sender's turn, not a message it wrote.
	Report bool `json:"report,omitempty"`
}

// How a chat stands to another, in ChatInfo.Relation.
const (
	RelationParent  = "parent"  // the chat that started this subchat
	RelationSubchat = "subchat" // a subchat this chat started
)

// The states of a chat in the chats tool's list.
const (
	ChatWorking = "working"
	ChatWaiting = "waiting" // for its user: an approval or a question
	ChatIdle    = "idle"
)

// ChatInfo is a chat another chat may message.
type ChatInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Project  string `json:"project,omitempty"`
	Machine  string `json:"machine,omitempty"` // the SSH machine it works on
	State    string `json:"state"`
	Relation string `json:"relation,omitempty"` // how it stands to the asking chat
	Provider string `json:"-"`                  // its model's provider: whether it sees images
}

// ChatAccess is the host's side of the chats tool, for one chat. Find gives
// the chat id ("parent" in a subchat) or says why it cannot be reached;
// Send delivers text, and the files attached to it, to it (see the host's
// wake; DeliverFiles copies the files); List is every other chat of this
// 1loop.
type ChatAccess struct {
	Find func(ctx context.Context, id string) (ChatInfo, error)
	List func(ctx context.Context) []ChatInfo
	Send func(ctx context.Context, id, text string, attachments ChatAttachments) error
}

// ChatState is a chat's state for the chats tool.
func ChatState(running, waiting bool) string {
	switch {
	case waiting:
		return ChatWaiting
	case running:
		return ChatWorking
	}
	return ChatIdle
}

// TooManyHops is the error for a message past MaxChatHops.
func TooManyHops(hops int) error {
	return fmt.Errorf("Not sent: %d messages between chats have followed one another since the user last wrote, the most there can be. Do not send more: end your turn and tell the user what is still open", hops-1)
}

// ChatMessage is text from sender, with the pictures and files (already in
// the receiver's workspace) it attached, delivered to another chat.
// relation is how the sender stands to the receiver: parent, subchat or "".
func ChatMessage(sender ChatSender, relation, text string, images []core.ImagePart, files []FileAttachment) Message {
	sender.Report = false
	var label string
	switch relation {
	case RelationParent:
		label = fmt.Sprintf("[Message from the parent conversation “%s” (chat %s)]", sender.Title, sender.ID)
	case RelationSubchat:
		label = fmt.Sprintf("[Message from subchat %s “%s”]", sender.ID, sender.Title)
	default:
		label = fmt.Sprintf("[Message from the chat %s “%s”]", sender.ID, sender.Title)
	}
	return Message{Role: "user", From: &sender, Content: ChatAttachmentText(label+"\n"+strings.TrimSpace(text), files), Images: images, Files: files}
}

// TurnReport tells another chat that sender's turn ended: info is the
// sender's state and last answer, relation how it stands to the receiver.
func TurnReport(sender ChatSender, relation string, info SubchatInfo) Message {
	sender.Report = true
	var text strings.Builder
	switch relation {
	case RelationSubchat:
		fmt.Fprintf(&text, "[Subchat %s “%s” ended its turn: %s]\n", sender.ID, sender.Title, info.State)
	case RelationParent:
		fmt.Fprintf(&text, "[The parent conversation “%s” (chat %s) ended its turn: %s]\n", sender.Title, sender.ID, info.State)
	default:
		fmt.Fprintf(&text, "[Chat %s “%s” ended its turn: %s]\n", sender.ID, sender.Title, info.State)
	}
	if info.Last != "" {
		text.WriteString(core.TruncateText(info.Last, 2000) + "\n")
	} else {
		text.WriteString("(no answer)\n")
	}
	text.WriteString(subchatPlace(info))
	return Message{Role: "user", Internal: true, From: &sender, Content: strings.TrimSpace(text.String())}
}

// SubchatReport tells the parent its subchat's turn ended.
func SubchatReport(info SubchatInfo) Message {
	return TurnReport(ChatSender{ID: info.ID, Title: info.Title}, RelationSubchat, info)
}

// ReportTo is who a chat's turn reports to once m has reached it: the
// sender of a message, and for a subchat (parent its parent's id) its
// parent when 1loop woke it (its own task's end, a report).
func ReportTo(current []string, m Message, parent string) []string {
	add := func(id string) {
		if id != "" && !slices.Contains(current, id) {
			current = append(current, id)
		}
	}
	if m.From != nil && !m.From.Report {
		add(m.From.ID)
	} else if m.Internal {
		add(parent)
	}
	return current
}

// TurnHops is a chat's hop count once m reaches it: a running turn keeps
// the most it has seen; the user's own message starts again from none.
func TurnHops(current int, running bool, m Message) int {
	switch {
	case m.From != nil && running:
		return max(current, m.From.Hops)
	case m.From != nil:
		return m.From.Hops
	case !m.Internal:
		return 0
	}
	return current
}

// MessageItem is a message's transcript row: the user's prompt, another
// chat's message under its sender, or a note for what 1loop tells the
// agent (a task's end, a report).
func MessageItem(id string, m Message) ChatItem {
	if m.Internal {
		return ChatItem{ID: id, Kind: "note", Text: WakeSummary(m), From: m.From}
	}
	item := PromptItem(id, m)
	if m.From != nil {
		_, body, _ := strings.Cut(item.Text, "\n")
		item.Text, item.From = body, m.From
	}
	return item
}

func chatsToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name": "chats",
		"description": "Talk to the other chats (conversations) of this 1loop. list gives each chat's id, title, project, and whether it is working, waiting for its user or idle; this chat's parent and subchats are marked. " +
			"message sends text to chat id (parent: this subchat's parent). It reaches that chat's agent labelled as coming from this chat, not from the user: it joins its turn if it is working, or starts one. When that turn ends, its last answer is reported back to you as a message, so do not poll. " +
			"Write what the other chat needs, since it does not see this conversation. Messaging a chat that is not this chat's parent or subchat asks the user first, depending on the approval mode. Chats on another computer or 1loop server cannot be reached. " +
			"files (message) attaches files to the message, by path (workspace or /tmp): pictures reach the other chat's model as images it sees, as if the user had pasted them (send a screenshot rather than describing it); other files are copied into its workspace and attached. Leave unused fields null.",
		"parameters": map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"action", "id", "text", "files"},
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"list", "message"}},
				"id":     map[string]any{"type": []string{"string", "null"}, "description": "message: the chat's id from list, or parent."},
				"text":   map[string]any{"type": []string{"string", "null"}, "description": "message: what to say."},
				"files":  filesProperty("message"),
			}},
	}}
}

func chatsInstructions(cfg Config) string {
	if !cfg.toolAvailable("chats") || (cfg.Provider == "claudecode" && cfg.Todo == nil) { // no MCP server
		return ""
	}
	tool := "chats"
	if cfg.Provider == "claudecode" {
		tool = "mcp__1loop_todo__chats"
	}
	return "\n\nOther chats: " + tool + " lists the other conversations of this 1loop and sends one a message. Messages starting with [Message from … come from another chat's agent, not from the user; when your turn ends, your last answer is reported to it."
}

// chatsTool answers the chats tool.
func (backend *App) chatsTool(ctx context.Context, arguments string, begin func(), emit emitter) (string, error) {
	cfg, _ := ctx.Value(configContextKey{}).(Config)
	if cfg.Chats == nil {
		return "", fmt.Errorf("Other chats cannot be reached from this chat")
	}
	access := *cfg.Chats
	var args struct {
		Action   string
		ID, Text *string
		Files    []string
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	value := func(field *string) string {
		if field == nil {
			return ""
		}
		return strings.TrimSpace(*field)
	}
	switch args.Action {
	case "list":
		begin()
		var lines []string
		for _, info := range access.List(ctx) {
			lines = append(lines, chatLine(info))
		}
		if len(lines) == 0 {
			return "No other chat here.", nil
		}
		return strings.Join(lines, "\n"), nil
	case "message":
		id, text := value(args.ID), value(args.Text)
		if id == "" || text == "" {
			return "", fmt.Errorf("Give the chat's id (from list) and the text")
		}
		if utf8.RuneCountInString(text) > 20000 {
			return "", fmt.Errorf("The message is too long: keep it under 20000 characters, and point at files instead")
		}
		info, err := access.Find(ctx, id)
		if err != nil {
			return "", err
		}
		attachments, err := messageAttachments(ctx, cfg.Workspace, args.Files)
		if err != nil {
			return "", err
		}
		if info.Relation == "" { // its own parent or subchats: no approval, as before
			preview := fmt.Sprintf("To %s\n\n%s%s", chatLine(info), text, filesPreview(args.Files))
			if !backend.confirm(ctx, emit, "Message another chat?", preview) {
				return "", fmt.Errorf("The user did not send the message")
			}
		}
		begin()
		if err := access.Send(ctx, id, text, attachments); err != nil {
			return "", err
		}
		note := AttachmentsNote(info.Provider, attachments)
		switch {
		case info.Relation == RelationParent:
			return "Sent to the parent conversation; its answer comes back to you when its turn ends. End your turn if you cannot go on without it." + note, nil
		case info.State == ChatIdle:
			return fmt.Sprintf("Sent to %s “%s”: it starts a turn, and you will receive its answer when the turn ends.%s", info.ID, info.Title, note), nil
		}
		return fmt.Sprintf("Sent to %s “%s”: it joins the turn under way, and you will receive its answer when the turn ends.%s", info.ID, info.Title, note), nil
	}
	return "", fmt.Errorf("Unknown action %q", args.Action)
}

// chatLine is a chat in the chats tool's list.
func chatLine(info ChatInfo) string {
	line := fmt.Sprintf("%s “%s” · %s", info.ID, info.Title, info.State)
	if info.Project != "" {
		line += " · " + info.Project
	}
	if info.Machine != "" {
		line += " on " + info.Machine
	}
	switch info.Relation {
	case RelationParent:
		line += " · this chat's parent"
	case RelationSubchat:
		line += " · a subchat of this chat"
	}
	return line
}
