// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"
)

// Every stroke of the interface's icons is 10 % heavier, the default one
// and those set by an icon itself.
func TestIconStrokesAreHeavier(t *testing.T) {
	plus := string(iconPlus.Content())
	for _, want := range []string{`stroke-width="1.65"`, `stroke-width="1.98"`} {
		if !strings.Contains(plus, want) {
			t.Fatalf("plus icon %q has no %s", plus, want)
		}
	}
	if got := heavierStrokes(`<circle stroke-width="12"/>`); got != `<circle stroke-width="13.2"/>` {
		t.Fatalf("got %s", got)
	}
}
