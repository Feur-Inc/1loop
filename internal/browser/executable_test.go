// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"path/filepath"
	"testing"
)

// ONELOOP_BROWSER=none wins over every browser, even ONELOOP_CHROMIUM, and
// never downloads one.
func TestFindBrowserDisabled(t *testing.T) {
	BrowserProfileOverride = filepath.Join(t.TempDir(), "browser") // no 1loop Chromium installed
	t.Cleanup(func() { BrowserProfileOverride = "" })
	t.Setenv("ONELOOP_CHROMIUM", "/usr/bin/chromium")
	for _, value := range []string{"none", " None "} {
		t.Setenv("ONELOOP_BROWSER", value)
		if _, err := findBrowser(context.Background()); err == nil || err.Error() != NoBrowserProblem {
			t.Fatalf("%q: %v", value, err)
		}
	}
	t.Setenv("ONELOOP_BROWSER", "")
	if binary, err := findBrowser(context.Background()); err != nil || binary.path != "/usr/bin/chromium" {
		t.Fatalf("unset: %+v %v", binary, err)
	}
}
