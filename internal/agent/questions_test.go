// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// claudeAsk runs claudeAskUser and answers its questions as the user would:
// answers by question id, or nil to close them.
func claudeAsk(t *testing.T, input string, answers map[string]string) (map[string]any, QuestionRequest) {
	t.Helper()
	backend := &App{}
	asked := make(chan QuestionRequest, 1)
	done := make(chan map[string]any, 1)
	go func() {
		done <- backend.claudeAskUser(context.Background(), json.RawMessage(input), func(event string, value any) {
			if event == "question" {
				asked <- value.(QuestionRequest)
			}
		})
	}()
	request := <-asked
	if !backend.Answer(request.ID, answers) {
		t.Fatal("the questions were not waiting for an answer")
	}
	return <-done, request
}

func TestClaudeAskUserQuestion(t *testing.T) {
	input := `{"questions":[
		{"question":"Which coffee?","header":"Coffee","multiSelect":false,"options":[{"label":"Espresso","description":"Short"},{"label":"Cold brew","description":"Cold"}]},
		{"question":"Which cup?","header":"Cup","multiSelect":false,"options":[{"label":"Mug","description":"Big"}]}]}`
	response, request := claudeAsk(t, input, map[string]string{"q1": "Espresso", "q2": OtherAnswer + "my own thermos"})
	if len(request.Questions) != 2 || request.Questions[0].Header != "Coffee" || len(request.Questions[0].Options) != 2 {
		t.Fatalf("questions shown: %+v", request.Questions)
	}
	if response["behavior"] != "allow" {
		t.Fatalf("response: %v", response)
	}
	updated := response["updatedInput"].(map[string]any)
	want := map[string]string{"Which coffee?": "Espresso", "Which cup?": "my own thermos"}
	if !reflect.DeepEqual(updated["answers"], want) {
		t.Fatalf("answers: %v, want %v", updated["answers"], want)
	}
	if questions, _ := updated["questions"].([]any); len(questions) != 2 { // Claude Code's own input stays
		t.Fatalf("updated input lost the questions: %v", updated)
	}

	multi := `{"questions":[{"question":"Which toppings?","header":"Toppings","multiSelect":true,"options":[{"label":"Milk","description":""},{"label":"Sugar","description":""}]}]}`
	response, request = claudeAsk(t, multi, map[string]string{"q1": "Milk, Sugar"})
	if !request.Questions[0].MultiSelect {
		t.Fatalf("multi-select lost: %+v", request.Questions[0])
	}
	if got := response["updatedInput"].(map[string]any)["answers"]; !reflect.DeepEqual(got, map[string]string{"Which toppings?": "Milk, Sugar"}) {
		t.Fatalf("multi-select answers: %v", got)
	}

	closed, _ := claudeAsk(t, input, nil)
	if closed["behavior"] != "deny" || closed["message"] == "" {
		t.Fatalf("closed questions: %v", closed)
	}
}
