// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// GET /api/search finds a chat by its messages, with the words around the
// match, after the chats found by their title (#700).
func TestSearchChats(t *testing.T) {
	entries := []SearchEntry{
		NewSearchEntry("a", "Database choice", nil),
		NewSearchEntry("b", "Landing page", []string{"We picked Postgres for the database."}),
	}
	recorder := httptest.NewRecorder()
	SearchChats(recorder, httptest.NewRequest("GET", "/api/search?q=database", nil), entries)
	var reply struct {
		Chats []ChatHit
		More  int
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Chats) != 2 || reply.Chats[0].ID != "a" || reply.Chats[0].Snippet != "" || reply.Chats[1].ID != "b" {
		t.Fatalf("chats = %+v, want a by its title, then b by its message", reply.Chats)
	}
	if reply.Chats[1].Snippet != "We picked Postgres for the database." {
		t.Errorf("snippet = %q", reply.Chats[1].Snippet)
	}
	recorder = httptest.NewRecorder()
	SearchChats(recorder, httptest.NewRequest("GET", "/api/search?q=+", nil), entries)
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil || len(reply.Chats) != 0 {
		t.Errorf("a query with no word found %+v, %v", reply.Chats, err)
	}
}
