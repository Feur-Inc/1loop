// SPDX-License-Identifier: MPL-2.0

package projectsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// As they were, through the whole chat's JSON: the copies made before and
// those of other versions of 1loop compare revisions with these.

func wholeRevision(chat Chat) string {
	chat = wholePortable(chat)
	for i := range chat.Versions {
		if chat.Versions[i].Snapshot != nil {
			chat.Versions[i].Snapshot.Config = agent.Config{}
		}
	}
	data, _ := json.Marshal([]any{chat.Title, chat.History, chat.Items, chat.Todo, chat.Version, chat.Versions})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

func wholePortable(chat Chat) Chat {
	data, _ := json.Marshal(chat)
	var copied Chat
	_ = json.Unmarshal(data, &copied)
	cleanConfig := func(cfg agent.Config) agent.Config {
		identity := agent.IdentityOf(cfg)
		if cfg.RanWith != nil {
			identity = *cfg.RanWith
		}
		identity.AuthProfile = ""
		return agent.Config{Provider: cfg.Provider, Model: cfg.Model, Workspace: cfg.Workspace, RanWith: &identity}
	}
	cleanHistory := func(history []agent.Message) []agent.Message {
		out := history[:0]
		for _, message := range history {
			if message.Internal && strings.HasPrefix(message.Content, relocationPrefix) {
				continue
			}
			message.Usage, message.CachedTokens, message.UsageEstimated = 0, nil, false
			var responses []json.RawMessage
			for _, raw := range message.Responses {
				var item struct{ Type string }
				if json.Unmarshal(raw, &item) == nil && item.Type != "reasoning" {
					responses = append(responses, raw)
				}
			}
			message.Responses = responses
			out = append(out, message)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	cleanItems := func(items []agent.ChatItem) []agent.ChatItem {
		out := items[:0]
		for _, item := range items {
			if item.Kind != "note" || !strings.HasPrefix(item.Text, relocationPrefix) {
				out = append(out, item)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	copied.Config = cleanConfig(copied.Config)
	copied.History, copied.Items = cleanHistory(copied.History), cleanItems(copied.Items)
	for i := range copied.Versions {
		if snapshot := copied.Versions[i].Snapshot; snapshot != nil {
			snapshot.Config = cleanConfig(snapshot.Config)
			snapshot.History, snapshot.Items = cleanHistory(snapshot.History), cleanItems(snapshot.Items)
		}
	}
	return copied
}

// A chat made portable without its JSON travels, and has the revision, as
// when it went through it.
func TestPortableMatchesItsJSON(t *testing.T) {
	picture := core.ImagePart{Mime: "image/png", Data: strings.Repeat("iVBOR", 100)}
	cached := 4
	history := []agent.Message{{Role: "user", Content: "look <&>", Images: []core.ImagePart{picture}, Usage: 7, CachedTokens: &cached},
		{Role: "user", Internal: true, Content: relocationPrefix + " elsewhere"},
		{Role: "assistant", Responses: []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`), json.RawMessage(` {"type" : "message"}`), json.RawMessage(`3`)}, ToolCalls: []agent.ToolCall{}},
		{Role: "assistant", Images: []core.ImagePart{}, ReasoningDetails: []map[string]any{{"a": 1}}}}
	items := []agent.ChatItem{{ID: "1", Kind: "user", Text: "look", Images: []core.ImagePart{picture}}, {Kind: "note", Text: relocationPrefix}, {Kind: "note", Text: "kept"}}
	for i, chat := range []Chat{
		{}, {History: []agent.Message{}, Items: []agent.ChatItem{}, Versions: []agent.ChatVersion{}, Todo: &agent.TodoState{}},
		{History: history[1:2], Items: items[1:2]},
		{ID: "a", Title: "Title", Config: agent.Config{Provider: "claudecode", Model: "m", AuthProfile: "mine", Workspace: "/w", Session: "s"}, History: history, Items: items, Version: "v2",
			Versions: []agent.ChatVersion{{ID: "v1", Selected: map[string]string{}}, {ID: "v2", Selected: map[string]string{"a": "b"}, Snapshot: &agent.VersionSnapshot{Config: agent.Config{Provider: "vllm", AuthProfile: "mine"}, History: history, Items: items, SharedItems: 2}}}},
		{Config: agent.Config{Provider: "openai", RanWith: &agent.RunIdentity{Provider: "vllm", AuthProfile: "mine"}}, History: history[:1]},
	} {
		before, _ := json.Marshal(chat)
		got, _ := json.Marshal(chat.Portable())
		want, _ := json.Marshal(wholePortable(chat))
		if !bytes.Equal(got, want) {
			t.Errorf("%d\ngot  %s\nwant %s", i, got, want)
		}
		if got, want := chat.Revision(), wholeRevision(chat); got != want {
			t.Errorf("%d: revision %s, want %s", i, got, want)
		}
		var out bytes.Buffer
		if size, err := chat.writeJSON(&out); err != nil || size != int64(len(before)) || !bytes.Equal(out.Bytes(), before) {
			t.Errorf("%d: written %s, %v", i, out.Bytes(), err)
		}
		// Independent: what is done to it stays there.
		portable := chat.Portable().AtWorkspace(agent.Config{Workspace: "/there"})
		for j := range portable.History {
			portable.History[j].Content = "changed"
		}
		for j := range portable.Items {
			portable.Items[j].Text = "changed"
		}
		if after, _ := json.Marshal(chat); !bytes.Equal(after, before) {
			t.Errorf("%d: the chat itself was changed", i)
		}
	}
	if chat := (Chat{History: history}); !reflect.DeepEqual(chat.Portable().History[0].Images, chat.History[0].Images) {
		t.Error("pictures differ")
	}
}
