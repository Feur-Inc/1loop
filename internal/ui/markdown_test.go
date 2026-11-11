// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// paragraphSpans parses one paragraph.
func paragraphSpans(t *testing.T, markdown string) []span {
	t.Helper()
	source := []byte(markdown)
	nodes, _ := splitMarkdown(source)
	if len(nodes) != 1 {
		t.Fatalf("%q: %d blocks", markdown, len(nodes))
	}
	return inlineSpans(nodes[0], source)
}

func TestMarkdownInline(t *testing.T) {
	for _, tc := range []struct {
		markdown string
		want     []span
	}{
		{"**b** *i* ***bi*** ~~s~~ `c`", []span{{text: "b", bold: true}, {text: " "}, {text: "i", italic: true}, {text: " "},
			{text: "bi", bold: true, italic: true}, {text: " "}, {text: "s", strike: true}, {text: " "}, {text: "c", code: true}}},
		{"about ~5 minutes or ~10", []span{{text: "about ~5 minutes or ~10"}}},
		{"snake_case_name and 2 * 3 * 4", []span{{text: "snake_case_name and 2 * 3 * 4"}}},
		{`\*not\* &amp; &copy;`, []span{{text: "*not* & ©"}}},
		{"[l](https://a.example) https://b.example.", []span{{text: "l", link: "https://a.example"}, {text: " "},
			{text: "https://b.example", link: "https://b.example"}, {text: "."}}},
		{"$x^2$ and \\(y\\) cost $5 and $10", []span{{text: "$x^2$", math: "x^2"}, {text: " and "},
			{text: `\(y\)`, math: "y"}, {text: " cost $5 and $10"}}},
		{"a  \nb\nc", []span{{text: "a"}, {hard: true}, {text: "b c"}}},
		{`see <cite file="internal/ui/a.go" lines="3-4"/> now`, []span{{text: "see "},
			{text: "a.go\u00a0·\u00a03–4", cite: true, link: citeScheme + "3-4:internal/ui/a.go"}, {text: " now"}}},
		{"line<br>next", []span{{text: "line"}, {hard: true}, {text: "next"}}},
	} {
		if got := paragraphSpans(t, tc.markdown); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q:\n got %+v\nwant %+v", tc.markdown, got, tc.want)
		}
	}
}

func TestMarkdownBlocks(t *testing.T) {
	source := "# Title\n\npara\n\n```go\ncode\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n$$\nx\n$$\n\n\\[ y \\]\n\n- item\n\n$$ open"
	_, blocks := splitMarkdown([]byte(source))
	var kinds []string
	for _, block := range blocks {
		kinds = append(kinds, block.kind+"/"+block.lang)
	}
	want := []string{"text/", "text/", "code/", "table/", "math/", "math/", "text/", "math/open"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if blocks[1].text != "para" || !strings.HasPrefix(blocks[2].text, "```go") {
		t.Errorf("sources = %q, %q", blocks[1].text, blocks[2].text)
	}
}

// While an answer streams, every block before the last one is already in
// its final form, so its widgets are kept.
func TestMarkdownStreamingKeepsBlocks(t *testing.T) {
	answer := "Intro paragraph.\n\n- one\n- two\n\n```sh\nmake\n```\n\nThe end, $x$ written word by word."
	_, final := splitMarkdown([]byte(answer))
	if len(final) != 4 {
		t.Fatalf("%d blocks", len(final))
	}
	for n := 1; n <= len(answer); n++ {
		_, blocks := splitMarkdown([]byte(answer[:n]))
		for i := 0; i+1 < len(blocks); i++ {
			if blocks[i] != final[i] {
				t.Fatalf("after %q, block %d is %+v, not yet %+v", answer[:n], i, blocks[i], final[i])
			}
		}
	}
}

func TestMarkdownOpenTag(t *testing.T) {
	if got := hideOpenTag(`text <cite file="a.g`); got != "text " {
		t.Errorf("open tag: %q", got)
	}
	if got := hideOpenTag(`text <cite file="a.go"/>`); got != `text <cite file="a.go"/>` {
		t.Errorf("whole tag: %q", got)
	}
}

func TestMarkdownView(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	view := newMarkdownView("> quoted\n> - with a list\n\n1. step\n   ```bash\n   make\n   ```\n2. [x] done\n   - sub")
	if len(view.objs) != 2 {
		t.Fatalf("%d blocks", len(view.objs))
	}
	var markers []string
	var walk func(fyne.CanvasObject)
	walk = func(obj fyne.CanvasObject) {
		switch obj := obj.(type) {
		case *fyne.Container:
			for _, child := range obj.Objects {
				walk(child)
			}
		case *flowText:
			if obj.marker != "" {
				markers = append(markers, obj.marker+obj.tail)
			}
		}
	}
	for _, obj := range view.objs {
		walk(obj)
	}
	want := []string{"•\n\n", "1.\n", "☑\n", "◦\n\n"}
	if !reflect.DeepEqual(markers, want) {
		t.Errorf("markers = %q, want %q", markers, want)
	}
	first := view.objs[0]
	view.ParseMarkdown("> quoted\n> - with a list\n\n1. step")
	if view.objs[0] != first {
		t.Error("an unchanged block was built again")
	}
}
