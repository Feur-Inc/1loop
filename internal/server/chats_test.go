// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"os"
	"strings"
	"testing"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// A chat messages another that is not its subchat (#357): the message shows
// in the other chat under the sender's name, starts a turn there, and that
// turn's answer comes back to the sender as a report, which asks for no
// report back. Unknown chats, the chat itself and a long exchange without
// the user are refused.
func TestChatMessages(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	model := subchatModel(t)
	chat := func(id, title string) *serverChat {
		return &serverChat{ID: id, Title: title, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
			Config: agent.Config{Provider: "compatible", BaseURL: model, Model: "mock", Workspace: server.root}}
	}
	api, docs := chat("api", "API work"), chat("docs", "Docs")
	server.mu.Lock()
	server.chats = []*serverChat{api, docs}
	server.mu.Unlock()
	access := server.chatAccess(api)

	list := access.List(ctx)
	if len(list) != 1 || list[0].ID != "docs" || list[0].State != agent.ChatIdle || list[0].Relation != "" {
		t.Fatalf("list %+v", list)
	}
	for id, want := range map[string]string{"nope": "No chat nope on this 1loop server", "api": "That is this chat", "parent": "no parent"} {
		if _, err := access.Find(ctx, id); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("find %s: %v", id, err)
		}
	}

	picture := core.ImagePart{Mime: "image/png", Data: "iVBORw0KGgo="}
	attached := agent.ChatAttachments{Images: []core.ImagePart{picture}, Files: []agent.SentFile{{Name: "spec.md", Data: []byte("# v2")}}}
	if err := access.Send(ctx, "docs", "the API changed: /v2", attached); err != nil {
		t.Fatal(err)
	}
	waitFor(t, server, "the report back", func() bool {
		return docs.run == nil && api.run == nil && countNotes(api, "Chat docs “Docs” ended its turn: done") == 1
	})
	server.mu.Lock()
	message := docs.Items[0]
	if message.Kind != "user" || message.Text != "the API changed: /v2" || message.From == nil || message.From.ID != "api" || message.From.Hops != 1 {
		t.Fatalf("the message in docs: %+v", message)
	}
	if !strings.HasPrefix(docs.History[0].Content, "[Message from the chat api “API work”]\n") {
		t.Fatalf("what docs' agent reads: %q", docs.History[0].Content)
	}
	// The picture api attached reaches docs' model, and the file its
	// workspace, both shown in its transcript as the user's attachments.
	if images := docs.History[0].Images; len(images) != 1 || images[0] != picture {
		t.Fatalf("the images docs' agent sees: %+v", images)
	}
	if len(message.Images) != 1 || message.Images[0] != picture || len(message.Files) != 1 || message.Files[0].Name != "spec.md" {
		t.Fatalf("the attachments in docs' transcript: %+v", message)
	}
	if data, err := os.ReadFile(message.Files[0].Path); err != nil || string(data) != "# v2" || !strings.HasPrefix(message.Files[0].Path, server.root) {
		t.Fatalf("the file in docs' workspace: %q %v", data, err)
	}
	if !strings.Contains(docs.History[0].Content, message.Files[0].Path) {
		t.Fatalf("docs' agent is not told where the file is: %q", docs.History[0].Content)
	}
	if report := api.Items[0]; report.Kind != "note" || report.From == nil || !report.From.Report || report.From.Hops != 2 {
		t.Fatalf("the report in api: %+v", report)
	}
	if countNotes(docs, "Chat api") != 0 || docs.ReportTo != nil || api.ReportTo != nil {
		t.Fatalf("the report was answered: docs %+v, reports to %v %v", docs.Items, docs.ReportTo, api.ReportTo)
	}

	// The hops guard: past MaxChatHops, nothing is sent; the user's own
	// message starts the count again.
	api.hops = agent.MaxChatHops
	server.mu.Unlock()
	if err := access.Send(ctx, "docs", "again", agent.ChatAttachments{}); err == nil || !strings.Contains(err.Error(), "Not sent") {
		t.Fatalf("past the hops: %v", err)
	}
	if got := agent.TurnHops(agent.MaxChatHops, true, agent.Message{Role: "user", Content: "go on"}); got != 0 {
		t.Fatalf("the user's message leaves %d hops", got)
	}

	// A deleted chat is not found.
	server.mu.Lock()
	server.chats = []*serverChat{api}
	server.mu.Unlock()
	if _, err := access.Find(ctx, "docs"); err == nil {
		t.Fatal("a deleted chat was found")
	}
}
