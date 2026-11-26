// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"1loop/internal/agent"
)

// The chats written one at a time make the same file as the list encoded
// at once (#476).
func TestWriteChatsMatchesWholeEncoding(t *testing.T) {
	chats := []*conversation{
		{ID: "a", Title: "First", History: []agent.Message{{Role: "user"}}, Items: []agent.ChatItem{{Kind: "user"}, {Kind: "note"}}},
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

// A saved state reads as json.Unmarshal reads it, whatever its spacing, and
// one that cannot be read fails with the same error.
func TestReadSavedStateMatchesWholeDecoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	long := strings.Repeat("picture", 1000)
	for _, text := range []string{
		`{}`, `null`, ` {"chats":null,"projects":["/a"]} `, `{"chats":[]}`,
		`{"claudeOffered":true,"chats":[{"id":"a","title":"First","history":[{"role":"user","content":"` + long + `"}],"items":[{"kind":"user","text":"` + long + `"}],"versions":[{"id":"v","snapshot":{"history":[],"items":null,"sharedItems":1}}]},null,{"id":"b","history":null}],"theme":"dark","unknown":{"history":[1]}}`,
		"{\n \"chats\" : [ {\n \"id\" : \"a\" } ] ,\"PROJECTS\":[\"/b\"]}",
		``, `{`, `{"chats":{}}`, `{"chats":[{"history":[{"role":1}]}]}`, `{"projects":3}`, `{} {}`, `{"chats":[]`, `[]`,
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		var want savedState
		wantErr := json.Unmarshal([]byte(text), &want)
		got, err := readSavedState(path)
		if fmt.Sprint(err) != fmt.Sprint(wantErr) {
			t.Errorf("%.80q: %v, want %v", text, err, wantErr)
		} else if wantErr == nil && !reflect.DeepEqual(got, want) {
			t.Errorf("%.80q\ngot  %+v\nwant %+v", text, got, want)
		}
	}
	if _, err := readSavedState(filepath.Join(t.TempDir(), "none")); !os.IsNotExist(err) {
		t.Errorf("no file: %v", err)
	}
}
