// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// summaryModel is a model that writes summaries; fail answers instead when
// it returns an error status.
func summaryModel(t *testing.T, fail func(call int) int) (Config, *[]string) {
	t.Helper()
	var calls atomic.Int32
	prompts := &[]string{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"m"}]}`)
			return
		}
		var request struct{ Messages []struct{ Content string } }
		json.Unmarshal(body, &request)
		call := int(calls.Add(1))
		if status := fail(call); status != 0 {
			http.Error(w, `{"error":{"message":"the model broke"}}`, status)
			return
		}
		*prompts = append(*prompts, request.Messages[len(request.Messages)-1].Content)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"## Goal: notes %d\"}}]}\n\n", call)
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(model.Close)
	return Config{Provider: "vllm", ConnectionID: "c", Model: "m", BaseURL: model.URL + "/v1", Workspace: t.TempDir(), Compaction: &CompactionState{}}, prompts
}

// importedHistory is a long conversation from another app.
func importedHistory(rounds int) []Message {
	var history []Message
	for i := range rounds {
		id := fmt.Sprintf("call%d", i)
		call := ToolCall{ID: id, Type: "function"}
		call.Function.Name, call.Function.Arguments = "shell", fmt.Sprintf(`{"command":"step %d"}`, i)
		history = append(history,
			Message{Role: "assistant", Content: fmt.Sprintf("Working on step %d. ", i) + strings.Repeat("Explained at length. ", 60), ToolCalls: []ToolCall{call}},
			Message{Role: "tool", ToolCallID: id, Content: fmt.Sprintf("output %d\n", i) + strings.Repeat("a line of output\n", 200)})
		if i%20 == 0 {
			history = append(history, Message{Role: "user", Content: fmt.Sprintf("request %d", i)})
		}
	}
	return history
}

// A conversation larger than the model's window is summarized in parts, and
// one that stops goes on from the parts it has (#128).
func TestCompactionInParts(t *testing.T) {
	history := importedHistory(900)
	broken := true
	cfg, prompts := summaryModel(t, func(call int) int {
		if broken && call > 2 {
			return http.StatusBadRequest
		}
		return 0
	})
	var notes []string
	emit := func(kind string, data any) {
		if kind == "context_note" {
			notes = append(notes, data.(string))
		}
	}
	if _, err := (&App{}).CompactNow(context.Background(), cfg, history, "", emit); err == nil {
		t.Fatal("a broken model compacted")
	}
	pending := cfg.Compaction.Interrupted()
	if pending == nil || len(pending.Parts) != 2 || pending.Total < 3 || pending.Error == "" {
		t.Fatalf("pending %+v", pending)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "2 of") || !strings.Contains(notes[0], "goes on where it stopped") {
		t.Errorf("notes %q", notes)
	}
	// 1loop is closed and opened again.
	data, _ := json.Marshal(cfg.Compaction)
	restored := &CompactionState{}
	if err := json.Unmarshal(data, restored); err != nil {
		t.Fatal(err)
	}
	cfg.Compaction, broken, notes = restored, false, nil
	before := len(*prompts)
	kept, err := (&App{}).CompactNow(context.Background(), cfg, history, "", emit)
	if err != nil {
		t.Fatal(err, notes)
	}
	if asked := len(*prompts) - before; asked != pending.Total-2+1 { // the parts left, then the handoff
		t.Errorf("%d requests for %d parts, 2 done", asked, pending.Total)
	}
	if len(notes) == 0 || !strings.HasPrefix(notes[0], "Compaction resumed · 2 of") {
		t.Errorf("notes %q", notes)
	}
	last := (*prompts)[len(*prompts)-1]
	if !strings.Contains(last, "Notes on part 1 of") || !strings.Contains(last, "## Decisions") {
		t.Errorf("handoff asked with %.300q", last)
	}
	if cfg.Compaction.Interrupted() != nil {
		t.Error("still pending")
	}
	if len(retainedRequests(kept)) != len(retainedRequests(history)) {
		t.Error("a request of the user was lost")
	}
	records := cfg.Compaction.List()
	if len(records) != 1 || records[0].After >= records[0].Before/2 || len(records[0].Was) == 0 || len(records[0].Now) != len(kept) {
		t.Fatalf("records %d", len(records))
	}
	if FindCompaction(records, "Context compacted · "+records[0].Label()+". Earlier…") == nil {
		t.Error("the note does not find its record")
	}
	for _, message := range kept {
		if message.Role == "tool" && len(message.ToolCallID) == 0 {
			t.Error("a tool result without its call")
		}
	}
}

// A stop in the middle leaves the compaction to resume; three failures give
// up.
func TestCompactionInterrupted(t *testing.T) {
	cfg, _ := summaryModel(t, func(int) int { return http.StatusBadRequest })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	quiet := func(string, any) {}
	compactHistory(ctx, cfg, importedHistory(60), 0, "", true, quiet)
	if pending := cfg.Compaction.Interrupted(); pending == nil || pending.Error != "stopped" {
		t.Fatalf("stopped: %+v", pending)
	}
	cfg.Compaction.drop()
	for range maxCompactionAttempts {
		compactHistory(context.Background(), cfg, importedHistory(60), 0, "", true, quiet)
	}
	if pending := cfg.Compaction.Interrupted(); pending != nil {
		t.Errorf("still pending after %d failures", pending.Attempts)
	}
}

// What the summary reads: no reminders, what was only read in short, errors
// at the end of an output kept, the same output once.
func TestDigestMessages(t *testing.T) {
	read, shell, again := ToolCall{ID: "r"}, ToolCall{ID: "s"}, ToolCall{ID: "t"}
	read.Function.Name, shell.Function.Name, again.Function.Name = "read_file", "shell", "shell"
	output := strings.Repeat("noise\n", 2000) + "FAIL: TestX"
	text := strings.Join(digestMessages([]Message{
		{Role: "user", Content: "fix it"},
		{Role: "user", Internal: true, Content: "[1loop context control] reminder"},
		{Role: "assistant", Content: "I read.", ToolCalls: []ToolCall{read, shell, again}},
		{Role: "tool", ToolCallID: "r", Content: strings.Repeat("source\n", 500)},
		{Role: "tool", ToolCallID: "s", Content: output},
		{Role: "tool", ToolCallID: "t", Content: output},
	}, 1), "\n")
	for _, want := range []string{"USER: fix it", "ASSISTANT: I read.", "CALL read_file", "FAIL: TestX"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is missing", want)
		}
	}
	if strings.Contains(text, "reminder") || strings.Count(text, "FAIL: TestX") != 1 || len(text) > 2000 {
		t.Errorf("%d bytes: %.400q", len(text), text)
	}
}

// Pasted files in old requests are shortened only when they would keep the
// context from being compacted; the last requests never are.
func TestShortenRequests(t *testing.T) {
	long := strings.Repeat("pasted ", 3000)
	messages := []Message{{Role: "user", Content: "a" + long, RecallID: "id1"}, {Role: "assistant", Content: "ok"}, {Role: "user", Content: "b" + long}, {Role: "user", Content: "c" + long}}
	if got := shortenRequests(messages, 1<<20); len(got[0].Content) != len(messages[0].Content) {
		t.Error("shortened with room to spare")
	}
	got := shortenRequests(messages, 12000)
	if len(got[0].Content) > 9000 || !strings.Contains(got[0].Content, "recall read id=id1") || got[2].Content != messages[2].Content || got[3].Content != messages[3].Content {
		t.Errorf("sizes %d %d %d", len(got[0].Content), len(got[2].Content), len(got[3].Content))
	}
	if messages[0].Content != "a"+long {
		t.Error("the conversation itself was changed")
	}
}
