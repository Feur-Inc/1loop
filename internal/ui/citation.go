package ui

// A citation in the model's answer: <cite file="path" lines="12-30"/>, as
// AI search engines cite their sources. It shows as a chip, the file's name
// and lines; a click opens the file in the Files tab, those lines marked.

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// citeScheme starts a citation chip's link: citeScheme + lines + ":" + path.
const citeScheme = "1loop-cite:"

var citeTag = regexp.MustCompile(`^<cite\s+file="([^"]+)"(?:\s+lines="(\d+)(?:\s*[-–]\s*(\d+))?")?\s*/?>(?:</cite>)?`)

// citeAt reads the citation tag at the start of s: its chip and its length.
func citeAt(s string) (span, int, bool) {
	match := citeTag.FindStringSubmatchIndex(s)
	if match == nil {
		return span{}, 0, false
	}
	file := s[match[2]:match[3]]
	from, to := 0, 0
	if match[4] >= 0 {
		from, _ = strconv.Atoi(s[match[4]:match[5]])
		to = from
		if match[6] >= 0 {
			to, _ = strconv.Atoi(s[match[6]:match[7]])
		}
	}
	label := path.Base(file)
	switch { // no-break spaces: a chip does not wrap
	case from > 0 && to > from:
		label += fmt.Sprintf("\u00a0·\u00a0%d–%d", from, to)
	case from > 0:
		label += fmt.Sprintf("\u00a0·\u00a0%d", from)
	}
	return span{text: label, cite: true, link: fmt.Sprintf("%s%d-%d:%s", citeScheme, from, to, file)}, match[1], true
}

// openCitation shows a chip's file in the Files tab (set by the desktop).
var openCitation = func(link string) {}

func (ui *desktopUI) openCitation(link string) {
	lines, file, _ := strings.Cut(strings.TrimPrefix(link, citeScheme), ":")
	from, to := 0, 0
	if _, err := fmt.Sscanf(lines, "%d-%d", &from, &to); err != nil || file == "" {
		return
	}
	ui.panel.openFileAt(file, from, to)
}
