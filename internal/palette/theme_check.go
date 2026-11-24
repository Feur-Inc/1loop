// SPDX-License-Identifier: MPL-2.0

package palette

import (
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ThemeFormat is the short reference of the theme file format that the
// agent reads on demand (docs/THEMES.md is the user's longer one).
//
//go:embed theme_format.md
var ThemeFormat string

var (
	shaderUniform = regexp.MustCompile(`\buniform\s+\w+\s+(u_(resolution|origin|time|background|surface|text|accent|state|state_time))\b`)
	shaderMain    = regexp.MustCompile(`\bvoid\s+main\s*\(`)
	// ThemeNameUnsafe is what a theme's file name may not contain.
	ThemeNameUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)
)

// CheckShader says in plain words what is wrong with a theme shader's
// source before it is saved; the compiler has the last word at paint time.
func CheckShader(source string) error {
	switch {
	case strings.TrimSpace(source) == "":
		return errors.New("the shader is empty")
	case strings.Contains(source, "#version"):
		return errors.New("the shader must not start with #version: 1loop adds it")
	case !shaderMain.MatchString(source):
		return errors.New("the shader has no void main()")
	case !strings.Contains(source, "gl_FragColor"):
		return errors.New("the shader never sets gl_FragColor (use it instead of an out variable)")
	}
	if m := shaderUniform.FindStringSubmatch(source); m != nil {
		return fmt.Errorf("the shader declares %s: 1loop already declares it, remove that line", m[1])
	}
	return nil
}

// CheckThemeFile reads a theme's text as 1loop would and, when it names a
// shader, checks that the shader is a plain file name that was given.
func CheckThemeFile(name, text, shader string) (ThemeFile, error) {
	theme, err := ParseTheme(name, text)
	if err != nil {
		return theme, err
	}
	if theme.Shader != "" {
		if filepath.Base(theme.Shader) != theme.Shader || filepath.Ext(theme.Shader) != ".frag" {
			return theme, fmt.Errorf("shader: %q must be a plain file name ending in .frag, beside the theme", theme.Shader)
		}
		if shader == "" {
			return theme, fmt.Errorf("shader: the theme names %s but no shader source was given", theme.Shader)
		}
		if err := CheckShader(shader); err != nil {
			return theme, fmt.Errorf("shader %s: %v", theme.Shader, err)
		}
	} else if shader != "" {
		return theme, errors.New("a shader was given but the theme has no shader = \"name.frag\" line")
	}
	if theme.Image != "" && filepath.Base(theme.Image) != theme.Image {
		return theme, fmt.Errorf("background_image: %q must be a plain file name beside the theme", theme.Image)
	}
	return theme, nil
}
