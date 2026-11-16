// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"slices"
	"testing"
)

// Deleting a chat takes its subchats at every level (#448).
func TestSubchatSubtree(t *testing.T) {
	parents := map[string]string{"root": "", "a": "root", "b": "root", "a1": "a", "a1x": "a1", "other": "", "o1": "other",
		"loop1": "loop2", "loop2": "loop1", "self": "self"}
	cases := map[string][]string{
		"root":  {"a", "b", "a1", "a1x"},
		"a1":    {"a1x"},
		"a1x":   nil,
		"other": {"o1"},
		"loop1": {"loop2"},
		"self":  nil,
	}
	for root, want := range cases {
		if got := SubchatSubtree(root, parents); !slices.Equal(got, want) {
			t.Errorf("under %s: %v, want %v", root, got, want)
		}
	}
	if SubtreeNote(0) != "" || SubtreeNote(1) != " Its subchat will be deleted with it." || SubtreeNote(3) != " Its 3 subchats, at every level, will be deleted with it." {
		t.Errorf("notes: %q %q %q", SubtreeNote(0), SubtreeNote(1), SubtreeNote(3))
	}
}
