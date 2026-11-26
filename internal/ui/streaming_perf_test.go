// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestMarkdownCodeBlockGrowsInPlace(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})

	view := newMarkdownView("Intro.\n\n```go\nline1()\nline2()\n```")
	view.box.Resize(fyne.NewSize(600, 400))
	settleBox(view.box)
	if len(view.objs) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(view.objs))
	}
	firstBox := view.objs[1]
	cv := view.codes[1]
	if cv == nil {
		t.Fatal("expected codeView for block 1")
	}
	// Lay out the sideScroll child at its real width inside the surface.
	cv.code.Resize(cv.code.MinSize())
	firstLineObj := cv.code.objects[0]

	// Append a third line to the same fenced code block: the outer box and
	// the first line's canvas.Text must be kept, and cv.text must update.
	changed := view.ParseMarkdown("Intro.\n\n```go\nline1()\nline2()\nline3()\n```")
	if !changed {
		t.Fatal("expected height change when code block adds a line")
	}
	if view.objs[1] != firstBox {
		t.Fatal("code block container was rebuilt instead of grown in place")
	}
	if cv.text != "line1()\nline2()\nline3()" {
		t.Fatalf("unexpected code text: %q", cv.text)
	}
	if len(cv.code.objects) < 3 || cv.code.objects[0] != firstLineObj {
		t.Fatal("earlier code line object was not preserved across grow")
	}
}

func TestReleaseTranscriptTextReachesCodeBlocks(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})

	view := newMarkdownView("```go\nfmt.Println(42)\n```")
	cv := view.codes[0]
	if cv == nil {
		t.Fatal("expected codeView")
	}
	cv.code.Resize(fyne.NewSize(500, cv.code.MinSize().Height))
	if len(cv.code.objects) == 0 || len(cv.code.cut) == 0 {
		t.Fatal("expected code flowText to have objects and cut atoms")
	}
	releaseTranscriptText(view.box)
	if len(cv.code.objects) != 0 || len(cv.code.cut) != 0 {
		t.Fatal("releaseTranscriptText did not release flowText inside sideScroll")
	}
}

func TestParseMarkdownSameLineNoLayoutChange(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})

	view := newMarkdownView("Hello")
	view.box.Resize(fyne.NewSize(600, 100))
	if changed := view.ParseMarkdown("Hello world"); changed {
		t.Fatal("expected no layout change when paragraph grows on the same line")
	}
	if changed := view.ParseMarkdown("Hello world\n\nSecond paragraph."); !changed {
		t.Fatal("expected layout change when a second paragraph is added")
	}
}

func TestWorkRingSteadyFrameCachedRGBA(t *testing.T) {
	now := time.Unix(1000, 0)
	ring := workRing{art: newRingArt(24)}
	ring.change("write", true, now)

	// After the 320 ms glyph pop-in settles, steady frames are cached *image.RGBA.
	t1 := now.Add(500 * time.Millisecond)
	img1, still := ring.frame(t1)
	if still {
		t.Fatal("write state should not be still")
	}
	rgba1, ok := img1.(*image.RGBA)
	if !ok {
		t.Fatalf("expected *image.RGBA, got %T", img1)
	}
	// Within 50 ms throttle returns nil.
	if imgSkip, _ := ring.frame(t1.Add(20 * time.Millisecond)); imgSkip != nil {
		t.Fatal("expected throttled frame to return nil")
	}
	// Full rotation (1100 ms) lands on the same angle step and returns the exact cached *image.RGBA.
	t2 := t1.Add(1100 * time.Millisecond)
	img2, _ := ring.frame(t2)
	rgba2, ok := img2.(*image.RGBA)
	if !ok {
		t.Fatalf("expected *image.RGBA, got %T", img2)
	}
	if rgba1 != rgba2 {
		t.Fatal("expected steady-state workRing frame to be cached and reused")
	}
}
