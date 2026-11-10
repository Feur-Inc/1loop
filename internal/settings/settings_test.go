// SPDX-License-Identifier: MPL-2.0

package settings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type searchCase struct {
	Query    string   `json:"query"`
	First    string   `json:"first"`
	Also     []string `json:"also"`
	None     bool     `json:"none"`
	Platform Where    `json:"platform"`
	Why      string   `json:"why"`
}

// The cases are shared with the phone's search test.
func TestSearch(t *testing.T) {
	data, err := os.ReadFile("testdata/search_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []searchCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		platform := c.Platform
		if platform == "" {
			platform = OnDesktop
		}
		if platform == OnDesktop && c.First != "" {
			if setting, _ := Lookup(c.First); !setting.On(OnDesktop) {
				platform = OnPhone // a case for a setting the desktop does not show
			}
		}
		matches := Search(On(platform), c.Query)
		keys := []string{}
		for _, match := range matches {
			keys = append(keys, match.Setting.Key)
		}
		if c.None {
			if len(matches) > 0 {
				t.Errorf("%q (%s): want nothing, got %v", c.Query, c.Why, keys)
			}
			continue
		}
		if len(matches) == 0 || keys[0] != c.First {
			t.Errorf("%q (%s): want %s first, got %v", c.Query, c.Why, c.First, keys)
		}
		for _, key := range c.Also {
			found := false
			for _, got := range keys {
				found = found || got == key
			}
			if !found {
				t.Errorf("%q (%s): want %s among %v", c.Query, c.Why, key, keys)
			}
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"abc", "abc", 0}, {"permisions", "permissions", 1}, {"permissoins", "permissions", 1}, {"kitten", "sitting", 3}, {"ab", "", 2}} {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestList(t *testing.T) {
	keys := map[string]bool{}
	for _, setting := range All() {
		if keys[setting.Key] {
			t.Errorf("%s is listed twice", setting.Key)
		}
		keys[setting.Key] = true
		if SectionTitle(setting.Section) == "" {
			t.Errorf("%s: unknown section %q", setting.Key, setting.Section)
		}
		if setting.Title == "" || setting.Description == "" {
			t.Errorf("%s: a title and a description are needed", setting.Key)
		}
		if setting.Where != OnBoth && setting.Where != OnDesktop && setting.Where != OnPhone {
			t.Errorf("%s: shown nowhere", setting.Key)
		}
		if (setting.Kind == Choice) != (len(setting.Values) > 0) {
			t.Errorf("%s: a choice, and only a choice, has values", setting.Key)
		}
	}
	// The security settings stay the user's.
	for _, key := range []string{KeyApprovalMode, "models.connections", "phone.remote", "phone.pairing", "general.remote_server", "general.workspace", "tools.github", "phone.sharing", "phone.adb"} {
		if setting, ok := Lookup(key); !ok || !setting.Protected {
			t.Errorf("%s must be protected", key)
		}
	}
}

// phoneCopy is the list as the phone's build bundles it.
const phoneCopy = "../../android/app/src/main/assets/settings.json"

// TestPhoneCopy keeps the phone's copy of the list in step; run with
// UPDATE_SETTINGS=1 to write it.
func TestPhoneCopy(t *testing.T) {
	want, err := json.MarshalIndent(struct {
		Sections []Section `json:"sections"`
		Settings []Setting `json:"settings"`
	}{Sections(), All()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("UPDATE_SETTINGS") != "" {
		if err := os.WriteFile(filepath.FromSlash(phoneCopy), want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.FromSlash(phoneCopy))
	if err != nil {
		t.Fatalf("%v: run the test with UPDATE_SETTINGS=1", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date: run go test ./internal/settings with UPDATE_SETTINGS=1", phoneCopy)
	}
}

func TestShortcuts(t *testing.T) {
	used := map[string]string{}
	for _, setting := range Shortcuts() {
		value, err := ShortcutValue(setting.Default)
		if err != nil || value != setting.Default {
			t.Errorf("%s: default %q reads as %q, %v", setting.Key, setting.Default, value, err)
		}
		if other, taken := used[value]; taken {
			t.Errorf("%s and %s both default to %s", setting.Key, other, value)
		}
		used[value] = setting.Key
		if found, _ := Lookup(setting.Key); found.Kind != Shortcut {
			t.Errorf("%s is not listed", setting.Key)
		}
	}
	for text, want := range map[string]string{"ctrl+shift+f": "Ctrl+Shift+F", "Alt+up": "Alt+ArrowUp", "cmd+k": "Meta+K", "Off": ShortcutOff, "": ShortcutOff, "Ctrl+,": "Ctrl+,"} {
		if got, err := ShortcutValue(text); err != nil || got != want {
			t.Errorf("ShortcutValue(%q) = %q, %v; want %q", text, got, err, want)
		}
	}
	for _, text := range []string{"F", "Shift+F", "Ctrl+C", "ctrl+v", "Ctrl+Enter", "Ctrl+Bogus", "Ctrl++"} {
		if got, err := ShortcutValue(text); err == nil {
			t.Errorf("ShortcutValue(%q) = %q, want an error", text, got)
		}
	}
}
