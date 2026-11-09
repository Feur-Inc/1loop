// SPDX-License-Identifier: MPL-2.0

package agent

// wrap_up: the model ends a turn that did work with a short recap and, when
// something waits on the user, one question with one or two replies (#215).
// The call is saved with the chat like any tool row, so the next-step card
// (internal/ui/next_step.go, the phone's NextStepCard) is drawn from it.

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type WrapUp struct {
	Recap   string         `json:"recap"`
	Ask     string         `json:"ask,omitempty"`
	Actions []WrapUpAction `json:"actions,omitempty"`
	Blocked bool           `json:"blocked,omitempty"`
}

// WrapUpAction is a reply button: a click sends Message, or Label when it
// has none, as the user's next message.
type WrapUpAction struct {
	Label   string `json:"label"`
	Message string `json:"message,omitempty"`
}

func (action WrapUpAction) Reply() string {
	if strings.TrimSpace(action.Message) != "" {
		return action.Message
	}
	return action.Label
}

// Card: only a question is drawn; a recap alone is the sidebar's status.
func (wrap WrapUp) Card() bool { return wrap.Ask != "" && len(wrap.Actions) > 0 }

// Status is the recap's first sentence, what the sidebar shows.
func (wrap WrapUp) Status() string {
	recap := strings.TrimSpace(wrap.Recap)
	for i, r := range recap {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(recap) || recap[i+1] == ' ' || recap[i+1] == '\n') {
			return recap[:i+1]
		}
	}
	return FirstLine(recap)
}

func WrapUpTool(name string) bool {
	return name == "wrap_up" || name == "mcp__1loop_todo__wrap_up"
}

// LastWrapUp is the chat's latest wrap_up and its item, and whether the user
// has written since (the card is answered, the chat no longer blocked).
func LastWrapUp(items []ChatItem) (wrap WrapUp, index int, answered bool) {
	for i := len(items) - 1; i >= 0; i-- {
		switch {
		case items[i].Kind == "user":
			answered = true
		case items[i].Kind == "tool" && WrapUpTool(items[i].Tool) && items[i].Failed == "":
			if parsed, err := ParseWrapUp(items[i].Args); err == nil {
				return parsed, i, answered
			}
		}
	}
	return WrapUp{}, -1, false
}

// WrapUpStatus is what the sidebar shows of a chat: its last recap's first
// sentence, and whether it waits on the user (blocked, not answered yet).
func WrapUpStatus(items []ChatItem) (status string, blocked bool) {
	wrap, index, answered := LastWrapUp(items)
	if index < 0 {
		return "", false
	}
	return wrap.Status(), wrap.Blocked && !answered
}

const (
	wrapUpRecapMax   = 300
	wrapUpAskMax     = 100
	wrapUpLabelMax   = 24
	wrapUpMessageMax = 300
)

// ParseWrapUp reads and checks a wrap_up call's arguments.
func ParseWrapUp(arguments string) (WrapUp, error) {
	var args struct {
		Recap   string  `json:"recap"`
		Ask     *string `json:"ask"`
		Actions []struct {
			Label   string  `json:"label"`
			Message *string `json:"message"`
		} `json:"actions"`
		Blocked *bool `json:"blocked"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return WrapUp{}, err
	}
	wrap := WrapUp{Recap: strings.TrimSpace(args.Recap), Blocked: args.Blocked != nil && *args.Blocked}
	if args.Ask != nil {
		wrap.Ask = strings.TrimSpace(*args.Ask)
	}
	switch {
	case wrap.Recap == "":
		return WrapUp{}, fmt.Errorf("recap is required: two or three sentences on what is now true and where it stands")
	case utf8.RuneCountInString(wrap.Recap) > wrapUpRecapMax:
		return WrapUp{}, fmt.Errorf("recap has %d characters: keep it to %d", utf8.RuneCountInString(wrap.Recap), wrapUpRecapMax)
	case utf8.RuneCountInString(wrap.Ask) > wrapUpAskMax:
		return WrapUp{}, fmt.Errorf("ask has %d characters: keep it to %d, the decision alone", utf8.RuneCountInString(wrap.Ask), wrapUpAskMax)
	case len(args.Actions) > 2:
		return WrapUp{}, fmt.Errorf("actions has %d buttons: give one or two", len(args.Actions))
	case len(args.Actions) > 0 && wrap.Ask == "":
		return WrapUp{}, fmt.Errorf("actions answer the ask: give an ask with them, or leave both out")
	case wrap.Ask != "" && len(args.Actions) == 0:
		return WrapUp{}, fmt.Errorf("an ask needs one or two actions that answer it, your pick first")
	}
	for i, action := range args.Actions {
		label := strings.TrimSpace(action.Label)
		message := ""
		if action.Message != nil {
			message = strings.TrimSpace(*action.Message)
		}
		switch {
		case label == "":
			return WrapUp{}, fmt.Errorf("action %d needs a label", i+1)
		case utf8.RuneCountInString(label) > wrapUpLabelMax:
			return WrapUp{}, fmt.Errorf("action %d's label has %d characters: keep it to %d (1 to 3 words)", i+1, utf8.RuneCountInString(label), wrapUpLabelMax)
		case utf8.RuneCountInString(message) > wrapUpMessageMax:
			return WrapUp{}, fmt.Errorf("action %d's message has %d characters: keep it to %d", i+1, utf8.RuneCountInString(message), wrapUpMessageMax)
		}
		wrap.Actions = append(wrap.Actions, WrapUpAction{Label: label, Message: message})
	}
	return wrap, nil
}

func wrapUpToolDefinition() map[string]any {
	nullableString := map[string]any{"type": []string{"string", "null"}}
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "wrap_up",
		"description": "End a turn that changed files, ran commands or kept a task list: 1loop draws a next-step card under your last message. Call it once, when the turn's work is done and before your closing message, which still follows in full: the recap never replaces it. It also completes the task list's step in progress. recap: two or three short sentences (at most 300 characters), what is now true, then where it stands, including what is not done or not verified; its first sentence is the chat's status in the sidebar. ask, only when the next move is the user's call (an approval, a choice of approach, a go-ahead to commit, push or send): that one decision in one short sentence (at most 100 characters), with no reason, no recommendation and no list of the options. actions: one or two reply buttons that answer the ask, your pick first (drawn as the main button); label is 1 to 3 words (at most 24 characters), message what the click sends as the user's reply (at most 300 characters; null sends the label). With nothing pending, give the recap alone: no card is drawn. blocked: true when your work stops until the user answers.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"recap": map[string]string{"type": "string"},
			"ask":   nullableString,
			"actions": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "object", "properties": map[string]any{
				"label":   map[string]string{"type": "string"},
				"message": nullableString,
			}, "required": []string{"label", "message"}, "additionalProperties": false}},
			"blocked": map[string]any{"type": []string{"boolean", "null"}},
		}, "required": []string{"recap", "ask", "actions", "blocked"}, "additionalProperties": false},
	}}
}

// wrapUp runs the call: checks it, completes the step in progress and says
// whether the card asks the question, so the model does not ask it again.
func wrapUp(todo *TodoState, arguments string, emit emitter) (string, error) {
	wrap, err := ParseWrapUp(arguments)
	if err != nil {
		return "", err
	}
	if todo.checkOff() {
		emit("todo", nil)
	}
	if wrap.Card() {
		return "Shown: the card asks your question with its buttons. Do not ask it again: now write your closing message, your full answer to the user.", nil
	}
	return "Recorded; no card is drawn, nothing being asked. Now write your closing message, your full answer to the user.", nil
}

// checkOff completes the step in progress at the end of a turn (#216).
func (todo *TodoState) checkOff() bool {
	if todo == nil {
		return false
	}
	todo.mu.Lock()
	defer todo.mu.Unlock()
	completed, changed := "completed", false
	for _, item := range todo.items {
		if item.Status == "in_progress" {
			_ = applyTodoChange(todo.items, todoChange{ID: item.ID, Status: &completed})
			changed = true
		}
	}
	return changed
}

func wrapUpInstructions(cfg Config) string {
	if !cfg.toolAvailable("wrap_up") {
		return ""
	}
	tool := "wrap_up"
	if cfg.Provider == "claudecode" {
		tool = "mcp__1loop_todo__wrap_up"
	}
	return "\n\nTurn wrap-up: every turn in which you changed files, ran commands or kept a task list ends with " + tool + ", called once when the turn's work is done and before your closing message; skip it for a quick answer or plain conversation. " +
		"Your closing message comes after the call and is your answer in full: the recap never replaces it. The call also completes the task list's step in progress, so no closing update is needed. " +
		"Give an ask, with one or two buttons that answer it, only when the next move is the user's call; a turn with nothing pending sends the recap alone. Its result says whether the card shows your question: if it does, do not ask it again in the closing message. " +
		"Never ask whether to keep watching, waiting for or polling something."
}
