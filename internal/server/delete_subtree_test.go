// SPDX-License-Identifier: MPL-2.0

package server

import (
	"encoding/json"
	"slices"
	"testing"

	"1loop/internal/agent"
)

// Deleting a chat deletes its subchats at every level, and stops those
// that run; other chats stay (#448).
func TestDeleteChatTakesItsSubtree(t *testing.T) {
	server, root, call := testServer(t)
	sub := func(id, parent string) *serverChat {
		return &serverChat{ID: id, Title: id, Config: agent.Config{Workspace: root}, Subchat: &agent.SubchatLink{Parent: parent}}
	}
	stopped := false
	running := sub("a1", "a")
	running.run = &serverRun{cancel: func() { stopped = true }}
	server.mu.Lock()
	server.chats = []*serverChat{
		{ID: "root", Title: "Main", Config: agent.Config{Workspace: root}}, sub("a", "root"), sub("b", "root"), running, sub("a1x", "a1"),
		{ID: "other", Title: "Other", Config: agent.Config{Workspace: root}}, sub("o1", "other"),
	}
	server.mu.Unlock()

	code, data := call("POST", "/api/chats/root/delete", nil)
	if code != 200 {
		t.Fatalf("delete: %d %s", code, data)
	}
	var reply struct {
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reply.Deleted, []string{"root", "a", "b", "a1", "a1x"}) {
		t.Fatalf("deleted %v", reply.Deleted)
	}
	server.mu.Lock()
	var left []string
	for _, chat := range server.chats {
		left = append(left, chat.ID)
	}
	server.mu.Unlock()
	if !slices.Equal(left, []string{"other", "o1"}) {
		t.Fatalf("left %v", left)
	}
	if !stopped || running.run != nil {
		t.Fatal("the running subchat was not stopped")
	}

	// A subchat alone: its parent stays.
	if code, data := call("POST", "/api/chats/o1/delete", nil); code != 200 {
		t.Fatalf("delete a subchat: %d %s", code, data)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.chats) != 1 || server.chats[0].ID != "other" {
		t.Fatalf("after deleting the subchat: %d chats", len(server.chats))
	}
}
