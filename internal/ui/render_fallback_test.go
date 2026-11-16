// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Each failed way of drawing reaches the next process, first line only and
// cut, and the last message lists them in order with the advice and the log.
func TestRenderAttempts(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv(triedVariable, "")
	noteTried("The graphics driver", errors.New("GLX: no config\ngoroutine 1 [running]"))
	noteTried("Mesa on the CPU", errors.New(strings.Repeat("x", 300)))
	tried := triedSoFar()
	if len(tried) != 2 || tried[0] != (attempt{"The graphics driver", "GLX: no config"}) {
		t.Fatalf("tried = %q", tried)
	}
	if !strings.HasSuffix(tried[1].failure, "…") || len(tried[1].failure) > 250 {
		t.Fatalf("long failure not cut: %d bytes", len(tried[1].failure))
	}
	text := renderFailureText(tried, []string{"Update the driver."})
	for _, want := range []string{"1. The graphics driver: GLX: no config", "2. Mesa on the CPU: x", "• Update the driver.", "renderer.log"} {
		if !strings.Contains(text, want) {
			t.Errorf("message misses %q:\n%s", want, text)
		}
	}
	if log, err := os.ReadFile(rendererLogPath()); err != nil || !strings.Contains(string(log), "The graphics driver failed") {
		t.Errorf("renderer.log: %q, %v", log, err)
	}
	forgetTried()
	if os.Getenv(triedVariable) != "" || len(triedSoFar()) != 0 {
		t.Fatal("the attempts stay for what 1loop starts")
	}
}
