// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package computeruse

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalHelperReport(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct{ script, want string }{
		{"exit 0", "ready: start asks the user; "},
		{"printf 'PIL (Pillow)\\npipewiresrc (GStreamer'\\''s PipeWire plugin)\\n'", " lacks PIL (Pillow), pipewiresrc (GStreamer's PipeWire plugin). Install what's missing"},
		{"echo 'Traceback' >&2; echo 'SyntaxError: bad' >&2; exit 1", " doesn't run: SyntaxError: bad"},
	} {
		python := filepath.Join(dir, "python3")
		if err := os.WriteFile(python, []byte("#!/bin/sh\n"+test.script+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ONELOOP_COMPUTER_PYTHON", python)
		if got := PortalHelperReport(context.Background()); !strings.Contains(got, test.want) {
			t.Errorf("%s: got %q, want %q", test.script, got, test.want)
		}
	}
	t.Setenv("ONELOOP_COMPUTER_PYTHON", filepath.Join(dir, "absent"))
	if got := PortalHelperReport(context.Background()); !strings.HasPrefix(got, "unavailable, start would fail: ") {
		t.Errorf("absent Python: got %q", got)
	}
}

// The check itself must run on a real Python, whatever it lacks.
func TestPortalCheckRuns(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if output, err := exec.Command(python, "-c", portalCheck).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}
