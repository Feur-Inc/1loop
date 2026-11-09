// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWrapUpValidation(t *testing.T) {
	long := func(n int) string { return strings.Repeat("é", n) }
	for _, test := range []struct {
		name, args, problem string
	}{
		{"recap alone", `{"recap":"Done.","ask":null,"actions":null,"blocked":null}`, ""},
		{"ask and two replies", `{"recap":"Done.","ask":"Commit?","actions":[{"label":"Commit","message":"Commit it"},{"label":"Not now","message":null}],"blocked":true}`, ""},
		{"limits are characters", `{"recap":"` + long(300) + `","ask":"` + long(100) + `","actions":[{"label":"` + long(24) + `","message":"` + long(300) + `"}]}`, ""},
		{"no recap", `{"recap":"  ","ask":null,"actions":null,"blocked":null}`, "recap is required"},
		{"long recap", `{"recap":"` + long(301) + `"}`, "recap has 301"},
		{"long ask", `{"recap":"x","ask":"` + long(101) + `","actions":[{"label":"Yes"}]}`, "ask has 101"},
		{"three actions", `{"recap":"x","ask":"Which?","actions":[{"label":"A"},{"label":"B"},{"label":"C"}]}`, "one or two"},
		{"actions without an ask", `{"recap":"x","ask":null,"actions":[{"label":"Yes","message":null}]}`, "give an ask"},
		{"ask without actions", `{"recap":"x","ask":"Push?","actions":[]}`, "needs one or two actions"},
		{"empty label", `{"recap":"x","ask":"Push?","actions":[{"label":" ","message":"push"}]}`, "needs a label"},
		{"long label", `{"recap":"x","ask":"Push?","actions":[{"label":"` + long(25) + `"}]}`, "label has 25"},
		{"long message", `{"recap":"x","ask":"Push?","actions":[{"label":"Push","message":"` + long(301) + `"}]}`, "message has 301"},
	} {
		_, err := ParseWrapUp(test.args)
		switch {
		case test.problem == "" && err != nil:
			t.Errorf("%s: %v", test.name, err)
		case test.problem != "" && (err == nil || !strings.Contains(err.Error(), test.problem)):
			t.Errorf("%s: got %v, want an error with %q", test.name, err, test.problem)
		}
	}
}

func TestWrapUpCardAndStatus(t *testing.T) {
	wrap, _ := ParseWrapUp(`{"recap":"Added the card. Tests pass on the server.","ask":"Open the PR?","actions":[{"label":"Open it","message":null}],"blocked":true}`)
	if !wrap.Card() || wrap.Actions[0].Reply() != "Open it" || !wrap.Blocked {
		t.Fatalf("card %+v", wrap)
	}
	if got := wrap.Status(); got != "Added the card." {
		t.Fatalf("status %q", got)
	}
	plain, _ := ParseWrapUp(`{"recap":"Version 1.2 is out"}`)
	if plain.Card() || plain.Status() != "Version 1.2 is out" {
		t.Fatalf("recap alone %+v, status %q", plain, plain.Status())
	}
}

func TestLastWrapUp(t *testing.T) {
	call := `{"recap":"Done.","ask":"Ship?","actions":[{"label":"Ship"}]}`
	items := []ChatItem{{Kind: "user"}, {Kind: "tool", Tool: "wrap_up", Args: call}, {Kind: "assistant", Text: "Closing."}}
	wrap, index, answered := LastWrapUp(items)
	if index != 1 || answered || wrap.Ask != "Ship?" {
		t.Fatalf("got %+v at %d, answered %v", wrap, index, answered)
	}
	items = append(items, ChatItem{Kind: "user", Text: "Ship"})
	if _, index, answered = LastWrapUp(items); index != 1 || !answered {
		t.Fatalf("after a reply: %d, answered %v", index, answered)
	}
	failed := []ChatItem{{Kind: "tool", Tool: "wrap_up", Args: `{"recap":""}`, Failed: "recap is required"}}
	if _, index, _ = LastWrapUp(failed); index != -1 {
		t.Fatalf("a rejected call drew a card")
	}
}

func TestWrapUpChecksOffTheStep(t *testing.T) {
	todo := &TodoState{}
	if _, err := todo.update(json.RawMessage(`{"add":["one","two"],"updates":[{"id":"+1","status":"in_progress","text":null}],"remove":[],"order":[],"clear":false,"advance":false}`)); err != nil {
		t.Fatal(err)
	}
	events := 0
	result, err := wrapUp(todo, `{"recap":"Did one.","ask":"Do two?","actions":[{"label":"Yes"}]}`, func(event string, _ any) {
		if event == "todo" {
			events++
		}
	})
	if err != nil || !strings.Contains(result, "Do not ask it again") {
		t.Fatalf("result %q, %v", result, err)
	}
	items := todo.Snapshot()
	if items[0].Status != "completed" || items[1].Status != "pending" || events != 1 {
		t.Fatalf("list %+v, %d todo events", items, events)
	}
	if result, _ = wrapUp(todo, `{"recap":"Nothing to ask."}`, func(string, any) { events++ }); !strings.Contains(result, "no card") || events != 1 {
		t.Fatalf("recap alone: %q, %d events", result, events)
	}
}
