// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"1loop/internal/core"
)

type jsonChat struct {
	ID       string        `json:"id"`
	Config   Config        `json:"config"`
	History  []Message     `json:"history"`
	Items    []ChatItem    `json:"items"`
	Todo     *TodoState    `json:"todo,omitempty"`
	Versions []ChatVersion `json:"versions,omitempty"`
	ReportTo []string      `json:"reportTo,omitempty"`
}

func jsonChats() []jsonChat {
	picture := core.ImagePart{Mime: "image/png", Data: strings.Repeat("iVBOR", 2000)}
	call := ToolCall{ID: "c", Type: "function"}
	call.Function.Name, call.Function.Arguments = "shell", `{"command":"`+strings.Repeat("ls ", 200)+`"}`
	cached := 3
	history := []Message{{Role: "user", Content: "look <&>   \xff", Images: []core.ImagePart{picture}},
		{Role: "assistant", ToolCalls: []ToolCall{call}, Responses: []json.RawMessage{json.RawMessage(` { "type" : "x" } `)}, CachedTokens: &cached, ReasoningDetails: []map[string]any{{"n": 1.5}}},
		{Role: "tool", ToolCallID: "c", Content: strings.Repeat("out\n", 300), Images: []core.ImagePart{picture}}}
	items := []ChatItem{{ID: "1", Kind: "user", Text: history[0].Content, Images: []core.ImagePart{picture}},
		{ID: "2", Kind: "tool", Tool: "shell", Args: call.Function.Arguments, Text: history[2].Content, Images: []core.ImagePart{picture}, From: &ChatSender{ID: "s"}}}
	return []jsonChat{
		{},
		{ID: "empty", History: []Message{}, Items: []ChatItem{}, Versions: []ChatVersion{}},
		{ID: "full", Config: Config{Provider: "vllm"}, History: history, Items: items, Todo: &TodoState{items: []TodoItem{{ID: "t", Text: `"history":null`}}}, ReportTo: []string{"x"},
			Versions: []ChatVersion{{ID: "v1"}, {ID: "v2", Parent: "v1", Selected: map[string]string{"a": "b"}, Snapshot: &VersionSnapshot{History: history[:2], Items: items[:1], SharedItems: 4}},
				{ID: "v3", Snapshot: &VersionSnapshot{History: []Message{}}}}},
		{ID: "one item", Items: items[1:]},
	}
}

// A chat written message by message is what json.Marshal writes, and reads
// as json.Unmarshal reads it.
func TestChatJSON(t *testing.T) {
	for _, chat := range jsonChats() {
		before, _ := json.Marshal(chat)
		var out bytes.Buffer
		writer := core.NewJSONWriter(&out)
		copied := chat
		WriteChatJSON(writer, &copied, &copied.History, &copied.Items, &copied.Versions)
		if writer.Err() != nil || !bytes.Equal(out.Bytes(), before) {
			t.Fatalf("%s: %v\ngot  %.600s\nwant %.600s", chat.ID, writer.Err(), out.String(), before)
		}
		if after, _ := json.Marshal(chat); !bytes.Equal(after, before) {
			t.Fatalf("%s: writing it changed the chat", chat.ID)
		}
		var want, got jsonChat
		if err := json.Unmarshal(before, &want); err != nil {
			t.Fatal(err)
		}
		null, err := ReadChatJSON(core.NewJSONReader(bytes.NewReader(before)), &got, &got.History, &got.Items, &got.Versions)
		if err != nil || null || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v, null %v\ngot  %+v\nwant %+v", chat.ID, err, null, got, want)
		}
	}
	var chat jsonChat
	if null, err := ReadChatJSON(core.NewJSONReader(strings.NewReader("null")), &chat, &chat.History, &chat.Items, &chat.Versions); err != nil || !null {
		t.Errorf("null: %v, %v", null, err)
	}
	for _, broken := range []string{`{"history":[{"role":5}]}`, `{"history":{}}`, `{"items":[`, `{"versions":[{"snapshot":{"items":[1]}}]}`} {
		if _, err := ReadChatJSON(core.NewJSONReader(strings.NewReader(broken)), &chat, &chat.History, &chat.Items, &chat.Versions); err == nil {
			t.Errorf("%s was read", broken)
		}
	}
}

// A picture found in the chat, in the model's history and in a version is
// once in memory.
func TestChatJSONSharesPictures(t *testing.T) {
	data, _ := json.Marshal(jsonChats()[2])
	var chat jsonChat
	if _, err := ReadChatJSON(core.NewJSONReader(bytes.NewReader(data)), &chat, &chat.History, &chat.Items, &chat.Versions); err != nil {
		t.Fatal(err)
	}
	at := func(text string) *byte { return unsafe.StringData(text) }
	one := at(chat.History[0].Images[0].Data)
	if at(chat.Items[0].Images[0].Data) != one || at(chat.Items[1].Images[0].Data) != one || at(chat.Versions[1].Snapshot.History[0].Images[0].Data) != one {
		t.Error("the same picture is several times in memory")
	}
	if at(chat.History[2].Content) != at(chat.Items[1].Text) || at(chat.History[1].ToolCalls[0].Function.Arguments) != at(chat.Items[1].Args) {
		t.Error("the same output is twice in memory")
	}
}

// Writing a large chat takes about one message's memory, not the chat's.
func TestChatJSONMemory(t *testing.T) {
	chat := jsonChat{ID: "large"}
	for i := range 200 {
		chat.Items = append(chat.Items, ChatItem{Kind: "tool", Images: []core.ImagePart{{Mime: "image/png", Data: strings.Repeat(string(rune('a'+i%26)), 100_000)}}})
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	writer := core.NewJSONWriter(discard{})
	WriteChatJSON(writer, &chat, &chat.History, &chat.Items, &chat.Versions)
	runtime.ReadMemStats(&after)
	if used := after.TotalAlloc - before.TotalAlloc; writer.Written < 20_000_000 || used > 4<<20 {
		t.Errorf("%d bytes written with %d allocated", writer.Written, used)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// The end of a running command's output, as String has it.
func TestOutputBufferLast(t *testing.T) {
	for _, size := range []int{0, 5, 4096, 20000, outputKeep - 1, outputKeep, outputKeep + 10, outputKeep + 5000, 5 * outputKeep} {
		buffer := &outputBuffer{}
		text := strings.Repeat("0123456789abcde\n", size/16+1)[:size]
		for rest := text; rest != ""; {
			n := min(len(rest), 7777)
			buffer.Write([]byte(rest[:n]))
			rest = rest[n:]
		}
		whole := buffer.String()
		if got, want := buffer.last(4096), whole[max(0, len(whole)-4096):]; got != want {
			t.Errorf("%d bytes: the last are %d, want %d", size, len(got), len(want))
		}
		if !strings.HasSuffix(whole, text[max(0, len(text)-outputKeep):]) || buffer.total != size || cap(buffer.tail) > 4*outputKeep {
			t.Errorf("%d bytes: kept %d, total %d, room %d", size, len(whole), buffer.total, cap(buffer.tail))
		}
	}
}
