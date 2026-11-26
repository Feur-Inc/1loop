// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"encoding/json"

	"1loop/internal/core"
)

// A chat written and read message by message (core/jsonstream.go): the
// desktop's, the server's and a copy's have the same three long lists.

// WriteChatJSON writes chat, a pointer to the caller's own copy, as
// json.Marshal encodes it; history, items and versions are its fields.
func WriteChatJSON(writer *core.JSONWriter, chat any, history *[]Message, items *[]ChatItem, versions *[]ChatVersion) {
	mark := core.JSONMark()
	writer.Around(chat,
		core.NewJSONHole(writer, history, Message{Role: mark}, func(m *Message) { writer.Value(m) }),
		core.NewJSONHole(writer, items, ChatItem{Kind: mark}, func(item *ChatItem) { writer.Value(item) }),
		core.NewJSONHole(writer, versions, ChatVersion{ID: mark}, func(version *ChatVersion) { writeVersionJSON(writer, *version) }))
}

// WriteMessagesJSON, WriteItemsJSON and WriteVersionsJSON write one list.
func WriteMessagesJSON(writer *core.JSONWriter, history []Message) {
	core.WriteJSONList(writer, history, func(m *Message) { writer.Value(m) })
}

func WriteItemsJSON(writer *core.JSONWriter, items []ChatItem) {
	core.WriteJSONList(writer, items, func(item *ChatItem) { writer.Value(item) })
}

func WriteVersionsJSON(writer *core.JSONWriter, versions []ChatVersion) {
	core.WriteJSONList(writer, versions, func(version *ChatVersion) { writeVersionJSON(writer, *version) })
}

func writeVersionJSON(writer *core.JSONWriter, version ChatVersion) {
	if version.Snapshot == nil {
		writer.Value(version)
		return
	}
	snapshot := *version.Snapshot
	version.Snapshot = &snapshot
	mark := core.JSONMark()
	writer.Around(version,
		core.NewJSONHole(writer, &snapshot.History, Message{Role: mark}, func(m *Message) { writer.Value(m) }),
		core.NewJSONHole(writer, &snapshot.Items, ChatItem{Kind: mark}, func(item *ChatItem) { writer.Value(item) }))
}

// ReadChatJSON reads the next value into chat, a pointer, as json.Unmarshal
// does; history, items and versions are its fields. Equal long texts are
// kept once. null says there was no chat.
func ReadChatJSON(reader *core.JSONReader, chat any, history *[]Message, items *[]ChatItem, versions *[]ChatVersion) (null bool, err error) {
	rest, err := reader.Object(map[string]func() error{
		"history": func() (err error) { *history, err = readMessages(reader); return err },
		"items":   func() (err error) { *items, err = readItems(reader); return err },
		"versions": func() (err error) {
			*versions, err = core.ReadJSONList(reader, func(version *ChatVersion) error { return readVersion(reader, version) })
			return err
		},
	})
	if err != nil || rest == nil {
		return err == nil, err
	}
	return false, json.Unmarshal(rest, chat)
}

func readVersion(reader *core.JSONReader, version *ChatVersion) error {
	rest, err := reader.Object(map[string]func() error{"snapshot": func() error {
		var snapshot VersionSnapshot
		rest, err := reader.Object(map[string]func() error{
			"history": func() (err error) { snapshot.History, err = readMessages(reader); return err },
			"items":   func() (err error) { snapshot.Items, err = readItems(reader); return err },
		})
		if err != nil || rest == nil {
			return err
		}
		version.Snapshot = &snapshot
		return json.Unmarshal(rest, &snapshot)
	}})
	if err != nil || rest == nil {
		return err
	}
	return json.Unmarshal(rest, version)
}

func readMessages(reader *core.JSONReader) ([]Message, error) {
	return core.ReadJSONList(reader, func(m *Message) error {
		if err := reader.Value(m); err != nil {
			return err
		}
		reader.Share(&m.Content)
		reader.Share(&m.ReasoningContent)
		for i := range m.Images {
			reader.Share(&m.Images[i].Data)
		}
		for i := range m.ToolCalls {
			reader.Share(&m.ToolCalls[i].Function.Arguments)
		}
		return nil
	})
}

func readItems(reader *core.JSONReader) ([]ChatItem, error) {
	return core.ReadJSONList(reader, func(item *ChatItem) error {
		if err := reader.Value(item); err != nil {
			return err
		}
		reader.Share(&item.Text)
		reader.Share(&item.Args)
		reader.Share(&item.Diff)
		for i := range item.Images {
			reader.Share(&item.Images[i].Data)
		}
		return nil
	})
}
