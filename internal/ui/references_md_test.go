// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

// References in an answer's text become links, after goldmark (#303)
// replaced the hand-written inline parser they were found in.
func TestMarkdownReferences(t *testing.T) {
	source := []byte("See #12 and Feur-Inc/1loop#5, not a#1, `#7` or [#8](https://example.com).")
	doc := parseMarkdown(source)
	links := map[string]string{}
	for _, s := range inlineSpans(doc.FirstChild(), source) {
		if s.link != "" {
			links[s.text] = s.link
		}
	}
	for text, want := range map[string]string{"#12": refScheme + "#12", "Feur-Inc/1loop#5": refScheme + "Feur-Inc/1loop#5", "#8": "https://example.com"} {
		if links[text] != want {
			t.Errorf("%q links to %q, want %q (links: %v)", text, links[text], want, links)
		}
	}
	for _, text := range []string{"a#1", "#1", "#7"} {
		if _, ok := links[text]; ok {
			t.Errorf("%q became a link (links: %v)", text, links)
		}
	}
}
