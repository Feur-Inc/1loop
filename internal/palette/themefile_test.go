// SPDX-License-Identifier: MPL-2.0

package palette

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTheme(t *testing.T) {
	theme, err := ParseTheme("nord", `
name = "Nord"
base = "dark"
accents = ["#88c0d0"]
roundness = 0.5
[colors]
background = "#2e3440"
text = "#eceff4"
hover = "#ffffff10"
`)
	if err != nil {
		t.Fatal(err)
	}
	if theme.Name != "Nord" || theme.Setting() != "file:nord" || theme.Roundness != 0.5 || len(theme.Accents) != 1 {
		t.Fatalf("read %+v", theme)
	}
	if Hex(theme.Tokens.Background) != "#2e3440" || theme.Tokens.Hover.A != 0x10 {
		t.Fatalf("colours %+v", theme.Tokens)
	}
	// Left out, the surfaces follow the background: no near-black card on blue-grey.
	if theme.Tokens.Surface == Dark.Surface || Luminance(theme.Tokens.Surface) <= Luminance(theme.Tokens.Background) {
		t.Fatalf("surface %s not derived from %s", Hex(theme.Tokens.Surface), Hex(theme.Tokens.Background))
	}
}

func TestParseThemeErrors(t *testing.T) {
	for text, want := range map[string]string{
		"base = \"blue\"":                   "neither dark nor light",
		"[colors]\nbackgrond = \"#000000\"": "colors.backgrond",
		"[colors]\ntext = \"red\"":          "not #rrggbb",
		"roundness = 9":                     "outside",
		"accents = [\"#12\"]":               "accents[0]",
		"name = \"x\"\nwat = 1":             "unknown key",
		"name = ":                           "line 1",
	} {
		if _, err := ParseTheme("t", text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", text, err, want)
		}
	}
}

func TestThemeTemplateLoads(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mine.toml"), []byte(ThemeTemplate("Mine")), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.toml"), []byte("base = 3"), 0o644)
	themes, failed := LoadThemes(dir)
	if len(themes) != 1 || themes[0].Name != "Mine" || len(failed) != 1 {
		t.Fatalf("themes %v, failed %v", themes, failed)
	}
}

func TestExampleThemesLoad(t *testing.T) {
	themes, failed := LoadThemes("../../docs/themes")
	if len(failed) > 0 || len(themes) == 0 {
		t.Fatalf("themes %d, failed %v", len(themes), failed)
	}
}

func TestThemeFonts(t *testing.T) {
	theme, err := ParseTheme("f", "font = \"Fira Code\"\nfont_mono = \"mono.ttf\"\n")
	if err != nil || theme.Fonts.Regular != "Fira Code" || theme.Fonts.Mono != "mono.ttf" {
		t.Fatalf("%v %+v", err, theme.Fonts)
	}
	if IsFontFile("Fira Code") || !IsFontFile("mono.ttf") || !IsFontFile("/usr/x/a") {
		t.Fatal("IsFontFile")
	}
}

func TestThemeBackgroundImage(t *testing.T) {
	if theme, err := ParseTheme("i", `background_image = "wall.gif"`); err != nil || theme.Image != "wall.gif" {
		t.Fatalf("%v %+v", err, theme)
	}
	for _, text := range []string{"background_image = \"a.bmp\"", "shader = \"a.frag\"\nbackground_image = \"a.png\""} {
		if _, err := ParseTheme("i", text); err == nil {
			t.Errorf("%q accepted", text)
		}
	}
}
