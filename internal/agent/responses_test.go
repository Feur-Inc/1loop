package agent

import (
	"strings"
	"testing"
)

// ChatGPT's backend at times ends a streamed answer with an empty list of
// items, after having sent each of them: the tool call must not be lost.
func TestResponsesItemsWithoutFinalList(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"status":"in_progress"}}`, ``,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","name":"update_todo"}}`, ``,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"add\":[\"x\"],"}`, ``,
		`data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"update_todo","arguments":"{\"add\":[\"x\"],\"updates\":[]}"}}`, ``,
		`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":2016}}}`, ``,
	}, "\n")
	out, err := readResponses(strings.NewReader(stream), func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Name != "update_todo" || out.ToolCalls[0].ID != "call_1" {
		t.Fatalf("the tool call was lost: %+v", out)
	}
	if len(out.Responses) != 1 || out.Usage != 2016 {
		t.Fatalf("items or usage not kept: %d items, usage %d", len(out.Responses), out.Usage)
	}
}

// The final list wins when it is there, and an answer with nothing at all
// is an error the user sees, not a silent end of turn.
func TestResponsesFinalListAndEmptyAnswer(t *testing.T) {
	full := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Bonjour"}`, ``,
		`data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message"}}`, ``,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"id":"msg_1","type":"message"},{"id":"rs_1","type":"reasoning"}]}}`, ``,
	}, "\n")
	out, err := readResponses(strings.NewReader(full), func(string, any) {})
	if err != nil || out.Content != "Bonjour" || len(out.Responses) != 2 {
		t.Fatalf("err=%v content=%q items=%d", err, out.Content, len(out.Responses))
	}
	empty := `data: {"type":"response.completed","response":{"status":"completed","output":[]}}` + "\n\n"
	if _, err := readResponses(strings.NewReader(empty), func(string, any) {}); err == nil {
		t.Fatal("an empty answer ended the turn without an error")
	}
}
