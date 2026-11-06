// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package computeruse

import (
	"context"
	"strings"
	"testing"
)

func TestHelperExitSaysWhy(t *testing.T) {
	for _, test := range []struct{ script, want string }{
		{`echo 'Traceback (most recent call last):' >&2; echo "ModuleNotFoundError: No module named 'PIL'" >&2; echo >&2; exit 1`,
			"Computer Use helper exited: ModuleNotFoundError: No module named 'PIL'"},
		{`exit 1`, "Computer Use helper exited"},
	} {
		process, err := startComputerProcess(context.Background(), "sh", "-c", test.script)
		if err != nil {
			t.Fatal(err)
		}
		_, err = process.Request(context.Background(), "initialize", map[string]any{})
		if err == nil || err.Error() != test.want {
			t.Errorf("%s: got %v, want %q", test.script, err, test.want)
		}
		process.Close()
	}
}

func TestStderrTailKeepsTheEnd(t *testing.T) {
	tail := &stderrTail{}
	tail.Write([]byte(strings.Repeat("noise\n", 2000)))
	tail.Write([]byte("last words\n"))
	if len(tail.data) > 4096 {
		t.Errorf("kept %d bytes", len(tail.data))
	}
	if got := tail.lastLine(); got != "last words" {
		t.Errorf("lastLine() = %q", got)
	}
	if got := (&stderrTail{}).lastLine(); got != "" {
		t.Errorf("empty lastLine() = %q", got)
	}
}
