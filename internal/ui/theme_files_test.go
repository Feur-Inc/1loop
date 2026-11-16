// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os"
	"path/filepath"
	"testing"

	"1loop/internal/palette"
)

func TestThemeFileChoice(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sea.toml"), []byte("accents = [\"#2288cc\", \"#cc8822\"]\nroundness = 0.5\n[colors]\nbackground = \"#102030\"\n"), 0o644)
	saved := themesDir
	themesDir = dir
	defer func() { themesDir = saved; loadThemeFiles() }()
	loadThemeFiles()

	choice := themeChoice{theme: "file:sea"}
	if palette.Hex(choice.accentColour()) != "#2288cc" {
		t.Fatalf("default accent %s, want the theme's first", palette.Hex(choice.accentColour()))
	}
	choice.accent = "#cc8822"
	if palette.Hex(choice.accentColour()) != "#cc8822" {
		t.Fatalf("chosen accent %s", palette.Hex(choice.accentColour()))
	}
	if choice.themeSetting() != "file:sea" {
		t.Fatalf("setting %s", choice.themeSetting())
	}
	// A project's theme wins over the global one while it is on screen.
	plain := themeChoice{theme: palette.ThemeDark, project: "file:sea"}
	if len(plain.fileAccents()) != 2 {
		t.Fatal("project theme not active")
	}
	if len(themeChoices(builtInThemes())) != 4 {
		t.Fatalf("choices %v", themeChoices(builtInThemes()))
	}
}
