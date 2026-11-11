// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/usage"
)

func ledgerRecords(ledger *usage.Ledger) []usage.Record {
	return ledger.Between(time.Time{}, time.Time{})
}

// A chat completion's usage reaches the ledger with its chat, turn,
// project and branch, cached tokens apart from the fresh ones.
func TestChatCompletionRecordsUsage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"model\":\"vendor/real-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":50," +
			"\"prompt_tokens_details\":{\"cached_tokens\":600,\"cache_write_tokens\":100},\"completion_tokens_details\":{\"reasoning_tokens\":20},\"cost\":0.0123}}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer server.Close()
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".git"), 0700)
	os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0600)
	ledger := usage.Open("")
	cfg := withTurn(Config{Provider: "compatible", ConnectionID: "srv", Model: "m", BaseURL: server.URL, Workspace: project,
		Usage: &UsageTag{Ledger: ledger, Chat: "chat1", Parent: "parent1"}})
	if _, err := completion(context.Background(), cfg, []Message{{Role: "user", Content: "hello"}}, false, func(string, any) {}); err != nil {
		t.Fatal(err)
	}
	records := ledgerRecords(ledger)
	if len(records) != 1 {
		t.Fatalf("records %+v", records)
	}
	record := records[0]
	want := usage.Tokens{Input: 300, Output: 50, CacheRead: 600, CacheWrite: 100, Reasoning: 20}
	if record.Tokens != want || record.Model != "vendor/real-model" || record.Cost == nil || *record.Cost != 0.0123 {
		t.Errorf("record %+v", record)
	}
	if record.Chat != "chat1" || record.Parent != "parent1" || record.Turn == "" || record.Project != project || record.Branch != "feature/x" || record.Connection != "srv" {
		t.Errorf("tags %+v", record)
	}
	// A chat without a folder has no project.
	scratch := withTurn(cfg)
	scratch.Scratch = scratch.Workspace
	recordUsage(scratch, spentTokens{tokens: usage.Tokens{Input: 1}})
	if last := ledgerRecords(ledger)[1]; last.Project != "" || last.Branch != "" || last.Turn == record.Turn {
		t.Errorf("scratch %+v", last)
	}
}

func TestResponsesUsage(t *testing.T) {
	stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-5.1-codex\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]," +
		"\"usage\":{\"input_tokens\":900,\"output_tokens\":40,\"input_tokens_details\":{\"cached_tokens\":800},\"output_tokens_details\":{\"reasoning_tokens\":30}}}}\n\n"
	out, err := readResponses(strings.NewReader(stream), func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	if out.spent == nil || out.spent.model != "gpt-5.1-codex" || out.spent.tokens != (usage.Tokens{Input: 100, CacheRead: 800, Output: 40, Reasoning: 30}) {
		t.Fatalf("spent %+v", out.spent)
	}
}

// Claude Code repeats a message's usage on each block and sends the final
// output count apart: each message is one call.
func TestClaudeTallyCountsEachMessageOnce(t *testing.T) {
	ledger := usage.Open("")
	tally := &callTally{cfg: Config{Provider: "claudecode", Model: "opus", Usage: &UsageTag{Ledger: ledger, Chat: "c"}}}
	first := &claudeMessage{ID: "m1", Model: "claude-opus-5-5", Usage: claudeUsage{Input: 3, CacheRead: 5000, CacheCreation: 200, Output: 1}}
	tally.seeClaude(first)                                                                                       // message_start
	tally.see(tally.id, "", 0, 0, 0, 0)                                                                          // a content block
	tally.seeClaude(first)                                                                                       // the text block's assistant event
	tally.see(tally.id, "", 3, 250, 5000, 200)                                                                   // message_delta
	tally.seeClaude(&claudeMessage{ID: "m1", Model: "claude-opus-5-5", Usage: claudeUsage{Input: 3, Output: 1}}) // the tool block's
	tally.seeClaude(&claudeMessage{ID: "m2", Usage: claudeUsage{Input: 2, CacheRead: 5300, Output: 90}})
	tally.flush()
	tally.flush()
	records := ledgerRecords(ledger)
	if len(records) != 2 {
		t.Fatalf("records %+v", records)
	}
	if records[0].Model != "claude-opus-5-5" || records[0].Tokens != (usage.Tokens{Input: 3, Output: 250, CacheRead: 5000, CacheWrite: 200}) {
		t.Errorf("first %+v", records[0])
	}
	if records[1].Model != "opus" || records[1].Output != 90 {
		t.Errorf("second %+v", records[1])
	}
}
