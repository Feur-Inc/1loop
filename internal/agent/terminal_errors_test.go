// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
)

// A wrong session_id names the chat's terminals (#197).
func TestNoTerminal(t *testing.T) {
	if err := NoTerminal("x", nil); !strings.Contains(err.Error(), "no terminal") {
		t.Fatal(err)
	}
	if err := NoTerminal("x", []string{"a1", "b2"}); !strings.Contains(err.Error(), "session_id x") || !strings.Contains(err.Error(), "a1, b2") {
		t.Fatal(err)
	}
}
