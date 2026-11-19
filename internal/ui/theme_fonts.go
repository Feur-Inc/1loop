// SPDX-License-Identifier: MPL-2.0

package ui

// Theme fonts (issue #464): a theme file can set the interface's font, its
// bold and italic, and the monospace one, as a font file or a family name
// fontconfig resolves. Fyne clears its font cache when the theme is set
// again (repaint), so a change shows at once.

import (
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"

	"1loop/internal/palette"
	"1loop/internal/platform"
)

// themeFonts are the active theme's faces by interFaces' keys and "mono";
// a style it leaves out keeps 1loop's.
var themeFonts = map[string]fyne.Resource{}

// fontsLoaded is what themeFonts were read from.
var fontsLoaded palette.ThemeFonts

// loadThemeFonts reads a theme's fonts, and says whether they changed.
func loadThemeFonts(fonts palette.ThemeFonts) bool {
	if fonts == fontsLoaded {
		return false
	}
	fontsLoaded = fonts
	themeFonts = map[string]fyne.Resource{}
	bold, italic := fonts.Bold, fonts.Italic
	if bold == "" && fonts.Regular != "" && !palette.IsFontFile(fonts.Regular) {
		bold = fonts.Regular + ":bold" // the family's own bold
	}
	if italic == "" && fonts.Regular != "" && !palette.IsFontFile(fonts.Regular) {
		italic = fonts.Regular + ":italic"
	}
	for key, font := range map[string]string{"regular": fonts.Regular, "bold": bold, "italic": italic, "mono": fonts.Mono} {
		if font == "" {
			continue
		}
		if face := readFont(font); face != nil {
			themeFonts[key] = face
		}
	}
	return true
}

// readFont reads a font file, or the file fontconfig picks for a family
// ("Name" or "Name:bold"); nil when there is none.
func readFont(font string) fyne.Resource {
	path := font
	if !palette.IsFontFile(font) {
		out, err := exec.Command("fc-match", "-f", "%{file}", font).Output()
		if err != nil || len(out) == 0 {
			platform.Devf("ui", "theme font %q: not found", font)
			return nil
		}
		path = string(out)
		// fc-match always answers: a family it lacks gets the default font.
		family, _ := exec.Command("fc-match", "-f", "%{family}", font).Output()
		want := strings.ToLower(strings.SplitN(font, ":", 2)[0])
		if !strings.Contains(strings.ToLower(string(family)), want) {
			platform.Devf("ui", "theme font %q: not installed (fc-match gave %s)", font, family)
			return nil
		}
	}
	data, err := palette.ReadThemeFile(path)
	if err != nil {
		platform.Devf("ui", "theme font %q: %v", font, err)
		return nil
	}
	return fyne.NewStaticResource(filepath.Base(path), data)
}
