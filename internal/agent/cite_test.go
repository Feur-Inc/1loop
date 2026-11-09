// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
)

// Calculations go through code, not web search (#172), for 1loop's own
// models.
func TestCalculationsRunCode(t *testing.T) {
	if !strings.Contains(systemInstructions(Config{Workspace: t.TempDir()}), "not with web search") {
		t.Error("the instructions don't steer calculations away from web search")
	}
}
