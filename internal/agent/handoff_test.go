package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func longHistory(rounds, size int) []Message {
	var history []Message
	for i := 0; i < rounds; i++ {
		call := ToolCall{ID: "call" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), Type: "function"}
		call.Function.Name, call.Function.Arguments = "read_file", `{"path":"main.go"}`
		history = append(history,
			Message{Role: "user", Content: "request " + strings.Repeat("u", 40)},
			Message{Role: "assistant", Content: "looking", ToolCalls: []ToolCall{call}, Usage: 999999},
			Message{Role: "tool", ToolCallID: call.ID, Content: strings.Repeat("o", size)},
			Message{Role: "assistant", Content: "done " + strings.Repeat("a", 40)})
	}
	return history
}

func TestHandoffNeeded(t *testing.T) {
	cfg := Config{Provider: "openai", ConnectionID: "chatgpt", Model: "gpt-6-luna"}
	history := []Message{{Role: "user", Content: "hi"}}
	if HandoffNeeded(cfg, history) {
		t.Fatal("a chat that never ran needs no handoff")
	}
	same := IdentityOf(cfg)
	cfg.RanWith = &same
	if HandoffNeeded(cfg, history) {
		t.Fatal("same model: no handoff")
	}
	other := RunIdentity{Provider: "vllm", ConnectionID: "local", Model: "qwen"}
	cfg.RanWith = &other
	if !HandoffNeeded(cfg, history) {
		t.Fatal("another provider needs a handoff")
	}
	// Claude Code changes models within its session: no handoff for that.
	claude := Config{Provider: "claudecode", ConnectionID: "claude", Model: "opus"}
	ran := IdentityOf(Config{Provider: "claudecode", ConnectionID: "claude", Model: "sonnet"})
	claude.RanWith = &ran
	if HandoffNeeded(claude, history) {
		t.Fatal("a model change inside Claude Code needs no handoff")
	}
}

func TestPlanHandoff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // learned windows are saved there
	small := longHistory(2, 100)
	previous := Config{Provider: "vllm", ConnectionID: "local", Model: "qwen"}
	next := Config{Provider: "compatible", ConnectionID: "other", Model: "unknown-model"}
	learnWindow("other", "unknown-model", 100000)

	plan := PlanHandoff(&previous, next, small, nil, "Qwen", "Other")
	if plan.Recommended.Mode != "as_is" || plan.AsIsProblem != "" {
		t.Fatalf("a small conversation goes as it is: %+v", plan)
	}

	big := longHistory(30, 8000) // ~60k tokens of outputs
	plan = PlanHandoff(&previous, next, big, nil, "Qwen", "Other")
	if plan.Recommended.Mode != "summary" || plan.Recommended.Summarizer != "previous" {
		t.Fatalf("a large conversation is summarized by the cached previous model: %+v", plan.Recommended)
	}
	if plan.Problem(plan.Recommended) != "" {
		t.Fatalf("the recommendation must be valid: %s", plan.Problem(plan.Recommended))
	}

	huge := longHistory(40, 12000) // larger than the 100k window
	plan = PlanHandoff(nil, next, huge, nil, "Qwen", "Other")
	if plan.AsIsProblem == "" || plan.Problem(HandoffChoice{Mode: "as_is"}) == "" {
		t.Fatal("a conversation larger than the window cannot go as it is")
	}
	if plan.Summarizers[0].Problem == "" {
		t.Fatal("the previous model is gone: unavailable")
	}
	if plan.Recommended.Summarizer != "next" || plan.Problem(plan.Recommended) != "" {
		t.Fatalf("without the previous model, the new one summarizes a level it can read: %+v %s", plan.Recommended, plan.Problem(plan.Recommended))
	}
	if plan.Problem(HandoffChoice{Mode: "summary", Summarizer: "next", Level: LevelEverything}) == "" {
		t.Fatal("the whole conversation does not fit the new model's window")
	}

	// Back to Claude Code with its session: only the gap.
	claude := Config{Provider: "claudecode", ConnectionID: "claude", Session: "s1", SessionMark: 2}
	items := []ChatItem{{Kind: "user", Text: "a"}, {Kind: "assistant", Text: "b"}, {Kind: "user", Text: "meanwhile"}, {Kind: "assistant", Text: "with chatgpt"}}
	plan = PlanHandoff(&previous, claude, big, items, "Qwen", "Claude")
	if plan.Recommended.Mode != "as_is" || !strings.Contains(plan.AsIsNote, "resumes its own session") {
		t.Fatalf("Claude Code resumes and reads the gap: %+v", plan)
	}
}

func TestForProvider(t *testing.T) {
	history := []Message{
		{Role: "assistant", Usage: 5000, Responses: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"x"}`), json.RawMessage(`{"type":"message","role":"assistant"}`)}},
		{Role: "assistant", Responses: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"y"}`)}},
	}
	out := forProvider(history, Config{Provider: "openai"})
	if len(out) != 1 || len(out[0].Responses) != 1 || out[0].Usage != 0 {
		t.Fatalf("encrypted reasoning and token counts are dropped: %+v", out)
	}
}

func TestLevelText(t *testing.T) {
	history := longHistory(3, 50)
	everything := levelText(history, LevelEverything, 100000)
	noOutputs := levelText(history, LevelNoOutputs, 100000)
	messages := levelText(history, LevelMessages, 100000)
	if !strings.Contains(everything, "RESULT:") || strings.Contains(noOutputs, "RESULT:") || strings.Contains(messages, "TOOL ") {
		t.Fatal("levels keep what they say")
	}
	if !(len(everything) > len(noOutputs) && len(noOutputs) > len(messages)) {
		t.Fatal("each level is lighter than the one before")
	}
	recent := levelText(longHistory(50, 2000), LevelRecent, 4000)
	if len(recent)/4 > 4000*3/10+600 || !strings.HasSuffix(strings.TrimSpace(recent), strings.Repeat("a", 40)) {
		t.Fatalf("recent keeps the latest exchanges within the budget: %d", len(recent)/4)
	}
}

// A summary by the new model (no cache): the result keeps the latest
// exchange whole and the summary, within the new window.
func TestApplyHandoffSummary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Goal: fix main.go. Next: run tests.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	next := Config{Provider: "compatible", ConnectionID: "srv", Model: "m", BaseURL: server.URL}
	learnWindow("srv", "m", 50000)
	history := longHistory(20, 3000)
	plan := PlanHandoff(nil, next, history, nil, "Previous", "New")
	choice := HandoffChoice{Mode: "summary", Summarizer: "next", Level: LevelMessages}
	result, err := (&App{}).ApplyHandoff(context.Background(), nil, next, history, choice, plan, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range result.History {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "Goal: fix main.go") || !strings.Contains(joined, "Conversation handoff from Previous to New") {
		t.Fatalf("the summary leads the new history: %s", joined[:min(400, len(joined))])
	}
	if last := result.History[len(result.History)-1]; last.Role != "assistant" || !strings.HasPrefix(last.Content, "done ") {
		t.Fatalf("the latest exchange stays whole: %+v", last)
	}
	if HistoryEstimate(result.History) >= HistoryEstimate(history)/2 {
		t.Fatal("the handoff reduces the conversation")
	}
	if !strings.Contains(result.Note, "summary by New") {
		t.Fatalf("the note says who summarized: %s", result.Note)
	}
}

// Other models are offered one per account: a catalog of hundreds of
// models stays a short list.
func TestAddSummarizerOnePerAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	plan := HandoffPlan{}
	for i := 0; i < 300; i++ {
		plan.AddSummarizer(fmt.Sprintf("model:router|m%d", i), fmt.Sprintf("m%d", i), Config{Provider: "openrouter", ConnectionID: "router", Model: fmt.Sprintf("m%d", i)}, "")
	}
	for i := 0; i < 20; i++ {
		plan.AddSummarizer(fmt.Sprintf("model:c%d|x", i), "x", Config{Provider: "compatible", ConnectionID: fmt.Sprintf("c%d", i), Model: "x"}, "")
	}
	if len(plan.Summarizers) != otherSummarizers || plan.Summarizers[0].ID != "model:router|m0" {
		t.Fatalf("one per account, %d at most: %d offered", otherSummarizers, len(plan.Summarizers))
	}
}
