// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
)

func todoCall(t *testing.T, todo *TodoState, call string) []TodoItem {
	t.Helper()
	items, err := todo.update([]byte(call))
	if err != nil {
		t.Fatalf("%s: %v", call, err)
	}
	return items
}

// The same text is a task of its own, not silently dropped (#196).
func TestTodoSameTextTwice(t *testing.T) {
	todo := &TodoState{}
	todoCall(t, todo, `{"add":["Run tests"],"updates":[],"remove":[],"order":[],"clear":false}`)
	items := todoCall(t, todo, `{"add":["Run tests"],"updates":[],"remove":[],"order":[],"clear":false}`)
	if len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("items: %+v", items)
	}
}

// Completing the last step and starting the next request's list is one call:
// the update applies to the list before it is cleared (#197).
func TestTodoCompleteThenClear(t *testing.T) {
	todo := &TodoState{}
	items := todoCall(t, todo, `{"add":["Old step"],"updates":[],"remove":[],"order":[],"clear":false}`)
	call := `{"add":["New step","Next one"],"updates":[{"id":"` + items[0].ID + `","status":"completed","text":null},{"id":"+1","status":"in_progress","text":null}],"remove":[],"order":[],"clear":true}`
	items = todoCall(t, todo, call)
	if len(items) != 2 || items[0].Text != "New step" || items[0].Status != "in_progress" || items[0].StartedAt == nil || items[1].Status != "pending" {
		t.Fatalf("items: %+v", items)
	}
}

// A task added by a call can be placed by the same call (#198).
func TestTodoOrderAddedTask(t *testing.T) {
	todo := &TodoState{}
	items := todoCall(t, todo, `{"add":["A","B"],"updates":[],"remove":[],"order":[],"clear":false}`)
	call := `{"add":["First"],"updates":[],"remove":[],"order":["+1","` + items[0].ID + `","` + items[1].ID + `"],"clear":false}`
	items = todoCall(t, todo, call)
	if got := items[0].Text + items[1].Text + items[2].Text; got != "FirstAB" {
		t.Fatalf("order: %s", got)
	}
	for _, call := range []string{
		`{"add":[],"updates":[{"id":"+1","status":"in_progress","text":null}],"remove":[],"order":[],"clear":false}`,
		`{"add":["X"],"updates":[],"remove":[],"order":["+1"],"clear":false}`,
	} {
		if _, err := todo.update([]byte(call)); err == nil {
			t.Fatalf("accepted %s", call)
		}
	}
	if len(todo.Snapshot()) != 3 {
		t.Fatal("a refused call changed the list")
	}
}

// Task text stays on one line, without control or direction characters (#198).
func TestTodoTextOneLine(t *testing.T) {
	todo := &TodoState{}
	items := todoCall(t, todo, `{"add":["Fix\nthe\u0000 bug‮ here "],"updates":[],"remove":[],"order":[],"clear":false}`)
	if items[0].Text != "Fix the bug here" {
		t.Fatalf("text: %q", items[0].Text)
	}
	if _, err := todo.update([]byte(`{"add":["‏\n"],"updates":[],"remove":[],"order":[],"clear":false}`)); err == nil {
		t.Fatal("accepted a task with no text")
	}
}

// An unknown ID names the ones the list has.
func TestTodoUnknownID(t *testing.T) {
	todo := &TodoState{}
	items := todoCall(t, todo, `{"add":["A"],"updates":[],"remove":[],"order":[],"clear":false}`)
	_, err := todo.update([]byte(`{"add":[],"updates":[{"id":"nope","status":"completed","text":null}],"remove":[],"order":[],"clear":false}`))
	if err == nil || !strings.Contains(err.Error(), items[0].ID) {
		t.Fatalf("error: %v", err)
	}
}
