// SPDX-License-Identifier: MPL-2.0

package palette

import (
	"strings"
	"testing"
)

func TestCheckThemeFile(t *testing.T) {
	good := "name = \"X\"\nshader = \"x.frag\"\n[colors]\nbackground = \"#101010\"\n"
	shader := "void main(){ gl_FragColor = u_accent; }"
	for _, c := range []struct{ text, shader, want string }{
		{good, shader, ""},
		{"name = \"X\"\n", "", ""},
		{good, "", "no shader source"},
		{good, "uniform float u_time;\nvoid main(){ gl_FragColor = u_accent; }", "declares u_time"},
		{good, "#version 110\n" + shader, "#version"},
		{good, "void main(){}", "gl_FragColor"},
		{"name = \"X\"\n", shader, "no shader"},
		{"shader = \"../x.frag\"\n", shader, "plain file name"},
		{"colour = 1\n", "", "unknown key"},
		{"[colors]\nbackground = \"red\"\n", "", "not #rrggbb"},
	} {
		_, err := CheckThemeFile("x", c.text, c.shader)
		if (err == nil) != (c.want == "") || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%q: got %v, want %q", c.text, err, c.want)
		}
	}
	if !strings.Contains(ThemeFormat, "u_time") {
		t.Error("format reference not embedded")
	}
}
