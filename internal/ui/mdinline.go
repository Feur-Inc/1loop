package ui

// Inline Markdown: the styles inside a paragraph, a heading, a list item or
// a table cell. parseInline turns the source into spans, each a run of text
// in one style.
//
// Supported: **bold**, *italic*, ***both***, __bold__, _italic_ (not inside
// a word), ~~strikethrough~~, `code` (any number of backticks), links
// [text](url), images shown as links, <https://…> and bare URLs, inline
// math $…$ and \(…\), backslash escapes, <br> and hard line breaks.

import (
	"regexp"
	"strings"
	"unicode"
)

type span struct {
	text   string
	bold   bool
	italic bool
	strike bool
	code   bool   // inline code: monospace on a tinted chip
	cite   bool   // a citation of the workspace's code: a small chip, its link opens the file
	link   string // where the run leads, when it is a link
	math   string // LaTeX source, drawn typeset when a TeX system is installed
	hard   bool   // a line break
}

var bareURL = regexp.MustCompile(`^https?://[^\s<>"'` + "`" + `]+`)

// parseInline parses one block's inline source.
func parseInline(source string) []span {
	parser := &inlineParser{}
	// A line ending with two spaces or a backslash breaks the line; any
	// other line ending inside a paragraph is a space.
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		hard := strings.HasSuffix(line, "  ") || strings.HasSuffix(line, "\\")
		line = strings.TrimRight(strings.TrimSuffix(strings.TrimRight(line, " "), "\\"), " ")
		if i > 0 {
			line = strings.TrimLeft(line, " \t")
		}
		parser.parse(line, span{})
		if i < len(lines)-1 {
			if hard {
				parser.out = append(parser.out, span{hard: true})
			} else {
				parser.text(" ", span{})
			}
		}
	}
	return parser.out
}

type inlineParser struct {
	out []span
}

// text appends plain text in a style, joining it to the previous span when
// that one has the same style.
func (parser *inlineParser) text(text string, style span) {
	if text == "" {
		return
	}
	style.text = text
	if n := len(parser.out); n > 0 {
		last := &parser.out[n-1]
		same := *last
		same.text = text
		if same == style && !last.hard && last.math == "" {
			last.text += text
			return
		}
	}
	parser.out = append(parser.out, style)
}

// parse reads s in the given style (what the enclosing emphasis set).
func (parser *inlineParser) parse(s string, style span) {
	plain := 0 // start of the plain text not yet written
	flush := func(end int) {
		parser.text(s[plain:end], style)
	}
	for i := 0; i < len(s); {
		c := s[i]
		rest := s[i:]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\`*_{}[]()#+-.!~$<>|", s[i+1]) >= 0:
			if s[i+1] == '(' { // \( … \): inline math
				if end := strings.Index(rest, `\)`); end > 2 {
					flush(i)
					parser.out = append(parser.out, span{math: strings.TrimSpace(rest[2:end]), text: rest[:end+2]})
					i += end + 2
					plain = i
					continue
				}
			}
			flush(i)
			parser.text(s[i+1:i+2], style)
			i += 2
			plain = i
			continue
		case c == '`':
			ticks := 0
			for ticks < len(rest) && rest[ticks] == '`' {
				ticks++
			}
			fence := rest[:ticks]
			if end := strings.Index(rest[ticks:], fence); end >= 0 {
				flush(i)
				code := style
				code.code = true
				parser.text(strings.TrimSpace(rest[ticks:ticks+end]), code)
				i += ticks + end + ticks
				plain = i
				continue
			}
		case c == '$':
			if end := mathEnd(rest); end > 0 {
				flush(i)
				parser.out = append(parser.out, span{math: strings.TrimSpace(rest[1:end]), text: rest[:end+1]})
				i += end + 1
				plain = i
				continue
			}
		case c == '*' || c == '_' || c == '~':
			run := 0
			for run < len(rest) && rest[run] == c {
				run++
			}
			if n, inner, ok := emphasis(s, i, run); ok {
				flush(i)
				spanValue := style
				switch {
				case c == '~':
					spanValue.strike = true
				case n == 1:
					spanValue.italic = true
				case n == 2:
					spanValue.bold = true
				default:
					spanValue.bold, spanValue.italic = true, true
				}
				parser.parse(inner, spanValue)
				i += n + len(inner) + n
				plain = i
				continue
			}
			i += run // not emphasis: the marks are text
			continue
		case c == '!' && strings.HasPrefix(rest, "!["):
			if label, url, n, ok := linkAt(rest[1:]); ok {
				flush(i)
				in := style
				in.link = url
				if label == "" {
					label = url
				}
				parser.text(label, in)
				i += 1 + n
				plain = i
				continue
			}
		case c == '[':
			if label, url, n, ok := linkAt(rest); ok {
				flush(i)
				in := style
				in.link = url
				parser.parse(label, in)
				i += n
				plain = i
				continue
			}
		case c == '<' && strings.HasPrefix(rest, "<cite"):
			flush(i)
			if cited, n, ok := citeAt(rest); ok {
				parser.out = append(parser.out, cited)
				i += n
			} else {
				i = len(s) // a tag still arriving: shown once whole
			}
			plain = i
			continue
		case c == '<':
			if end := strings.IndexByte(rest, '>'); end > 0 {
				inside := rest[1:end]
				switch {
				case bareURL.MatchString(inside) && bareURL.FindString(inside) == inside:
					flush(i)
					in := style
					in.link = inside
					parser.text(inside, in)
					i += end + 1
					plain = i
					continue
				case strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(inside), "/"), "br"):
					flush(i)
					parser.out = append(parser.out, span{hard: true})
					i += end + 1
					plain = i
					continue
				}
			}
		case c == 'h' && style.link == "" && (i == 0 || !isWordByte(s[i-1])):
			if url := bareURL.FindString(rest); url != "" {
				url = trimURL(url)
				flush(i)
				in := style
				in.link = url
				parser.text(url, in)
				i += len(url)
				plain = i
				continue
			}
		}
		i++
	}
	flush(len(s))
}

func isWordByte(b byte) bool {
	return b >= 0x80 || unicode.IsLetter(rune(b)) || unicode.IsDigit(rune(b))
}

// trimURL drops the punctuation that ends a sentence rather than the URL.
func trimURL(url string) string {
	for len(url) > 0 {
		last := url[len(url)-1]
		if strings.IndexByte(".,;:!?*_~", last) >= 0 ||
			(last == ')' && strings.Count(url, "(") < strings.Count(url, ")")) {
			url = url[:len(url)-1]
			continue
		}
		break
	}
	return url
}

// mathEnd finds the closing $ of inline math starting at s[0] == '$', or 0.
// Prices ("$5 and $10") are not math: the content may not start or end
// with a space, and the closing sign may not be followed by a digit.
func mathEnd(s string) int {
	if len(s) < 3 || s[1] == ' ' || s[1] == '$' {
		return 0
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '\n':
			return 0
		case '$':
			if s[i-1] == ' ' || (i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9') {
				return 0
			}
			return i
		}
	}
	return 0
}

// emphasis looks for what closes the run of marks of length run starting
// at s[at]. It returns how many marks delimit and what is between them.
// Runs of marks met on the way that open an emphasis of their own are
// matched with their closers first, so "*a **b** c*" and "**a *b***" nest.
func emphasis(s string, at, run int) (n int, inner string, ok bool) {
	c := s[at]
	n = run
	if c == '~' {
		if run < 2 {
			return 0, "", false
		}
		n = 2
	} else if n > 3 {
		n = 3
	}
	open := at + run
	if open >= len(s) || s[open] == ' ' {
		return 0, "", false // "2 * 3": marks before a space open nothing
	}
	if c == '_' && at > 0 && isWordByte(s[at-1]) {
		return 0, "", false // snake_case is not emphasis
	}
	var nested []int // lengths of the runs opened inside and not closed yet
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
			continue
		case '`': // code hides marks
			if end := strings.IndexByte(s[j+1:], '`'); end >= 0 {
				j += end + 1
			}
			continue
		case c:
		default:
			continue
		}
		length := 1
		for j+length < len(s) && s[j+length] == c {
			length++
		}
		before, after := s[j-1], byte(' ')
		if j+length < len(s) {
			after = s[j+length]
		}
		closes := before != ' '
		opens := after != ' ' && (before == ' ' || strings.IndexByte("([{\"'", before) >= 0)
		if c == '_' {
			closes = closes && !isWordByte(after)
			opens = opens && !isWordByte(before)
		}
		switch {
		case opens && !closes:
			nested = append(nested, length)
		case closes:
			left := length
			for left > 0 && len(nested) > 0 {
				top := nested[len(nested)-1]
				if top > left {
					nested[len(nested)-1] = top - left
					left = 0
				} else {
					left -= top
					nested = nested[:len(nested)-1]
				}
			}
			if len(nested) == 0 && left >= n {
				end := j + length - n // its last marks are ours
				if end > at+n {
					return n, s[at+n : end], true
				}
			}
		}
		j += length - 1
	}
	return 0, "", false
}

// linkAt parses [label](url "title") at the start of s.
func linkAt(s string) (label, url string, n int, ok bool) {
	depth := 0
	closing := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				closing = i
			}
		}
		if closing >= 0 {
			break
		}
	}
	if closing < 0 || closing+1 >= len(s) || s[closing+1] != '(' {
		return "", "", 0, false
	}
	depth = 0
	for i := closing + 1; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				target := strings.TrimSpace(s[closing+2 : i])
				if space := strings.IndexAny(target, " \t"); space > 0 {
					target = target[:space] // a title follows the address
				}
				target = strings.Trim(target, "<>")
				if target == "" {
					return "", "", 0, false
				}
				return s[1:closing], target, i + 1, true
			}
		case '\n':
			return "", "", 0, false
		}
	}
	return "", "", 0, false
}
