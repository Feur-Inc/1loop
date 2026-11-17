// SPDX-License-Identifier: MPL-2.0

package palette

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipOf(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(body))
	}
	w.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const frag = "void main() { gl_FragColor = vec4(1.0); }"

func TestZipThemeLoadsInMemory(t *testing.T) {
	dir := t.TempDir()
	zipOf(t, filepath.Join(dir, "night.zip"), map[string]string{
		"theme.toml":        "name = \"Night\"\nshader = \"a.frag\"\nfont = \"fonts/x.ttf\"\n",
		"a.frag":            frag,
		"fonts/x.ttf":       "FONT",
		"fonts/LICENSE.txt": "licence",
	})
	themes, failed := LoadThemes(dir)
	if len(failed) != 0 || len(themes) != 1 || themes[0].ID != "night" || themes[0].Name != "Night" {
		t.Fatalf("themes %+v failed %+v", themes, failed)
	}
	if themes[0].Packaging() != "zip" {
		t.Fatal(themes[0].Packaging())
	}
	if data, err := ReadThemeFile(themes[0].Shader); err != nil || string(data) != frag {
		t.Fatalf("shader %q %v", data, err)
	}
	if data, err := ReadThemeFile(themes[0].Fonts.Regular); err != nil || string(data) != "FONT" {
		t.Fatalf("font %q %v", data, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatal("the zip was extracted to disk")
	}
}

func TestZipRefusals(t *testing.T) {
	cases := map[string]map[string]string{
		"slip":     {"theme.toml": "name = \"a\"", "../evil.toml": "x"},
		"absolute": {"theme.toml": "name = \"a\"", "/etc/x.txt": "x"},
		"kind":     {"theme.toml": "name = \"a\"", "run.sh": "x"},
		"missing":  {"theme.toml": "shader = \"gone.frag\""},
		"escape":   {"theme.toml": "font = \"../x.ttf\""},
		"notheme":  {"a.txt": "x"},
	}
	want := map[string]string{"slip": "..", "absolute": "absolute", "kind": "not a file a theme may carry", "missing": "not in the theme", "escape": "..", "notheme": "no theme.toml"}
	for name, files := range cases {
		dir := t.TempDir()
		zipOf(t, filepath.Join(dir, name+".zip"), files)
		themes, failed := LoadThemes(dir)
		if len(themes) != 0 || len(failed) != 1 || !strings.Contains(failed[0].Error(), want[name]) {
			t.Errorf("%s: %+v %+v", name, themes, failed)
		}
	}
}

func TestZipSizeAndCountCaps(t *testing.T) {
	dir := t.TempDir()
	big := map[string]string{"theme.toml": "name = \"a\"", "big.txt": strings.Repeat("a", MaxThemeBytes+1)}
	zipOf(t, filepath.Join(dir, "big.zip"), big)
	many := map[string]string{"theme.toml": "name = \"a\""}
	for i := 0; i <= MaxThemeEntries; i++ {
		many[strings.Repeat("a", 1)+string(rune('a'+i%26))+strings.Repeat("b", i)+".txt"] = "x"
	}
	zipOf(t, filepath.Join(dir, "many.zip"), many)
	zipOf(t, filepath.Join(dir, "bad.zip"), nil)
	os.WriteFile(filepath.Join(dir, "broken.zip"), []byte("not a zip"), 0o644)
	_, failed := LoadThemes(dir)
	got := map[string]string{}
	for _, f := range failed {
		got[filepath.Base(f.Path)] = f.Err.Error()
	}
	if !strings.Contains(got["big.zip"], "more than") || !strings.Contains(got["many.zip"], "entries") ||
		!strings.Contains(got["broken.zip"], "not a zip file") || got["bad.zip"] == "" {
		t.Fatalf("%v", got)
	}
}

func TestLooseAndFolderStillLoad(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loose.toml"), []byte("name = \"Loose\"\nshader = \"l.frag\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "l.frag"), []byte(frag), 0o644)
	os.MkdirAll(filepath.Join(dir, "fold"), 0o755)
	os.WriteFile(filepath.Join(dir, "fold", "theme.toml"), []byte("name = \"Fold\"\nshader = \"f.frag\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "fold", "f.frag"), []byte(frag), 0o644)
	themes, failed := LoadThemes(dir)
	if len(failed) != 0 || len(themes) != 2 || themes[0].ID != "fold" || themes[1].ID != "loose" {
		t.Fatalf("%+v %+v", themes, failed)
	}
	if themes[0].Packaging() != "folder" || themes[1].Packaging() != "loose" || themes[1].Shader != filepath.Join(dir, "l.frag") {
		t.Fatalf("%+v", themes)
	}
	if _, err := ReadThemeFile(themes[0].Shader); err != nil {
		t.Fatal(err)
	}
}

func TestPackAndUnpack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "p.toml"), []byte("name = \"P\"\nshader = \"p.frag\"\nfont = \"Maple-Regular.ttf\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "p.frag"), []byte(frag), 0o644)
	os.WriteFile(filepath.Join(dir, "Maple-Regular.ttf"), []byte("F"), 0o644)
	os.WriteFile(filepath.Join(dir, "Maple-LICENSE.txt"), []byte("L"), 0o644)
	if _, err := PackTheme(dir, "p"); err != nil {
		t.Fatal(err)
	}
	themes, failed := LoadThemes(dir)
	if len(failed) != 0 || len(themes) != 1 || themes[0].Packaging() != "zip" {
		t.Fatalf("%+v %+v", themes, failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "p.toml")); err == nil {
		t.Fatal("loose file kept: the name would be taken twice")
	}
	if err := UnpackTheme(themes[0].PackagePath(), filepath.Join(dir, "p-edit")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p-edit", "Maple-LICENSE.txt")); err != nil {
		t.Fatal("the licence did not travel with the font")
	}
	if themes, failed := LoadThemes(dir); len(themes) != 2 || len(failed) != 0 {
		t.Fatalf("%+v %+v", themes, failed)
	}
}

func TestSameNameTwice(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.toml"), []byte("name = \"A\""), 0o644)
	zipOf(t, filepath.Join(dir, "a.zip"), map[string]string{"theme.toml": "name = \"A\""})
	themes, failed := LoadThemes(dir)
	if len(themes) != 1 || len(failed) != 1 {
		t.Fatalf("%+v %+v", themes, failed)
	}
}
