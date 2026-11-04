package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseAgyModels(t *testing.T) {
	output := "Fetching available models...\n" +
		"gemini-4-argon-medium\tGemini 4 Argon (Medium)\n" +
		"gemini-4-argon-high\tGemini 4 Argon (High)\n" +
		"gemini-4-argon-low\tGemini 4 Argon (Low)\n" +
		"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n" +
		"plain-model\tPlain\n"
	got := parseAgyModels(output)
	want := []agyModel{
		{ID: "gemini-4-argon", Name: "Gemini 4 Argon", Efforts: []string{"low", "medium", "high"}},
		{ID: "gpt-oss-120b", Name: "GPT-OSS 120B", Efforts: []string{"medium"}},
		{ID: "plain-model", Name: "Plain"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseAgyModels:\n got %+v\nwant %+v", got, want)
	}
}

func TestAgyRuleAndTool(t *testing.T) {
	input := []byte(`{"CommandLine":"echo hi > c.txt && cat c.txt"}`)
	if rule := agyRule("command", input); rule != "command(echo hi > c.txt && cat c.txt)" {
		t.Fatalf("rule %q", rule)
	}
	if rule := agyRule("command", []byte(`{}`)); rule != "" {
		t.Fatalf("a call without a target must not become a rule: %q", rule)
	}
	tool, args := agyTool("/work", "run_command", input)
	if tool != "shell" || !strings.Contains(args, "cat c.txt") {
		t.Fatalf("run_command: %s %s", tool, args)
	}
	tool, args = agyTool("/work", "write_file", []byte(`{"Path":"/work/src/a.go"}`))
	if tool != "edit_file" || !strings.Contains(args, `"path":"src/a.go"`) {
		t.Fatalf("write_file: %s %s", tool, args)
	}
}

func TestHarnessSession(t *testing.T) {
	claude := Config{Provider: "claudecode", Session: "agy:abc"}
	if HarnessSession(claude) != "" {
		t.Fatal("Claude Code must not resume an Antigravity conversation")
	}
	if HarnessSession(Config{Provider: "antigravity", Session: "agy:abc"}) != "abc" {
		t.Fatal("Antigravity conversation not found")
	}
	if HarnessSession(Config{Provider: "antigravity", Session: "0b1c-claude"}) != "" {
		t.Fatal("Antigravity must not resume a Claude Code session")
	}
}

func TestAgyArgs(t *testing.T) {
	args := agyArgs(Config{Model: "gemini-3.8-flash", Effort: "low"}, "conv", []string{"command(ls)"})
	want := []string{"--model", "gemini-3.8-flash", "--effort", "low", "--conversation", "conv",
		"--settings", `{"permissions":{"allow":["command(ls)"]}}`}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %q", args)
	}
	yolo := agyArgs(Config{ApprovalMode: "yolo"}, "", []string{"command(ls)"})
	if !reflect.DeepEqual(yolo, []string{"--dangerously-skip-permissions"}) {
		t.Fatalf("yolo: %q", yolo)
	}
}

// fakeAgy puts on PATH an agy that answers like agy 1.4.12: a refused
// command the first time, the command's output once --settings allows it.
func fakeAgy(t *testing.T) string {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
read line
echo '{"event":"init","conversation_id":"c1","init":{"model":"m"}}'
echo '{"event":"step_update","step_update":{"step_index":0,"state":"DONE","step_type":"user_input"}}'
echo '{"event":"step_update","step_update":{"step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"ls"}}}}'
case "$*" in
*--settings*)
  echo '{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"tool","tool_info":{"name":"run_command","parameters":{"CommandLine":"ls"},"output":"a.txt"}}}'
  echo '{"event":"step_update","step_update":{"step_index":3,"state":"ACTIVE","step_type":"agent_response","text_delta":"Found a.txt"}}'
  echo '{"event":"step_update","step_update":{"step_index":3,"state":"DONE","step_type":"agent_response","usage":{"input_tokens":10}}}'
  echo '{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"Found a.txt"}}'
  ;;
*)
  echo '{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"tool","tool_info":{"name":"run_command","parameters":{"CommandLine":"ls"}}}}'
  echo '{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"","denied_actions":[{"action":"command","display_name":"RunCommand"}]}}'
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ONELOOP_FLATPAK", "")
	return log
}

func TestRunAntigravityApprovesRefusedCommand(t *testing.T) {
	log := fakeAgy(t)
	backend := &App{Approvals: map[string]chan bool{}}
	var events []string
	var session string
	emit := func(kind string, value any) {
		events = append(events, kind)
		switch kind {
		case "approval":
			go backend.Approve(value.(map[string]any)["id"].(string), true)
		case "session":
			session = value.(string)
		}
	}
	cfg := Config{Provider: "antigravity", Model: "gemini-3.8-flash", Effort: "low", Workspace: t.TempDir()}
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	err := backend.runAntigravity(context.Background(), cfg, []Message{{Role: "user", Content: "list"}}, inbox, emit)
	if err != nil {
		t.Fatal(err)
	}
	if session != "agy:c1" {
		t.Fatalf("session %q", session)
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "--settings") ||
		!strings.Contains(lines[1], "--conversation c1") || !strings.Contains(lines[1], `command(ls)`) {
		t.Fatalf("agy runs:\n%s", calls)
	}
	joined := strings.Join(events, " ")
	for _, want := range []string{"approval", "tool_result", "delta", "message_end"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("no %s event in %s", want, joined)
		}
	}
}

func TestRunAntigravityDeniedCommandStops(t *testing.T) {
	log := fakeAgy(t)
	backend := &App{Approvals: map[string]chan bool{}}
	emit := func(kind string, value any) {
		if kind == "approval" {
			go backend.Approve(value.(map[string]any)["id"].(string), false)
		}
	}
	cfg := Config{Provider: "antigravity", Workspace: t.TempDir()}
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	if err := backend.runAntigravity(context.Background(), cfg, []Message{{Role: "user", Content: "list"}}, inbox, emit); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if n := strings.Count(string(calls), "\n"); n != 1 {
		t.Fatalf("a denied command must not start agy again: %d runs", n)
	}
}

func TestParseAgyUsage(t *testing.T) {
	out := []byte(`{"conversation_id":"","status":"SUCCESS","num_turns":0,"command":{"name":"usage","data":{"groups":[
		{"name":"Gemini Models","buckets":[{"id":"gemini-weekly","window":"weekly","remaining_fraction":0.75,"reset_time":"2026-11-09T15:53:11Z"},{"id":"gemini-5h","window":"5h","remaining_fraction":1,"reset_time":"2026-11-03T13:21:55Z"}]},
		{"name":"Claude and GPT models","buckets":[{"id":"3p-5h","window":"5h","remaining_fraction":0.25,"reset_time":"2026-11-03T14:15:57Z"}]},
		{"name":"Gemini 4 Argon","buckets":[{"id":"argon","window":"weekly"}]}]}}}`)
	limits, err := parseAgyUsage(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, window := range limits.Windows {
		got = append(got, fmt.Sprintf("%s %.0f%% %s", window.Label, window.Remaining(), window.ResetAt.Format(time.RFC3339)))
	}
	want := []string{
		"Gemini · 5h 100% 2026-11-03T13:21:55Z",
		"Gemini · 7d 75% 2026-11-09T15:53:11Z",
		"Claude/GPT · 5h 25% 2026-11-03T14:15:57Z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("windows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := parseAgyUsage([]byte(`{"command":{"name":"help"}}`)); err == nil {
		t.Fatal("an answer other than /usage was accepted")
	}
}

// An exhausted model group makes only its own models unavailable as handoff
// summarizers, not the rest of the account (Codex review on #144).
func TestAgyLimitsPerModelGroup(t *testing.T) {
	reset := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	out := []byte(`{"command":{"name":"usage","data":{"groups":[
		{"name":"Gemini Models","buckets":[{"window":"5h","remaining_fraction":0,"reset_time":"` + reset + `"}]},
		{"name":"Claude and GPT models","buckets":[{"window":"5h","remaining_fraction":1,"reset_time":"` + reset + `"}]},
		{"name":"Gemini 4 Argon","buckets":[{"window":"weekly","remaining_fraction":0.5,"reset_time":"` + reset + `"}]}]}}}`)
	limits, err := parseAgyUsage(out)
	if err != nil {
		t.Fatal(err)
	}
	for model, unavailable := range map[string]bool{
		"gemini-3.8-flash":  true,  // its group (Gemini) is exhausted
		"claude-sonnet-5-5": false, // Claude/GPT still has quota
		"gpt-6.1":           false,
		"gemini-4-argon":    false, // a named group of its own, not the family's
		"some-other-model":  true,  // no group names it: every one counts
	} {
		if got := HandoffAvailability("", limits, model) != ""; got != unavailable {
			t.Errorf("%s: unavailable = %v, want %v", model, got, unavailable)
		}
	}
	// A limit without group (other providers) still applies to every model.
	account := AccountLimits{Windows: []AccountLimitWindow{{Label: "5h", used: 100}}}
	if HandoffAvailability("", account, "claude-sonnet-5-5") == "" {
		t.Error("an account-wide exhausted limit was ignored")
	}
}
