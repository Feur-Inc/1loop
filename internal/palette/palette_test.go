// SPDX-License-Identifier: MPL-2.0

package palette

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"1loop/internal/settings"
)

// Text and the status colours read on every surface they are drawn on.
func TestContrast(t *testing.T) {
	for name, tokens := range map[string]Tokens{"dark": Dark, "light": Light} {
		for _, surface := range []string{"Background", "Surface", "Code", "Raised"} {
			bg := reflect.ValueOf(tokens).FieldByName(surface).Interface().(color.NRGBA)
			for ink, least := range map[string]float64{"Text": 7, "Muted": 3.5, "Danger": 3.5, "Added": 3.5} {
				fg := reflect.ValueOf(tokens).FieldByName(ink).Interface().(color.NRGBA)
				if c := Contrast(fg, bg); c < least {
					t.Errorf("%s: %s on %s is %.2f:1, want %.1f", name, ink, surface, c, least)
				}
			}
		}
		for _, line := range []color.NRGBA{tokens.DiffAdded, tokens.DiffRemoved} {
			if c := Contrast(tokens.Text, line); c < 7 {
				t.Errorf("%s: text on a diff line is %.2f:1", name, c)
			}
		}
		for _, accent := range Accents {
			derived := Derive(tokens, accent.Color)
			if c := Contrast(derived.Accent, tokens.Background); c < minContrast {
				t.Errorf("%s: %s on the background is %.2f:1", name, accent.ID, c)
			}
			if c := Contrast(derived.OnAccent, derived.Accent); c < 3 {
				t.Errorf("%s: ink on %s is %.2f:1", name, accent.ID, c)
			}
		}
	}
	// Amber stays the desktop's amber on the dark theme.
	if got := Derive(Dark, AccentColor(DefaultAccent)); got.Accent != rgb(0xffb000) || got.OnAccent != rgb(0x111111) {
		t.Errorf("dark amber = %v", got)
	}
}

// The settings list offers the presets, and custom.
func TestAccentSetting(t *testing.T) {
	setting, _ := settings.Lookup(settings.KeyAccent)
	var ids []string
	for _, value := range setting.Values {
		ids = append(ids, value.ID)
	}
	var want []string
	for _, accent := range Accents {
		want = append(want, accent.ID)
	}
	if !reflect.DeepEqual(ids, append(want, "custom")) {
		t.Errorf("settings offer %v, the palette %v", ids, want)
	}
	for text, want := range map[string]bool{"#ffb000": true, "FFB000": true, " #4c8dff ": true, "#fff": false, "blue": false, "#gg0000": false} {
		if _, ok := ParseHex(text); ok != want {
			t.Errorf("ParseHex(%q) = %v", text, ok)
		}
	}
	if AccentColor("blue") != rgb(0x4c8dff) || AccentColor("#123456") != rgb(0x123456) || AccentColor("nope") != rgb(0xffb000) {
		t.Error("AccentColor reads the setting wrong")
	}
}

const (
	phoneTokens = "../../android/app/src/main/java/io/oneloop/remote/PaletteTokens.kt"
	derivedCase = "testdata/derived.json"
)

func lowerFirst(name string) string {
	r := []rune(name)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func kotlinColor(c color.NRGBA) string {
	return fmt.Sprintf("Color(0x%02X%02X%02X%02X)", c.A, c.R, c.G, c.B)
}

// kotlin is the phone's copy of the tokens and presets.
func kotlin() []byte {
	var out strings.Builder
	out.WriteString("// SPDX-License-Identifier: MPL-2.0\n\n")
	out.WriteString("// Generated from internal/palette by its tests (UPDATE_PALETTE=1 go test ./internal/palette): do not edit.\n\n")
	out.WriteString("package io.oneloop.remote\n\nimport androidx.compose.ui.graphics.Color\n\n")
	out.WriteString("// A theme's colours by what they are for, as on the desktop.\nclass Tokens(\n")
	kind := reflect.TypeOf(Tokens{})
	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		if field.Type.Kind() == reflect.Bool {
			fmt.Fprintf(&out, "    val %s: Boolean,\n", lowerFirst(field.Name))
		} else {
			fmt.Fprintf(&out, "    val %s: Color,\n", lowerFirst(field.Name))
		}
	}
	out.WriteString(")\n\nclass AccentPreset(val id: String, val label: String, val color: Color)\n\nobject PaletteTokens {\n")
	for _, theme := range []struct {
		name   string
		tokens Tokens
	}{{"dark", Dark}, {"light", Light}} {
		fmt.Fprintf(&out, "    val %s = Tokens(\n", theme.name)
		value := reflect.ValueOf(theme.tokens)
		for i := 0; i < kind.NumField(); i++ {
			name := lowerFirst(kind.Field(i).Name)
			if c, ok := value.Field(i).Interface().(color.NRGBA); ok {
				fmt.Fprintf(&out, "        %s = %s,\n", name, kotlinColor(c))
			} else {
				fmt.Fprintf(&out, "        %s = %v,\n", name, value.Field(i).Interface())
			}
		}
		out.WriteString("    )\n")
	}
	out.WriteString("    val accents = listOf(\n")
	for _, accent := range Accents {
		fmt.Fprintf(&out, "        AccentPreset(%q, %q, %s),\n", accent.ID, accent.Label, kotlinColor(accent.Color))
	}
	fmt.Fprintf(&out, "    )\n    const val defaultAccent = %q\n}\n", DefaultAccent)
	return []byte(out.String())
}

// derived are what Derive gives, for the phone's test of its own Derive.
func derived() []byte {
	var cases []map[string]string
	hexA := func(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x%02x", c.A, c.R, c.G, c.B) }
	inputs := []string{"#000000", "#ffffff", "#123456", "#ffff00", "#7f7f7f"}
	for _, accent := range Accents {
		inputs = append(inputs, accent.ID)
	}
	for _, theme := range []string{ThemeDark, ThemeLight} {
		for _, input := range inputs {
			d := Derive(For(theme, false), AccentColor(input))
			cases = append(cases, map[string]string{"theme": theme, "input": Hex(AccentColor(input)),
				"accent": hexA(d.Accent), "accentHot": hexA(d.AccentHot), "onAccent": hexA(d.OnAccent), "link": hexA(d.Link),
				"focus": hexA(d.Focus), "selection": hexA(d.Selection), "ring": hexA(d.Ring)})
		}
	}
	data, _ := json.MarshalIndent(cases, "", "  ")
	return append(data, '\n')
}

// TestPhoneCopy keeps the phone's tokens and its test cases in step; run
// with UPDATE_PALETTE=1 to write them.
func TestPhoneCopy(t *testing.T) {
	for path, want := range map[string][]byte{phoneTokens: kotlin(), derivedCase: derived()} {
		if os.Getenv("UPDATE_PALETTE") != "" {
			if err := os.WriteFile(filepath.FromSlash(path), want, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is out of date: run go test ./internal/palette with UPDATE_PALETTE=1", path)
		}
	}
}
