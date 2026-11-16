package painter

import (
	"testing"

	"fyne.io/fyne/v2"
	"github.com/go-text/typesetting/shaping"
)

// A tab after a change of font (an emoji between words) drew the text
// before it again, and in the wrong font (1loop #438): every character is
// drawn once, in order.
func TestTabAfterEmojiDrawsEachCharacterOnce(t *testing.T) {
	face := CachedFontFace(fyne.TextStyle{}, nil, nil)
	text := "Docker Wayland\t❌ absent\tIl n'y a de compositeur Wayland (weston, sway, cage) ni dans l'image."
	runes := []rune(text)
	var drawn []rune
	last := -1
	walkString(face.Fonts, text, float32ToFixed266(14), fyne.TextStyle{}, new(float32), 1, func(run shaping.Output, x float32) {
		for _, g := range run.Glyphs {
			if g.ClusterIndex <= last {
				t.Fatalf("%q drawn again after %q", string(runes[g.ClusterIndex]), string(drawn))
			}
			last = g.ClusterIndex
			drawn = append(drawn, runes[g.ClusterIndex])
		}
	})
	want := []rune{}
	for _, r := range runes {
		if r != '\t' {
			want = append(want, r)
		}
	}
	if string(drawn) != string(want) {
		t.Fatalf("drawn %q, want %q", string(drawn), string(want))
	}
}
