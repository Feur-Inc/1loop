// SPDX-License-Identifier: MPL-2.0

package keys

import "testing"

func TestParse(t *testing.T) {
	for text, want := range map[string]Chord{
		"Enter":          {Key: "Enter"},
		"return":         {Key: "Enter"},
		"esc":            {Key: "Escape"},
		"a":              {Key: "a"},
		"Ctrl+A":         {Ctrl: true, Key: "A"},
		"Control+a":      {Ctrl: true, Key: "a"},
		"Cmd+A":          {Meta: true, Key: "A"},
		"Super+Shift+t":  {Meta: true, Shift: true, Key: "t"},
		"Option+Left":    {Alt: true, Key: "ArrowLeft"},
		"Ctrl++":         {Ctrl: true, Key: "+"},
		"+":              {Key: "+"},
		"Plus":           {Key: "+"},
		"Ctrl+[":         {Ctrl: true, Key: "["},
		"Insert":         {Key: "Insert"},
		"f5":             {Key: "F5"},
		" Shift + Tab  ": {Shift: true, Key: "Tab"},
	} {
		got, err := Parse(text)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", text, got, err, want)
		}
	}
	// Unknown names are errors, never text to type (#193).
	for _, text := range []string{"Escap", "Hyper+A", "Ctrl+", "", "F13", "Ctrl+Escap"} {
		if got, err := Parse(text); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", text, got)
		}
	}
}
