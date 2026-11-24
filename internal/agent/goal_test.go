// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseGoalCommand(t *testing.T) {
	for text, want := range map[string]GoalCommand{
		"/goal":                        {Action: "show"},
		"/goal pause":                  {Action: "pause"},
		"/goal Resume":                 {Action: "resume"},
		"/goal clear":                  {Action: "clear"},
		"/goal edit Ship v2":           {Action: "edit", Objective: "Ship v2"},
		"/goal budget 200k":            {Action: "budget", Value: "200k"},
		"/goal tokens off":             {Action: "budget", Value: "off"},
		"/goal turns 12":               {Action: "turns", Value: "12"},
		"/goal time 2h":                {Action: "time", Value: "2h"},
		"/goal Make every test pass":   {Action: "set", Objective: "Make every test pass"},
		"/goal pause the music player": {Action: "set", Objective: "pause the music player"},
	} {
		got, ok := ParseGoalCommand(text)
		if !ok || got != want {
			t.Errorf("%q: %+v %v, want %+v", text, got, ok, want)
		}
	}
	for _, text := range []string{"goal", "/goals", "/subchat x", "hello /goal"} {
		if _, ok := ParseGoalCommand(text); ok {
			t.Errorf("%q read as /goal", text)
		}
	}
}

func TestGoalCommands(t *testing.T) {
	state := &GoalState{}
	if _, err := state.Command(GoalCommand{Action: "pause"}, false, ""); err == nil {
		t.Fatal("paused a goal that does not exist")
	}
	outcome, err := state.Command(GoalCommand{Action: "set", Objective: "Port the parser to Go"}, false, "openai")
	if err != nil || outcome.Start == nil || !outcome.Start.Internal || !strings.HasPrefix(outcome.Start.Content, "[Goal set: Port the parser to Go]") || !strings.Contains(outcome.Start.Content, "<objective>\nPort the parser to Go\n</objective>") {
		t.Fatalf("set: %+v %v", outcome, err)
	}
	if goal := state.Snapshot(); goal.Status != GoalActive || goal.By != "" {
		t.Fatalf("set: %+v", goal)
	}
	if _, err := state.Command(GoalCommand{Action: "budget", Value: "1.5m"}, false, ""); err != nil || state.Snapshot().TokenBudget != 1_500_000 {
		t.Fatalf("budget: %v %+v", err, state.Snapshot())
	}
	if _, err := state.Command(GoalCommand{Action: "time", Value: "1h30m"}, false, ""); err != nil || state.Snapshot().TimeBudget != 5400 {
		t.Fatalf("time: %v %+v", err, state.Snapshot())
	}
	for _, bad := range []GoalCommand{{Action: "budget", Value: "lots"}, {Action: "turns", Value: "-1"}, {Action: "time", Value: "5s"}} {
		if _, err := state.Command(bad, false, ""); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// Paused while a turn runs: that turn ends, nothing follows.
	if outcome, err := state.Command(GoalCommand{Action: "pause"}, true, ""); err != nil || outcome.Start != nil {
		t.Fatalf("pause: %+v %v", outcome, err)
	}
	if _, ok := state.Next(""); ok {
		t.Fatal("a paused goal starts a turn")
	}
	outcome, err = state.Command(GoalCommand{Action: "resume"}, false, "claudecode")
	if err != nil || outcome.Start == nil || !strings.Contains(outcome.Start.Content, "mcp__1loop_todo__update_goal") {
		t.Fatalf("resume: %+v %v", outcome, err)
	}
	if _, err := state.Command(GoalCommand{Action: "edit", Objective: "Port the parser and the lexer"}, false, ""); err != nil || state.Snapshot().Objective != "Port the parser and the lexer" {
		t.Fatalf("edit: %v", err)
	}
	// A new goal keeps the limits the user set.
	state.Command(GoalCommand{Action: "set", Objective: "Something else"}, false, "")
	if goal := state.Snapshot(); goal.TokenBudget != 1_500_000 || goal.TimeBudget != 5400 {
		t.Fatalf("limits lost: %+v", goal)
	}
	if _, err := state.Command(GoalCommand{Action: "clear"}, false, ""); err != nil || state.Snapshot() != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := state.Command(GoalCommand{Action: "set", Objective: strings.Repeat("x", goalObjectiveLimit+1)}, false, ""); err == nil {
		t.Fatal("an objective over the limit was taken")
	}
}

// The goal's turns as Run accounts them: a user turn, then automatic ones
// until a turn makes no progress, a limit is reached or the user stops one.
func TestGoalTurns(t *testing.T) {
	state := &GoalState{}
	start, _ := state.Command(GoalCommand{Action: "set", Objective: "Ship it"}, false, "")
	state.turnBegin([]Message{{Role: "user", Content: "hello"}, *start.Start})
	state.sawTool()
	state.addTokens(1200)
	state.turnEnd(nil, false)
	goal := state.Snapshot()
	if goal.Turns != 1 || goal.TokensUsed != 1200 || goal.Status != GoalActive || goal.Idle || goal.RunningSince != nil {
		t.Fatalf("after the first turn: %+v", goal)
	}
	next, ok := state.Next("")
	if !ok || !strings.HasPrefix(next.Content, "[Goal: turn 2]") {
		t.Fatalf("no second turn: %+v", next)
	}
	// An automatic turn without a tool call stops the loop until the user acts.
	state.turnBegin([]Message{next})
	state.turnEnd(nil, false)
	if goal := state.Snapshot(); !goal.Idle || goal.Status != GoalActive {
		t.Fatalf("an idle turn kept going: %+v", goal)
	}
	if _, ok := state.Next(""); ok {
		t.Fatal("an idle goal starts a turn")
	}
	// The user's message wakes it.
	state.turnBegin([]Message{{Role: "user", Content: "go on"}})
	state.turnEnd(nil, false)
	if _, ok := state.Next(""); !ok {
		t.Fatal("the user's turn did not wake the goal")
	}
	// A stopped turn pauses it.
	state.turnBegin(nil)
	state.turnEnd(context.Canceled, true)
	if goal := state.Snapshot(); goal.Status != GoalPaused {
		t.Fatalf("a stopped turn left the goal %s", goal.Status)
	}
	// A limit reached while the agent works: the message once, then the goal stops.
	state.Command(GoalCommand{Action: "resume"}, true, "")
	state.Command(GoalCommand{Action: "budget", Value: "2k"}, true, "")
	state.turnBegin(nil)
	state.addTokens(900)
	message, ok := state.limitMessage("")
	if !ok || !strings.Contains(message.Content, "Token budget reached") {
		t.Fatalf("no limit message: %+v", message)
	}
	if _, again := state.limitMessage(""); again {
		t.Fatal("the limit message came twice")
	}
	state.turnEnd(nil, false)
	if goal := state.Snapshot(); goal.Status != GoalBudgetLimited {
		t.Fatalf("over its budget, the goal is %s", goal.Status)
	}
	if _, err := state.Command(GoalCommand{Action: "resume"}, false, ""); err == nil {
		t.Fatal("resumed over its budget")
	}
	state.Command(GoalCommand{Action: "budget", Value: "off"}, false, "")
	if _, err := state.Command(GoalCommand{Action: "resume"}, false, ""); err != nil {
		t.Fatalf("not resumed once the limit was removed: %v", err)
	}
	state.Command(GoalCommand{Action: "turns", Value: "4"}, false, "")
	state.turnBegin(nil)
	state.sawTool()
	state.turnEnd(nil, false)
	if goal := state.Snapshot(); goal.Status != GoalBudgetLimited || !strings.Contains(goal.Reason, "Turn limit") {
		t.Fatalf("the turn limit: %+v", goal)
	}
}

// A goal saved while active comes back paused: a restart starts nothing.
func TestGoalSavedActiveComesBackPaused(t *testing.T) {
	state := &GoalState{}
	state.Command(GoalCommand{Action: "set", Objective: "Ship it"}, false, "")
	state.turnBegin(nil)
	time.Sleep(20 * time.Millisecond)
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var loaded GoalState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	goal := loaded.Snapshot()
	if goal.Status != GoalPaused || goal.Seconds <= 0 || goal.RunningSince != nil {
		t.Fatalf("loaded: %+v", goal)
	}
	if data, _ := json.Marshal(&GoalState{}); string(data) != "null" {
		t.Fatalf("no goal saved as %s", data)
	}
}

// create_goal: only when the user asked for a goal, else after their yes.
func TestCreateGoalNeedsTheUsersRequest(t *testing.T) {
	backend := &App{Approvals: map[string]chan ApprovalAnswer{}}
	state := &GoalState{}
	ctx := context.WithValue(context.Background(), goalContextKey{}, state)
	var asked []string
	emit := func(kind string, value any) {
		if kind == "approval" {
			data := value.(map[string]any)
			asked = append(asked, fmt.Sprint(data["title"]))
			go backend.Approve(fmt.Sprint(data["id"]), false)
		}
	}
	state.turnBegin([]Message{{Role: "user", Content: "Fix the flaky test"}})
	if _, err := backend.goalTool(ctx, "create_goal", `{"objective":"Fix every flaky test","token_budget":null}`, emit); err == nil || len(asked) != 1 {
		t.Fatalf("a goal the user did not ask for: %v, asked %v", err, asked)
	}
	if state.Snapshot() != nil {
		t.Fatal("the declined goal was set")
	}
	state.turnBegin([]Message{{Role: "user", Content: "Keep at it until CI is green, set it as a goal"}})
	out, err := backend.goalTool(ctx, "create_goal", `{"objective":"Get CI green","token_budget":50000}`, emit)
	if err != nil || len(asked) != 1 {
		t.Fatalf("asked for: %v, asked %v", err, asked)
	}
	goal := state.Snapshot()
	if goal.By != "agent" || goal.TokenBudget != 50000 || goal.Status != GoalActive || !strings.Contains(out, `"setBy":"agent"`) {
		t.Fatalf("created: %+v %s", goal, out)
	}
	if _, err := backend.goalTool(ctx, "create_goal", `{"objective":"Another","token_budget":null}`, emit); err == nil {
		t.Fatal("replaced an unfinished goal")
	}
	out, err = backend.goalTool(ctx, "update_goal", `{"status":"complete"}`, emit)
	if err != nil || state.Snapshot().Status != GoalComplete || !strings.Contains(out, "Report the final usage") {
		t.Fatalf("complete: %v %s", err, out)
	}
	if _, err := backend.goalTool(ctx, "update_goal", `{"status":"paused"}`, emit); err == nil {
		t.Fatal("paused a complete goal")
	}
}

// Run with a real model loop: the turn, its tool calls and its tokens are
// the goal's, and update_goal ends it.
func TestGoalRun(t *testing.T) {
	cfg, _ := mockModel(t, func(call int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		usage := `{"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":40}}`
		switch call {
		case 1:
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"update_goal","arguments":"{\"status\":\"complete\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		default:
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Done: every requirement is verified."}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: "+usage+"\n\ndata: [DONE]\n\n")
	})
	state := &GoalState{}
	start, _ := state.Command(GoalCommand{Action: "set", Objective: "Ship it"}, false, cfg.Provider)
	cfg.Goal, cfg.Todo, cfg.ApprovalMode = state, &TodoState{}, "yolo"
	var goalEvents int
	err := (&App{}).Run(context.Background(), cfg, []Message{*start.Start}, Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}, func(kind string, value any) {
		if kind == "goal" {
			goalEvents++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	goal := state.Snapshot()
	if goal.Status != GoalComplete || goal.Turns != 1 || goal.TokensUsed != 680 || goal.RunningSince != nil || goal.Seconds <= 0 || goalEvents < 2 {
		t.Fatalf("after the run: %+v, %d goal events", goal, goalEvents)
	}
	if _, ok := state.Next(cfg.Provider); ok {
		t.Fatal("a complete goal starts a turn")
	}
}

func TestGoalTools(t *testing.T) {
	cfg := Config{Provider: "openai"}
	if cfg.toolAvailable("get_goal") {
		t.Fatal("goal tools without a goal state")
	}
	cfg.Goal = &GoalState{}
	for _, name := range []string{"get_goal", "create_goal", "update_goal"} {
		if !cfg.toolAvailable(name) {
			t.Errorf("%s unavailable", name)
		}
	}
	if !strings.Contains(goalInstructions(cfg), "only when the user explicitly asks for a goal") {
		t.Fatal("the prompt does not hold the rule")
	}
	for _, text := range []string{"set it as a goal", "Mets-le en objectif", "/goal later"} {
		if !goalAsked(text) {
			t.Errorf("%q does not ask for a goal", text)
		}
	}
	for _, text := range []string{"fix the goalkeeper sprite", "keep going"} {
		if goalAsked(text) {
			t.Errorf("%q asks for a goal", text)
		}
	}
}
