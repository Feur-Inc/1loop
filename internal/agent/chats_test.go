// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
)

// The chats tool (#357): its own parent and subchats are messaged without
// approval, as before; another chat asks the user first, depending on the
// approval mode. An unknown chat's error reaches the model.
func TestChatsTool(t *testing.T) {
	host := &fakeSubchats{list: []SubchatInfo{{ID: "s1", Title: "Fix", State: SubchatDone}}}
	chats := host.chats("")
	cfg := Config{Chats: chats}
	if !cfg.toolAvailable("chats") || (Config{}).toolAvailable("chats") {
		t.Fatal("chats is offered only with a host")
	}
	if text := chatsInstructions(Config{Provider: "claudecode", Todo: &TodoState{}, Chats: chats}); !strings.Contains(text, "mcp__1loop_todo__chats") {
		t.Fatalf("Claude Code instructions: %q", text)
	}

	output, err, _ := runTool(t, "chats", cfg, false, false, map[string]any{"action": "list"})
	if err != nil || output != "other “Docs” · working · site" {
		t.Fatalf("list = %q, %v", output, err)
	}
	// Its subchat: no approval.
	output, err, asked := runTool(t, "chats", cfg, false, false, map[string]any{"action": "message", "id": "s1", "text": "Also README"})
	if err != nil || len(asked) != 0 || host.messages[0] != "s1: Also README" || !strings.Contains(output, "starts a turn") {
		t.Fatalf("message a subchat: %q %v asked %q", output, err, asked)
	}
	// Another chat: the user is asked, with the text; a refusal sends nothing.
	_, err, asked = runTool(t, "chats", cfg, false, false, map[string]any{"action": "message", "id": "other", "text": "The API moved to /v2"})
	if err == nil || len(host.messages) != 1 || len(asked) != 1 || !strings.Contains(asked[0], "Message another chat?") || !strings.Contains(asked[0], "/v2") || !strings.Contains(asked[0], "“Docs”") {
		t.Fatalf("refused: %v, asked %q, sent %v", err, asked, host.messages)
	}
	output, err, _ = runTool(t, "chats", cfg, false, true, map[string]any{"action": "message", "id": "other", "text": "The API moved to /v2"})
	if err != nil || host.messages[1] != "other: The API moved to /v2" || !strings.Contains(output, "joins the turn under way") {
		t.Fatalf("approved: %q %v", output, err)
	}
	// Full access: not asked.
	if _, err, asked := runTool(t, "chats", cfg, true, false, map[string]any{"action": "message", "id": "other", "text": "x"}); err != nil || len(asked) != 0 {
		t.Fatalf("YOLO: %v asked %q", err, asked)
	}
	if _, err, _ := runTool(t, "chats", cfg, true, true, map[string]any{"action": "message", "id": "gone", "text": "x"}); err == nil || !strings.Contains(err.Error(), "No chat gone") {
		t.Fatalf("an unknown chat: %v", err)
	}
	if _, err, _ := runTool(t, "chats", cfg, true, true, map[string]any{"action": "message", "id": "other"}); err == nil {
		t.Fatal("sent without text")
	}
	// A subchat asks its parent: no approval, and the old subchat message
	// call still goes the same way.
	sub := &fakeSubchats{}
	output, err, asked = runSubchat(t, sub.access("Main"), false, false, map[string]any{"action": "message", "id": "parent", "text": "Which branch?"})
	if err != nil || len(asked) != 0 || sub.messages[0] != "parent: Which branch?" || !strings.Contains(output, "Sent to the parent") {
		t.Fatalf("subchat message to the parent: %q %v", output, err)
	}
}

// Who a turn reports to, the hop count, and the transcript rows of a
// message from another chat and of a report.
func TestChatMessageFlow(t *testing.T) {
	from := ChatSender{ID: "a", Title: "API", Hops: 3}
	message := ChatMessage(from, "", "  hello\nthere ", nil, nil)
	if message.Internal || message.From.Report || message.Content != "[Message from the chat a “API”]\nhello\nthere" {
		t.Fatalf("message %+v", message)
	}
	if got := ChatMessage(from, RelationParent, "x", nil, nil).Content; !strings.HasPrefix(got, "[Message from the parent conversation “API” (chat a)]") {
		t.Fatalf("from the parent: %q", got)
	}
	report := TurnReport(ChatSender{ID: "b", Title: "Docs", Hops: 4}, "", SubchatInfo{State: SubchatDone, Last: "Done it."})
	if !report.Internal || !report.From.Report || WakeSummary(report) != "Chat b “Docs” ended its turn: done" || !strings.Contains(report.Content, "Done it.") {
		t.Fatalf("report %+v", report)
	}

	// A message asks for a report; a report does not, except in a subchat,
	// which reports to its parent whatever woke it; no duplicates.
	if got := ReportTo(nil, message, ""); len(got) != 1 || got[0] != "a" {
		t.Fatalf("after a message: %v", got)
	}
	if got := ReportTo(nil, report, ""); got != nil {
		t.Fatalf("after a report: %v", got)
	}
	if got := ReportTo([]string{"p"}, report, "p"); len(got) != 1 {
		t.Fatalf("a subchat after a report: %v", got)
	}
	if got := ReportTo(nil, Message{Role: "user", Content: "hi"}, "p"); got != nil {
		t.Fatalf("after the user's message: %v", got)
	}

	if TurnHops(0, false, message) != 3 || TurnHops(5, true, message) != 5 || TurnHops(5, false, message) != 3 {
		t.Fatal("hops from a message")
	}
	if TurnHops(5, true, Message{Role: "user", Content: "hi"}) != 0 || TurnHops(5, false, Message{Role: "user", Internal: true}) != 5 {
		t.Fatal("hops from the user, from a task's end")
	}

	if item := MessageItem("", message); item.Kind != "user" || item.Text != "hello\nthere" || item.From == nil || item.From.ID != "a" {
		t.Fatalf("message row %+v", item)
	}
	if item := MessageItem("", report); item.Kind != "note" || item.Text != "Chat b “Docs” ended its turn: done" || item.From == nil {
		t.Fatalf("report row %+v", item)
	}
	if item := MessageItem("p1", Message{Role: "user", Content: "mine"}); item.Kind != "user" || item.From != nil || item.ID != "p1" {
		t.Fatalf("prompt row %+v", item)
	}
	if verb, target, _ := DescribeTool(ChatItem{Kind: "tool", Tool: "chats", Args: `{"action":"message","id":"b"}`}, false); verb != "Messaged" || target != "chat b" {
		t.Fatalf("tool row %q %q", verb, target)
	}
}
