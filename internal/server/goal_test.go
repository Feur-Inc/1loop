// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"1loop/internal/agent"
)

// /goal on the server (#444): a new chat on the goal, its turns one after
// the other until the agent marks it complete, and the meta clients draw.
func TestGoalOnTheServer(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		call := calls.Add(1)
		var request struct {
			Messages []struct {
				Role, Content string
				ToolCalls     []any `json:"tool_calls"`
			}
		}
		json.Unmarshal(body, &request)
		// The turn's goal message, and whether a tool was called since.
		goalText, called := "", false
		for i := len(request.Messages) - 1; i >= 0; i-- {
			m := request.Messages[i]
			if m.Role == "user" && strings.HasPrefix(m.Content, "[Goal") {
				goalText = m.Content
				break
			}
			called = called || len(m.ToolCalls) > 0
		}
		switch {
		case called: // after its tool: the turn's answer
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Step done."}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		case strings.HasPrefix(goalText, "[Goal: turn 2]"): // the automatic turn finishes it
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c2","type":"function","function":{"name":"update_goal","arguments":"{\"status\":\"complete\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		default: // the first turn makes progress with a tool
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"get_goal","arguments":"{}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		}
		fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":%d}}\n\ndata: [DONE]\n\n", call)
	}))
	defer model.Close()
	server, root, call := testServer(t)
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: model.URL + "/v1", Workspace: root}
	server.approvalMode = "yolo"

	body, _ := json.Marshal(map[string]any{"text": "/goal Port the parser"})
	if code, data := call("POST", "/api/send", bytes.NewReader(body)); code != 200 {
		t.Fatalf("send: %d %s", code, data)
	}
	server.mu.Lock()
	chat := server.chats[0]
	server.mu.Unlock()
	waitIdle(t, server, chat)
	// The first turn ended: the second started by itself, and ended the goal.
	waitIdle(t, server, chat)

	server.mu.Lock()
	goal := chat.Goal.Snapshot()
	meta := server.metaLocked(chat)
	var notes []string
	for _, item := range chat.Items {
		if item.Kind == "note" {
			notes = append(notes, item.Text)
		}
	}
	title := chat.Title
	server.mu.Unlock()
	if goal == nil || goal.Status != agent.GoalComplete || goal.Turns != 2 || goal.TokensUsed == 0 {
		t.Fatalf("goal = %+v", goal)
	}
	if title != "Port the parser" {
		t.Errorf("title %q", title)
	}
	if len(notes) < 2 || notes[0] != "Goal set: Port the parser" || notes[1] != "Goal: turn 2" {
		t.Errorf("notes %q", notes)
	}
	if meta.Goal == nil || meta.Goal.Status != agent.GoalComplete || meta.Goal.Label != "complete" || meta.Goal.Running != 0 {
		t.Errorf("meta goal %+v", meta.Goal)
	}

	// Commands on the chat: a note each, and errors as they are.
	for text, want := range map[string]int{"/goal pause": 400, "/goal": 200, "/goal clear": 200, "/goal resume": 400} {
		raw, _ := json.Marshal(map[string]any{"chat": chat.ID, "text": text})
		if code, data := call("POST", "/api/send", bytes.NewReader(raw)); code != want {
			t.Errorf("%s: %d %s", text, code, data)
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if chat.Goal.Snapshot() != nil {
		t.Error("/goal clear left the goal")
	}
	if last := chat.Items[len(chat.Items)-1]; last.Kind != "note" || last.Text != "Goal cleared" {
		t.Errorf("last item %+v", last)
	}
	if !strings.HasPrefix(chat.Items[len(chat.Items)-2].Text, "Goal complete: Port the parser · ") {
		t.Errorf("/goal showed %q", chat.Items[len(chat.Items)-2].Text)
	}
}
