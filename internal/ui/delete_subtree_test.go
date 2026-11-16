// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// deleteTreeWindow is a window with a chat three levels deep, a running
// subchat in it, and another chat with a subchat of its own.
func deleteTreeWindow(t *testing.T) (*desktopUI, *bool) {
	t.Helper()
	test.NewTempApp(t)
	window, _ := twoWindows(t)
	window.windows = window.windows[:1]
	window.ctx = context.Background()
	window.window = test.NewWindow(nil)
	window.window.SetContent(window.build())
	window.window.Resize(fyne.NewSize(1200, 800))
	sub := func(id, parent string) *conversation {
		return &conversation{ID: id, Title: id, Config: agent.Config{Workspace: "/src/app"}, Subchat: &agent.SubchatLink{Parent: parent}}
	}
	stopped := false
	running := sub("a1", "a")
	running.run = &chatRun{cancel: func() { stopped = true }}
	window.chats = []*conversation{
		{ID: "root", Title: "Main", Config: agent.Config{Workspace: "/src/app"}}, sub("a", "root"), sub("b", "root"), running, sub("a1x", "a1"),
		{ID: "other", Title: "Other", Config: agent.Config{Workspace: "/src/app"}}, sub("o1", "other"),
	}
	return window, &stopped
}

func chatIDs(chats []*conversation) []string {
	var ids []string
	for _, chat := range chats {
		ids = append(ids, chat.ID)
	}
	return ids
}

// Deleting a chat on the desktop deletes its subchats at every level and
// stops those that run; deleting a subchat leaves its parent (#448).
func TestDeleteChatTakesItsSubtree(t *testing.T) {
	window, stopped := deleteTreeWindow(t)
	window.openLocalChat(window.chatByID("a1x"))
	if got := chatIDs(window.subtreeOf(window.chatByID("root"))); !slices.Equal(got, []string{"a", "b", "a1", "a1x"}) {
		t.Fatalf("subtree %v", got)
	}
	gone := window.removeTree(window.chatByID("root"))
	if !slices.Equal(chatIDs(gone), []string{"root", "a", "b", "a1", "a1x"}) {
		t.Fatalf("deleted %v", chatIDs(gone))
	}
	if left := chatIDs(window.chats); !slices.Equal(left, []string{"other", "o1"}) {
		t.Fatalf("left %v", left)
	}
	if !*stopped {
		t.Fatal("the running subchat was not stopped")
	}
	if window.current != nil {
		t.Fatal("the deleted nested subchat is still open")
	}
	window.removeTree(window.chatByID("o1"))
	if left := chatIDs(window.chats); !slices.Equal(left, []string{"other"}) {
		t.Fatalf("after deleting the subchat: %v", left)
	}
}

// The worktrees a deleted subtree leaves unused are offered for removal,
// not one another chat still works in.
func TestDeleteTreeWorktrees(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	window.worktrees = map[string]string{"/src/app/.worktrees/a": "/src/app", "/src/app/.worktrees/b": "/src/app"}
	a, b, a1x := window.chatByID("a"), window.chatByID("b"), window.chatByID("a1x")
	a.Config.Workspace, a.Subchat.Branch = "/src/app/.worktrees/a", "subchat/a"
	a1x.Config.Workspace = "/src/app/.worktrees/a" // works in its parent's worktree: deleted with it
	b.Config.Workspace, b.Subchat.Branch = "/src/app/.worktrees/b", "subchat/b"
	window.chatByID("o1").Config.Workspace = "/src/app/.worktrees/b" // another tree works there too
	if got := window.treeWorktrees(window.chatByID("root")); !slices.Equal(got, []string{"/src/app/.worktrees/a"}) {
		t.Fatalf("worktrees %v", got)
	}
	if window.subchatWorktree(a) != "" {
		t.Fatal("a alone does not own a worktree its subchat works in")
	}
}

// The phone and web remote delete a chat with its subtree, and say which
// chats went.
func TestRemoteDeleteChatTakesItsSubtree(t *testing.T) {
	window, _ := deleteTreeWindow(t)
	server := &remoteServer{ui: window, dirty: map[*conversation]map[int]bool{}, meta: map[*conversation]bool{}}
	request := httptest.NewRequest("POST", "/api/chats/a/delete", nil)
	request.SetPathValue("id", "a")
	response := httptest.NewRecorder()
	server.deleteChat(response, request)
	var reply struct {
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || response.Code != 200 {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	if !slices.Equal(reply.Deleted, []string{"a", "a1", "a1x"}) {
		t.Fatalf("deleted %v", reply.Deleted)
	}
	if left := chatIDs(window.chats); !slices.Equal(left, []string{"root", "b", "other", "o1"}) {
		t.Fatalf("left %v", left)
	}
}

// A server's chats: the client counts the subtree it asks to delete.
func TestServerSubtree(t *testing.T) {
	chats := []remoteapi.Chat{{ID: "root"}, {ID: "a", Parent: "root"}, {ID: "a1", Parent: "a"}, {ID: "x"}}
	var ids []string
	for _, chat := range serverSubtree(chats, "root") {
		ids = append(ids, chat.ID)
	}
	if !slices.Equal(ids, []string{"a", "a1"}) {
		t.Fatalf("subtree %v", ids)
	}
}
