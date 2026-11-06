// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"1loop/internal/core"
)

// VersionState is a view of a conversation while editing or selecting a
// branch. Callers keep the live runner, TODO and cache outside snapshots.
type VersionState struct {
	Config   Config
	History  []Message
	Items    []ChatItem
	Version  string
	Versions []ChatVersion
}

// Only inactive versions hold snapshots. The active conversation remains
// the source used by the runner, persistence and remote views.
type VersionSnapshot struct {
	Config  Config     `json:"config"`
	History []Message  `json:"history"`
	Items   []ChatItem `json:"items"`
	// In a saved file only (CompactVersions): how many leading messages and
	// items are the active conversation's, left out of History and Items.
	SharedHistory int `json:"sharedHistory,omitempty"`
	SharedItems   int `json:"sharedItems,omitempty"`
}

type ChatVersion struct {
	ID       string            `json:"id"`
	Parent   string            `json:"parent,omitempty"`
	Fork     string            `json:"fork,omitempty"`
	Snapshot *VersionSnapshot  `json:"snapshot,omitempty"`
	Selected map[string]string `json:"selected,omitempty"` // fork -> last continuation visited
}

func (chat *VersionState) IdentifyMessages() {
	for i := range chat.Items {
		if chat.Items[i].ID == "" {
			chat.Items[i].ID = core.Identifier()[:16]
		}
	}
}

func (chat *VersionState) versionIndex(id string) int {
	return slices.IndexFunc(chat.Versions, func(chatVersionValue ChatVersion) bool { return chatVersionValue.ID == id })
}

// Edits of one prompt (or retries of its response) form a family. Later
// forks at another message stay inside their chosen family's continuation.
func (chat *VersionState) Family(anchor string) (family []int, selected int) {
	edges := make([][]int, len(chat.Versions))
	for i, chatVersion := range chat.Versions {
		if chatVersion.Fork == anchor {
			if parent := chat.versionIndex(chatVersion.Parent); parent >= 0 {
				edges[i] = append(edges[i], parent)
				edges[parent] = append(edges[parent], i)
			}
		}
	}
	member := chat.versionIndex(chat.Version)
	for member >= 0 && len(edges[member]) == 0 {
		if strings.HasSuffix(anchor, ":reply") && chat.Versions[member].Fork == strings.TrimSuffix(anchor, ":reply") {
			return nil, 0 // retries of another wording belong to that prompt's version
		}
		member = chat.versionIndex(chat.Versions[member].Parent)
	}
	if member < 0 {
		return nil, 0
	}
	seen := map[int]bool{member: true}
	queue := []int{member}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, next := range edges[at] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	for i := range chat.Versions {
		if seen[i] {
			if i == member {
				selected = len(family)
			}
			family = append(family, i)
		}
	}
	return family, selected
}

func (chat *VersionState) rememberVersion() {
	anchors := map[string]bool{}
	for _, chatVersion := range chat.Versions {
		if chatVersion.Fork != "" {
			anchors[chatVersion.Fork] = true
		}
	}
	for anchor := range anchors {
		prompt := strings.TrimSuffix(anchor, ":reply")
		if !slices.ContainsFunc(chat.Items, func(item ChatItem) bool { return item.Kind == "user" && item.ID == prompt }) {
			continue // an edit of an earlier prompt removed this later branch point
		}
		family, selected := chat.Family(anchor)
		if len(family) > 1 {
			chatVersionValue := &chat.Versions[family[selected]]
			if chatVersionValue.Selected == nil {
				chatVersionValue.Selected = map[string]string{}
			}
			chatVersionValue.Selected[anchor] = chat.Version
		}
	}
	chat.Versions[chat.versionIndex(chat.Version)].Snapshot = &VersionSnapshot{
		Config: chat.Config, History: append([]Message{}, chat.History...), Items: append([]ChatItem{}, chat.Items...),
	}
}

// Match user messages backwards: this also works after compaction removes
// an earlier prefix, and disambiguates repeated prompts such as "continue".
func (chat *VersionState) historyBefore(index int) []Message {
	cursor := len(chat.History)
	for i := len(chat.Items) - 1; i >= index; i-- {
		item := chat.Items[i]
		if item.Kind != "user" {
			continue
		}
		found := -1
		for j := cursor - 1; j >= 0; j-- {
			messageValue := chat.History[j]
			if messageValue.Role == "user" && messageValue.Content == AttachmentText(item.Text, item.Files) && slices.Equal(messageValue.Images, item.Images) {
				found = j
				break
			}
		}
		if found < 0 {
			break
		}
		cursor = found
		if i == index {
			return append([]Message{}, chat.History[:cursor]...)
		}
	}
	// Older messages may exist only in the transcript after compaction.
	// Rebuild that visible prefix without pulling in the discarded future.
	var history []Message
	for i, item := range chat.Items[:index] {
		switch item.Kind {
		case "user":
			history = append(history, PromptMessage(item.Text, item.Images, item.Files))
		case "assistant":
			history = append(history, Message{Role: item.Kind, Content: item.Text, Images: item.Images})
		case "tool":
			call := ToolCall{ID: fmt.Sprintf("earlier-%d", i), Type: "function"}
			call.Function.Name, call.Function.Arguments = item.Tool, item.Args
			if call.Function.Arguments == "" {
				call.Function.Arguments = "{}"
			}
			history = append(history, Message{Role: "assistant", ToolCalls: []ToolCall{call}},
				Message{Role: "tool", ToolCallID: call.ID, Content: item.Text})
		}
	}
	return history
}

func (chat *VersionState) ForkMessage(index int, response bool) string {
	chat.IdentifyMessages()
	if chat.Version == "" {
		chat.Version = core.Identifier()[:16]
		chat.Versions = []ChatVersion{{ID: chat.Version}}
	}
	chat.rememberVersion()
	anchor := chat.Items[index].ID
	if response {
		anchor += ":reply"
	}
	parent := chat.Version
	if family, selected := chat.Family(anchor); len(family) > 1 {
		parent = chat.Versions[family[selected]].ID
	}
	chat.Version = core.Identifier()[:16]
	chat.Versions = append(chat.Versions, ChatVersion{ID: chat.Version, Parent: parent, Fork: anchor})
	chat.History = chat.historyBefore(index)
	id := chat.Items[index].ID
	chat.Items = append([]ChatItem{}, chat.Items[:index]...)
	chat.Config.ClearSessions() // Claude must not resume a session containing the discarded future.
	return id
}

func (chat *VersionState) SelectVersion(anchor string, delta int) bool {
	family, selected := chat.Family(anchor)
	to := selected + delta
	if to < 0 || to >= len(family) {
		return false
	}
	chat.rememberVersion()
	target := family[to]
	if leaf := chat.versionIndex(chat.Versions[target].Selected[anchor]); leaf >= 0 {
		target = leaf
	}
	chatVersion := &chat.Versions[target]
	if chatVersion.Snapshot == nil {
		return false
	}
	chat.Version, chat.Config = chatVersion.ID, chatVersion.Snapshot.Config
	chat.History = append([]Message{}, chatVersion.Snapshot.History...)
	chat.Items = append([]ChatItem{}, chatVersion.Snapshot.Items...)
	chatVersion.Snapshot = nil
	return true
}

func CopyVersions(versions []ChatVersion) []ChatVersion {
	copy := append([]ChatVersion{}, versions...)
	for i, chatVersionValue := range copy {
		if chatVersionValue.Selected != nil {
			copy[i].Selected = map[string]string{}
			for anchor, id := range chatVersionValue.Selected {
				copy[i].Selected[anchor] = id
			}
		}
	}
	return copy
}

// CompactVersions is versions as they are saved: each snapshot without the
// leading messages and items it shares with the active conversation (history
// and items), which a long chat otherwise repeated in full for every edit.
// The snapshots of versions are new ones: the live ones are not changed.
func CompactVersions(versions []ChatVersion, history []Message, items []ChatItem) []ChatVersion {
	out := CopyVersions(versions)
	for i, version := range out {
		snapshot := version.Snapshot
		if snapshot == nil || snapshot.SharedHistory > 0 || snapshot.SharedItems > 0 {
			continue
		}
		messages := sharedPrefix(snapshot.History, history)
		rows := sharedPrefix(snapshot.Items, items)
		out[i].Snapshot = &VersionSnapshot{Config: snapshot.Config, History: snapshot.History[messages:], Items: snapshot.Items[rows:],
			SharedHistory: messages, SharedItems: rows}
	}
	return out
}

// ExpandVersions undoes CompactVersions once a chat is loaded: the shared
// part comes back from the active conversation, whose strings it shares.
func ExpandVersions(versions []ChatVersion, history []Message, items []ChatItem) []ChatVersion {
	for i := range versions {
		snapshot := versions[i].Snapshot
		if snapshot == nil || (snapshot.SharedHistory == 0 && snapshot.SharedItems == 0) {
			continue
		}
		messages, rows := min(snapshot.SharedHistory, len(history)), min(snapshot.SharedItems, len(items))
		versions[i].Snapshot = &VersionSnapshot{Config: snapshot.Config,
			History: append(append([]Message{}, history[:messages]...), snapshot.History...),
			Items:   append(append([]ChatItem{}, items[:rows]...), snapshot.Items...)}
	}
	return versions
}

// sharedPrefix is how many leading elements of a and b are equal.
func sharedPrefix[T any](a, b []T) int {
	n := 0
	for n < len(a) && n < len(b) && reflect.DeepEqual(a[n], b[n]) {
		n++
	}
	return n
}
