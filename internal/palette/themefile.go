// SPDX-License-Identifier: MPL-2.0

package palette

// Theme files (issue #464): a folder of TOML files, each a look of its own
// laid over the dark or the light tokens. The format is documented in
// docs/THEMES.md; Load reports a broken file by name and line, and never
// stops the others from loading.

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// ThemeFilePrefix marks a theme setting that names a theme file, by its
// file name without the extension: "file:nord".
const ThemeFilePrefix = "file:"

// Roundness limits: 0 is square corners, 1 is 1loop's own, 1.6 the
// roundest that still keeps text clear of the corners.
const (
	MinRoundness = 0
	MaxRoundness = 1.6
)

// ThemeFile is a theme read from a file.
type ThemeFile struct {
	ID        string        // the file name without .toml
	Path      string        // where it is
	Name      string        // as the picker shows it
	Tokens    Tokens        // the base's tokens with the file's colours
	Accents   []color.NRGBA // its own accents, if any: they replace the presets
	Roundness float64       // corners, scaled: 1 is 1loop's own
	Shader    string        // a GLSL fragment shader drawn behind the app: its path
	Image     string        // or a picture (PNG, JPEG, WebP, animated GIF): its path
	// ShaderOpacity is how opaque the background and the sidebar stay over
	// the shader, from 0 to 1.
	ShaderOpacity float64
	// Blur is a blur radius by element (BlurElements), in Fyne units: what
	// lies behind the element shows through it blurred. BlurTint is the
	// translucent colour laid over that blur, by element; an element left
	// out of it keeps its own colour, translucent.
	Blur     map[string]float64
	BlurTint map[string]color.NRGBA
	// Fonts: a font file (.ttf, .otf) beside the theme or anywhere, or a
	// family name the system knows (fc-match), by style. "" keeps 1loop's.
	Fonts ThemeFonts
}

// ThemeFonts are a theme's fonts by style.
type ThemeFonts struct {
	Regular, Bold, Italic, Mono string
}

// Setting is the theme setting that chooses this theme.
func (theme ThemeFile) Setting() string { return ThemeFilePrefix + theme.ID }

// themeSource is a theme file as written.
type themeSource struct {
	Name      string             `toml:"name"`
	Base      string             `toml:"base"`
	Accents   []string           `toml:"accents"`
	Roundness *float64           `toml:"roundness"`
	Shader    string             `toml:"shader"`
	Image     string             `toml:"background_image"`
	Opacity   *float64           `toml:"shader_opacity"`
	Font      string             `toml:"font"`
	FontBold  string             `toml:"font_bold"`
	FontItal  string             `toml:"font_italic"`
	FontMono  string             `toml:"font_mono"`
	Colors    map[string]string  `toml:"colors"`
	Blur      map[string]float64 `toml:"blur"`
	BlurTint  map[string]string  `toml:"blur_tint"`
}

// BlurElements are the parts of the window a theme can blur ([blur] and
// [blur_tint]): the sidebar, the side panel (tools, files, browser), the
// composer, a split window's pane headers, menus and popovers, dialogs.
var BlurElements = []string{"sidebar", "panel", "composer", "headers", "menus", "dialogs"}

// MaxBlur is the widest blur radius a theme may ask for.
const MaxBlur = 64

func blurElement(name string) bool {
	for _, element := range BlurElements {
		if element == name {
			return true
		}
	}
	return false
}

// ThemeColourKeys are the [colors] a theme file may set, as written in the
// file (snake_case of the Tokens' fields), in the Tokens' order.
func ThemeColourKeys() []string {
	var keys []string
	typ := reflect.TypeOf(Tokens{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type == reflect.TypeOf(color.NRGBA{}) {
			keys = append(keys, snake(typ.Field(i).Name))
		}
	}
	return keys
}

func snake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// tokenField is the Tokens field a [colors] key sets.
func tokenField(tokens *Tokens, key string) (*color.NRGBA, bool) {
	v := reflect.ValueOf(tokens).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if snake(typ.Field(i).Name) == key && typ.Field(i).Type == reflect.TypeOf(color.NRGBA{}) {
			return v.Field(i).Addr().Interface().(*color.NRGBA), true
		}
	}
	return nil, false
}

// ParseHexAlpha reads #rrggbb or #rrggbbaa.
func ParseHexAlpha(text string) (color.NRGBA, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(text), "#")
	if len(trimmed) == 8 {
		c, ok := ParseHex(trimmed[:6])
		var a uint8
		if _, err := fmt.Sscanf(strings.ToLower(trimmed[6:]), "%02x", &a); !ok || err != nil {
			return color.NRGBA{}, false
		}
		c.A = a
		return c, true
	}
	return ParseHex(trimmed)
}

// ParseTheme reads a theme file's text. id names it in errors and in the
// setting.
func ParseTheme(id, text string) (ThemeFile, error) {
	var source themeSource
	meta, err := toml.Decode(text, &source)
	if err != nil {
		var parse toml.ParseError
		if errors.As(err, &parse) {
			return ThemeFile{}, fmt.Errorf("line %d: %s", parse.Position.Line, parse.Message)
		}
		return ThemeFile{}, err
	}
	if extra := meta.Undecoded(); len(extra) > 0 {
		return ThemeFile{}, fmt.Errorf("unknown key %q (see docs/THEMES.md)", extra[0].String())
	}
	theme := ThemeFile{ID: id, Name: strings.TrimSpace(source.Name), Roundness: 1, Shader: strings.TrimSpace(source.Shader), Image: strings.TrimSpace(source.Image), ShaderOpacity: 0.8}
	if theme.Shader != "" && theme.Image != "" {
		return ThemeFile{}, errors.New("shader and background_image: choose one")
	}
	if ext := strings.ToLower(filepath.Ext(theme.Image)); theme.Image != "" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" {
		return ThemeFile{}, fmt.Errorf("background_image: %q is not a .png, .jpg, .webp or .gif", theme.Image)
	}
	if theme.Name == "" {
		theme.Name = id
	}
	switch strings.ToLower(strings.TrimSpace(source.Base)) {
	case "", ThemeDark:
		theme.Tokens = Dark
	case ThemeLight:
		theme.Tokens = Light
	default:
		return ThemeFile{}, fmt.Errorf("base: %q is neither dark nor light", source.Base)
	}
	base := theme.Tokens
	set := map[string]bool{}
	for key, value := range source.Colors {
		field, ok := tokenField(&theme.Tokens, key)
		if !ok {
			return ThemeFile{}, fmt.Errorf("colors.%s: not a colour 1loop knows (see docs/THEMES.md)", key)
		}
		c, ok := ParseHexAlpha(value)
		if !ok {
			return ThemeFile{}, fmt.Errorf("colors.%s: %q is not #rrggbb or #rrggbbaa", key, value)
		}
		*field, set[key] = c, true
	}
	derive(&theme.Tokens, base, set)
	for i, text := range source.Accents {
		c, ok := ParseHex(text)
		if !ok {
			return ThemeFile{}, fmt.Errorf("accents[%d]: %q is not #rrggbb", i, text)
		}
		theme.Accents = append(theme.Accents, c)
	}
	if source.Roundness != nil {
		r := *source.Roundness
		if math.IsNaN(r) || r < MinRoundness || r > MaxRoundness {
			return ThemeFile{}, fmt.Errorf("roundness: %v is outside %v to %v", r, MinRoundness, MaxRoundness)
		}
		theme.Roundness = r
	}
	if source.Opacity != nil {
		o := *source.Opacity
		if math.IsNaN(o) || o < 0 || o > 1 {
			return ThemeFile{}, fmt.Errorf("shader_opacity: %v is outside 0 to 1", o)
		}
		theme.ShaderOpacity = o
	}
	for element, radius := range source.Blur {
		if !blurElement(element) {
			return ThemeFile{}, fmt.Errorf("blur.%s: not an element 1loop blurs (%s)", element, strings.Join(BlurElements, ", "))
		}
		if math.IsNaN(radius) || radius < 0 || radius > MaxBlur {
			return ThemeFile{}, fmt.Errorf("blur.%s: %v is outside 0 to %d", element, radius, MaxBlur)
		}
		if theme.Blur == nil {
			theme.Blur = map[string]float64{}
		}
		theme.Blur[element] = radius
	}
	for element, text := range source.BlurTint {
		if !blurElement(element) {
			return ThemeFile{}, fmt.Errorf("blur_tint.%s: not an element 1loop blurs (%s)", element, strings.Join(BlurElements, ", "))
		}
		c, ok := ParseHexAlpha(text)
		if !ok {
			return ThemeFile{}, fmt.Errorf("blur_tint.%s: %q is not #rrggbb or #rrggbbaa", element, text)
		}
		if theme.BlurTint == nil {
			theme.BlurTint = map[string]color.NRGBA{}
		}
		theme.BlurTint[element] = c
	}
	theme.Fonts = ThemeFonts{strings.TrimSpace(source.Font), strings.TrimSpace(source.FontBold), strings.TrimSpace(source.FontItal), strings.TrimSpace(source.FontMono)}
	return theme, nil
}

// derive fills the colours a file leaves out from those it sets: the
// surfaces and borders from its background, the quieter texts from its
// text, each as far from them as the base's are from its own. A file that
// sets only a background and a text still looks whole.
func derive(tokens *Tokens, base Tokens, set map[string]bool) {
	fromBackground := []string{"sidebar", "surface", "popover", "raised", "field", "code", "border", "border_soft",
		"border_hot", "border_hover", "pop_border", "track", "diff_added", "diff_removed"}
	fromText := []string{"text_soft", "muted", "placeholder", "faint", "knob_off", "code_ink"}
	follow := func(anchor string, keys []string, toward color.NRGBA, baseToward color.NRGBA) {
		if !set[anchor] {
			return
		}
		baseAnchor, _ := tokenField(&base, anchor)
		anchorField, _ := tokenField(tokens, anchor)
		for _, key := range keys {
			if set[key] {
				continue
			}
			field, _ := tokenField(tokens, key)
			baseValue, _ := tokenField(&base, key)
			*field = mixLike(*anchorField, toward, *baseAnchor, baseToward, *baseValue)
		}
	}
	follow("background", fromBackground, tokens.Text, base.Text)
	follow("text", fromText, tokens.Background, base.Background)
}

// mixLike places a colour between from and to as far as want is between
// baseFrom and baseTo, channel by channel, keeping want's tint: a status
// colour's background keeps its green or red.
func mixLike(from, to, baseFrom, baseTo, want color.NRGBA) color.NRGBA {
	ch := func(f, t, bf, bt, w uint8) uint8 {
		span := float64(bt) - float64(bf)
		k := 0.0
		if math.Abs(span) > 1 {
			k = (float64(w) - float64(bf)) / span
		}
		v := float64(f) + (float64(t)-float64(f))*k
		return uint8(math.Max(0, math.Min(255, v+0.5)))
	}
	return color.NRGBA{R: ch(from.R, to.R, baseFrom.R, baseTo.R, want.R), G: ch(from.G, to.G, baseFrom.G, baseTo.G, want.G),
		B: ch(from.B, to.B, baseFrom.B, baseTo.B, want.B), A: want.A}
}

// IsFontFile says whether a font setting names a file rather than a family.
func IsFontFile(font string) bool {
	lower := strings.ToLower(font)
	return strings.HasSuffix(lower, ".ttf") || strings.HasSuffix(lower, ".otf") || strings.ContainsRune(font, '/')
}

// ThemeError is a theme file that could not be read.
type ThemeError struct {
	Path string
	Err  error
}

func (e ThemeError) Error() string { return filepath.Base(e.Path) + ": " + e.Err.Error() }

// ThemesDir is where theme files live: ~/.config/1loop/themes on Linux.
func ThemesDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "themes")
}

// LoadThemes reads every *.toml in dir, sorted by name, with the files
// that failed apart.
func LoadThemes(dir string) ([]ThemeFile, []ThemeError) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
	sort.Strings(paths)
	var themes []ThemeFile
	var failed []ThemeError
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			var theme ThemeFile
			theme, err = ParseTheme(strings.TrimSuffix(filepath.Base(path), ".toml"), string(data))
			if err == nil {
				theme.Path = path
				if theme.Shader != "" && !filepath.IsAbs(theme.Shader) {
					theme.Shader = filepath.Join(dir, theme.Shader)
				}
				if theme.Image != "" && !filepath.IsAbs(theme.Image) {
					theme.Image = filepath.Join(dir, theme.Image)
				}
				for _, font := range []*string{&theme.Fonts.Regular, &theme.Fonts.Bold, &theme.Fonts.Italic, &theme.Fonts.Mono} {
					if IsFontFile(*font) && !filepath.IsAbs(*font) {
						*font = filepath.Join(dir, *font)
					}
				}
				themes = append(themes, theme)
				continue
			}
		}
		failed = append(failed, ThemeError{Path: path, Err: err})
	}
	return themes, failed
}

// ThemeTemplate is what New theme writes: every key, commented, around
// 1loop's dark look.
func ThemeTemplate(name string) string {
	var b strings.Builder
	b.WriteString("# SPDX-License-Identifier: MPL-2.0\n")
	b.WriteString("# A 1loop theme: see docs/THEMES.md. Saving this file updates 1loop at once.\n\n")
	fmt.Fprintf(&b, "name = %q\n", name)
	b.WriteString("base = \"dark\"            # dark or light: what the colours left out come from\n")
	b.WriteString("accents = [\"#ffb000\"]    # one is chosen by default; several are offered in the picker\n")
	b.WriteString("roundness = 1.0          # 0 square to 1.6 round; 1 is 1loop's own\n")
	b.WriteString("# shader = \"aurora.frag\"  # a GLSL background beside this file (docs/THEMES.md)\n")
	b.WriteString("# background_image = \"wall.gif\"  # or a picture: .png, .jpg, .webp or an animated .gif\n")
	b.WriteString("# shader_opacity = 0.8    # how much the background and sidebar cover the shader or picture\n")
	b.WriteString("# font = \"IBM Plex Sans\"  # a family installed on the system, or a .ttf/.otf beside this file\n")
	b.WriteString("# font_bold = \"IBM Plex Sans:bold\"\n")
	b.WriteString("# font_italic = \"IBM Plex Sans:italic\"\n")
	b.WriteString("# font_mono = \"JetBrains Mono\"  # code, terminals, diffs\n\n")
	b.WriteString("# [blur]                  # what lies behind an element shows through it, blurred (radius 0 to 64)\n")
	b.WriteString("# sidebar = 16            # also: panel, composer, headers, menus, dialogs\n")
	b.WriteString("# [blur_tint]             # the colour laid over the blur; left out, the element's own, translucent\n")
	b.WriteString("# sidebar = \"#0c0c0c99\"\n\n")
	b.WriteString("[colors]\n")
	b.WriteString("# Only the colours you set change; surfaces follow background and the quieter texts follow text.\n")
	for _, key := range ThemeColourKeys() {
		field, _ := tokenField(&Dark, key)
		prefix := "# "
		if key == "background" || key == "text" {
			prefix = ""
		}
		value := Hex(*field)
		if field.A != 0xff {
			value += fmt.Sprintf("%02x", field.A)
		}
		fmt.Fprintf(&b, "%s%s = %q\n", prefix, key, value)
	}
	return b.String()
}
