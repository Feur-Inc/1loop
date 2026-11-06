// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"testing"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp/kb"
)

// Unknown key names are errors, not letters typed into the page (#193), and
// Cmd is Meta (#210).
func TestBrowserKeyChord(t *testing.T) {
	key, mods, err := browserKeyChord("Cmd+A")
	if err != nil || key != "a" || len(mods) != 1 || mods[0] != input.ModifierMeta {
		t.Fatalf("Cmd+A: %q %v %v", key, mods, err)
	}
	if key, _, err := browserKeyChord("Insert"); err != nil || key != kb.Insert {
		t.Fatalf("Insert: %q %v", key, err)
	}
	if key, mods, err := browserKeyChord("Ctrl++"); err != nil || key != "+" || len(mods) != 1 {
		t.Fatalf("Ctrl++: %q %v %v", key, mods, err)
	}
	for _, text := range []string{"Escap", "Hyper+A"} {
		if _, _, err := browserKeyChord(text); err == nil {
			t.Fatalf("%s accepted", text)
		}
	}
}
