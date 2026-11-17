// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bytes"
	"encoding/json"
	"testing"

	"1loop/internal/agent"
)

// The chats written one at a time make the same file as the list encoded
// at once (#476).
func TestWriteChatsMatchesWholeEncoding(t *testing.T) {
	chats := []*conversation{
		{ID: "a", Title: "First", History: []agent.Message{{Role: "user"}}},
		{ID: "b", Title: "Second <&>"},
	}
	head := []byte(`{"x":1,"chats":null,"y":2}`)
	before, after, _ := bytes.Cut(head, []byte(`"chats":null`))
	var got bytes.Buffer
	var written int64
	if err := writeChats(&got, &written, before, chats, after); err != nil {
		t.Fatal(err)
	}
	whole, _ := json.Marshal(chats)
	want := string(before) + `"chats":` + string(whole) + string(after)
	if got.String() != want || written != int64(got.Len()) {
		t.Fatalf("got  %s\nwant %s (written %d)", got.String(), want, written)
	}
	if err := writeChats(&got, &written, before, nil, after); err != nil {
		t.Fatal(err)
	}
}
