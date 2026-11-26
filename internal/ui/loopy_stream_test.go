// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

func shownText(objects []fyne.CanvasObject) string {
	var b strings.Builder
	for _, o := range objects {
		if t, ok := o.(*canvas.Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func TestLoopyBubbleGrowsAsItStreams(t *testing.T) {
	test.NewTempApp(t)
	bubble := newLoopyBubble("Ça ro", false, "")
	bubble.row.Resize(fyne.NewSize(672, bubble.row.MinSize().Height))
	before := bubble.box.Size()
	long := "Ça roule, merci ! Je suis prêt à t'aider pour ce que tu veux aujourd'hui."
	bubble.setText(long)
	bubble.row.Resize(fyne.NewSize(672, bubble.row.MinSize().Height))
	t.Logf("box %v -> %v, text %v, shows %q", before, bubble.box.Size(), bubble.text.Size(), shownText(bubble.text.objects))
	if bubble.box.Size().Width <= before.Width || !strings.Contains(shownText(bubble.text.objects), "merci") {
		t.Fatal("the bubble did not grow")
	}
}

// An error shown in a form, then another: the second is what shows.
func TestFlowTextSaysWhatItIsGiven(t *testing.T) {
	test.NewTempApp(t)
	text := plainFlow("", 12.5, colDanger)
	text.Resize(fyne.NewSize(400, 40))
	for _, said := range []string{"First problem", "The second one, longer"} {
		text.setSpans([]span{{text: said}})
		text.Resize(fyne.NewSize(400, text.MinSize().Height))
		if got := shownText(text.objects); got != said {
			t.Errorf("%q shows %q", said, got)
		}
	}
}
