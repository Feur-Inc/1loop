// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"1loop/internal/agent"
)

// A change left out gives back the file's own lines, the others stay.
func TestReviewDropChange(t *testing.T) {
	file := &reviewFile{ReviewFile: agent.ReviewFile{Path: "a", Before: "1\n2\n3\n4\n5\n6\n", After: "1\nTWO\n3\n4\n5\nsix"}}
	file.text = file.After
	rows, at := reviewRows(file)
	if rows[0].added != 2 || rows[0].removed != 2 || len(at) != 2 {
		t.Fatalf("+%d −%d, %d changes", rows[0].added, rows[0].removed, len(at))
	}
	for _, change := range at {
		if change.beforeFrom == 1 {
			file.dropChange(change)
		}
	}
	if file.text != "1\n2\n3\n4\n5\nsix" || !file.edited() {
		t.Errorf("left %q", file.text)
	}
	_, at = reviewRows(file)
	for _, change := range at {
		file.dropChange(change)
	}
	if file.text != file.Before {
		t.Errorf("everything left out: %q", file.text)
	}
}

func TestReviewAnswer(t *testing.T) {
	files := []*reviewFile{{ReviewFile: agent.ReviewFile{Path: "a", After: "a\r\n"}, text: "a\n"}, {ReviewFile: agent.ReviewFile{Path: "b", After: "b"}, text: "mine"}, {ReviewFile: agent.ReviewFile{Path: "c"}, denied: true}}
	answer := reviewAnswer(files, " why ")
	if !answer.Allow || len(answer.Files) != 1 || answer.Files["b"] != "mine" || len(answer.Denied) != 1 || answer.Note != "why" {
		t.Errorf("%+v", answer)
	}
	for _, file := range files {
		file.denied = true
	}
	if answer := reviewAnswer(files, ""); answer.Allow || answer.Denied != nil {
		t.Errorf("all refused: %+v", answer)
	}
}
