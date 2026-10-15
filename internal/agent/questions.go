package agent

// request_user_input: the model asks the user instead of guessing. Each
// question offers up to 10 options; the user may always answer in their own
// words ("Other"). The run waits for the answers, from the desktop or the
// phone.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"1loop/internal/core"
)

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type Question struct {
	ID       string           `json:"id"`
	Header   string           `json:"header"`
	Question string           `json:"question"`
	Options  []QuestionOption `json:"options"`
}

// QuestionRequest is the "question" event: the run waits for Answer(ID, …).
type QuestionRequest struct {
	ID        string     `json:"id"`
	Questions []Question `json:"questions"`
}

// OtherAnswer prefixes an answer the user wrote rather than picked.
const OtherAnswer = "Other: "

func questionToolDefinition() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "request_user_input",
		"description": "Ask the user when a choice is theirs to make (what to include, which approach, a missing detail) instead of guessing or widening the request. Ask as many questions as needed, together. Each offers at most 10 options, short and distinct; keep them few (2 to 4 is usual) and put the one you recommend first, with \"(Recommended)\" at the end of its label. The user can always answer in their own words instead; that answer starts with \"" + OtherAnswer + "\". id is a short snake_case key; header a label of 12 characters at most.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"questions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"id":       str,
				"header":   str,
				"question": str,
				"options": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
					"label":       str,
					"description": str,
				}, "required": []string{"label", "description"}, "additionalProperties": false}},
			}, "required": []string{"id", "header", "question", "options"}, "additionalProperties": false}},
		}, "required": []string{"questions"}, "additionalProperties": false},
	}}
}

// askUser shows the questions and waits for the answers.
func (backend *App) askUser(ctx context.Context, arguments string, begin func(), emit emitter) (string, error) {
	var args struct{ Questions []Question }
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	if len(args.Questions) == 0 {
		return "", fmt.Errorf("Ask at least one question")
	}
	for i, question := range args.Questions {
		if strings.TrimSpace(question.Question) == "" {
			return "", fmt.Errorf("Question %d is empty", i+1)
		}
		if len(question.Options) > 10 {
			return "", fmt.Errorf("%q offers %d options: give at most 10", question.Question, len(question.Options))
		}
		if question.ID == "" {
			args.Questions[i].ID = fmt.Sprintf("q%d", i+1)
		}
	}
	id := core.Identifier()
	ch := make(chan map[string]string, 1)
	backend.mu.Lock()
	if backend.answers == nil {
		backend.answers = map[string]chan map[string]string{}
	}
	backend.answers[id] = ch
	backend.mu.Unlock()
	defer func() { backend.mu.Lock(); delete(backend.answers, id); backend.mu.Unlock() }()
	begin()
	emit("question", QuestionRequest{ID: id, Questions: args.Questions})
	select {
	case answers := <-ch:
		if answers == nil {
			return "", fmt.Errorf("The user closed the questions without answering")
		}
		data, _ := json.Marshal(map[string]any{"answers": answers})
		return string(data), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Answer gives the run the user's answers, by question id; nil when the user
// closed the questions.
func (backend *App) Answer(id string, answers map[string]string) bool {
	backend.mu.Lock()
	ch := backend.answers[id]
	backend.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- answers:
	default:
	}
	return true
}
