package lang

import "testing"

// LANG=C.UTF-8 reaches us as "C": English, and no parse error.
func TestClosestSupportedLocaleCLocale(t *testing.T) {
	for _, locs := range [][]string{nil, {"C"}, {"POSIX"}, {""}, {"C", "en-US"}} {
		if got := closestSupportedLocale(locs).LanguageString(); got != "en" {
			t.Errorf("closestSupportedLocale(%q) = %q, want en", locs, got)
		}
	}
	if got := closestSupportedLocale([]string{"C", "fr-FR"}).LanguageString(); got != "fr" {
		t.Errorf("closestSupportedLocale(C, fr-FR) = %q, want fr", got)
	}
}
