// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// The state written message by message is what json.Marshal writes, and
// reads as json.Unmarshal reads it.
func TestServerStateJSON(t *testing.T) {
	long := strings.Repeat("output <&>\n", 100)
	chats := []*serverChat{
		{ID: "a", Title: "First", History: []agent.Message{{Role: "user", Content: long}}, Items: []agent.ChatItem{{Kind: "user", Text: long}},
			Versions: []agent.ChatVersion{{ID: "v", Snapshot: &agent.VersionSnapshot{History: []agent.Message{{Role: "user", Content: "other"}}}}}, Todo: &agent.TodoState{}},
		{ID: "b", History: []agent.Message{}},
	}
	for _, chats := range [][]*serverChat{chats, {}} {
		want, _ := json.Marshal(struct {
			Chats   []*serverChat            `json:"chats"`
			Devices []remoteapi.PairedDevice `json:"devices"`
		}{chats, []remoteapi.PairedDevice{}})
		var out bytes.Buffer
		if err := writeServerState(&out, chats, []remoteapi.PairedDevice{}); err != nil || !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("%v\ngot  %s\nwant %s", err, out.Bytes(), want)
		}
		path := filepath.Join(t.TempDir(), "state.json")
		os.WriteFile(path, want, 0o600)
		var whole struct {
			Chats   []*serverChat
			Devices []remoteapi.PairedDevice
		}
		json.Unmarshal(want, &whole)
		got, devices, err := readServerState(path)
		if err != nil || !reflect.DeepEqual(got, whole.Chats) || !reflect.DeepEqual(devices, whole.Devices) {
			t.Fatalf("%v: %+v", err, got)
		}
		os.WriteFile(path, want[:len(want)-1], 0o600)
		if _, _, err := readServerState(path); err == nil {
			t.Error("half a state was read")
		}
	}
	if _, _, err := readServerState(filepath.Join(t.TempDir(), "none")); !os.IsNotExist(err) {
		t.Errorf("no file: %v", err)
	}
}
