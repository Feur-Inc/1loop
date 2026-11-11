// SPDX-License-Identifier: MPL-2.0

package ui

// Markdown parsing. goldmark (CommonMark, with GitHub's tables, task lists
// and bare links) reads the answer into a tree; this file adds what models
// write and CommonMark lacks, formulas ($…$, \(…\), $$…$$, \[…\]) and
// strikethrough with two tildes only (one tilde is "about": ~5 minutes),
// and turns the tree's inline nodes into spans, each a run of text in one
// style, that flowText lays out.

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
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

// markdownParser reads answers. A goldmark parser is safe for concurrent use.
var markdownParser = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.TaskList, extension.Linkify),
	goldmark.WithParserOptions(
		parser.WithBlockParsers(util.Prioritized(mathBlockParser{}, 150)),
		parser.WithInlineParsers(
			util.Prioritized(mathInlineParser{}, 90), // before code spans and escapes
			util.Prioritized(strikeParser{}, 500),
		),
	),
).Parser()

func parseMarkdown(source []byte) ast.Node {
	return markdownParser.Parse(text.NewReader(source))
}

// inlineSpans turns a block's inline nodes into spans.
func inlineSpans(block ast.Node, source []byte) []span {
	builder := &spanBuilder{source: source}
	builder.walk(block, span{})
	// A line break at the very end draws nothing.
	for len(builder.out) > 0 && builder.out[len(builder.out)-1].hard {
		builder.out = builder.out[:len(builder.out)-1]
	}
	return builder.out
}

type spanBuilder struct {
	source []byte
	out    []span
}

// text appends plain text in a style, joining it to the previous span when
// that one has the same style.
func (builder *spanBuilder) text(text string, style span) {
	if text == "" {
		return
	}
	style.text = text
	if n := len(builder.out); n > 0 {
		last := &builder.out[n-1]
		same := *last
		same.text = text
		if same == style && !last.hard && last.math == "" {
			last.text += text
			return
		}
	}
	builder.out = append(builder.out, style)
}

func (builder *spanBuilder) walk(parent ast.Node, style span) {
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		switch node := node.(type) {
		case *ast.Text:
			value := node.Segment.Value(builder.source)
			if !node.IsRaw() {
				value = util.UnescapePunctuations(value)
				value = util.ResolveNumericReferences(value)
				value = util.ResolveEntityNames(value)
			}
			builder.text(string(value), style)
			switch {
			case node.HardLineBreak():
				builder.out = append(builder.out, span{hard: true})
			case node.SoftLineBreak():
				builder.text(" ", style)
			}
		case *ast.String:
			builder.text(string(node.Value), style)
		case *ast.CodeSpan:
			code := style
			code.code = true
			builder.text(strings.TrimSpace(plainText(node, builder.source)), code)
		case *ast.Emphasis:
			inner := style
			if node.Level >= 2 {
				inner.bold = true
			} else {
				inner.italic = true
			}
			builder.walk(node, inner)
		case *east.Strikethrough:
			inner := style
			inner.strike = true
			builder.walk(node, inner)
		case *ast.Link:
			inner := style
			inner.link = string(node.Destination)
			builder.walk(node, inner)
		case *ast.Image: // shown as a link to it
			inner := style
			inner.link = string(node.Destination)
			label := plainText(node, builder.source)
			if label == "" {
				label = inner.link
			}
			builder.text(label, inner)
		case *ast.AutoLink:
			inner := style
			inner.link = string(node.URL(builder.source))
			builder.text(string(node.Label(builder.source)), inner)
		case *ast.RawHTML:
			builder.html(rawHTML(node, builder.source), style)
		case *mathInline:
			builder.out = append(builder.out, span{math: node.formula, text: node.source})
		case *east.TaskCheckBox: // the list item's marker shows it
		default:
			builder.walk(node, style)
		}
	}
}

// html reads an inline tag: a citation becomes its chip, <br> a line
// break; any other tag is shown as written.
func (builder *spanBuilder) html(raw string, style span) {
	if cited, n, ok := citeAt(raw); ok && n == len(raw) {
		builder.out = append(builder.out, cited)
		return
	}
	if inside := strings.TrimSpace(strings.Trim(raw, "<>/")); strings.EqualFold(inside, "br") {
		builder.out = append(builder.out, span{hard: true})
		return
	}
	builder.text(raw, style)
}

// htmlSpans reads a block of HTML (a citation alone on its line is one)
// as text whose tags are read by html.
func htmlSpans(source string) []span {
	builder := &spanBuilder{}
	for i, line := range strings.Split(strings.TrimSpace(source), "\n") {
		if i > 0 {
			builder.text(" ", span{})
		}
		for line != "" {
			at := strings.IndexByte(line, '<')
			if at < 0 {
				builder.text(line, span{})
				break
			}
			builder.text(line[:at], span{})
			end := strings.IndexByte(line[at:], '>')
			if end < 0 {
				builder.text(line[at:], span{})
				break
			}
			builder.html(line[at:at+end+1], span{})
			line = line[at+end+1:]
		}
	}
	return builder.out
}

func rawHTML(node *ast.RawHTML, source []byte) string {
	var raw strings.Builder
	for i := 0; i < node.Segments.Len(); i++ {
		segment := node.Segments.At(i)
		raw.Write(segment.Value(source))
	}
	return raw.String()
}

// plainText is the text inside a node, without its styles.
func plainText(parent ast.Node, source []byte) string {
	var out strings.Builder
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		switch node := node.(type) {
		case *ast.Text:
			out.Write(node.Segment.Value(source))
			if node.SoftLineBreak() || node.HardLineBreak() {
				out.WriteByte(' ')
			}
		case *ast.String:
			out.Write(node.Value)
		default:
			out.WriteString(plainText(node, source))
		}
	}
	return out.String()
}

// ---------------------------------------------------------------------------
// Formulas.

var (
	kindMathInline = ast.NewNodeKind("MathInline")
	kindMathBlock  = ast.NewNodeKind("MathBlock")
)

// mathInline is $…$ or \(…\) inside a line.
type mathInline struct {
	ast.BaseInline
	formula string // the LaTeX
	source  string // as written, for copying
}

func (node *mathInline) Kind() ast.NodeKind { return kindMathInline }
func (node *mathInline) Dump(source []byte, level int) {
	ast.DumpHelper(node, source, level, map[string]string{"formula": node.formula}, nil)
}

type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$', '\\'} }

func (mathInlineParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	s := strings.TrimRight(string(line), "\r\n")
	var formula string
	var n int
	switch {
	case strings.HasPrefix(s, `\(`):
		end := strings.Index(s, `\)`)
		if end <= 2 {
			return nil
		}
		formula, n = s[2:end], end+2
	case strings.HasPrefix(s, "$"):
		end := mathEnd(s)
		if end == 0 {
			return nil
		}
		formula, n = s[1:end], end+1
	default:
		return nil
	}
	block.Advance(n)
	return &mathInline{formula: strings.TrimSpace(formula), source: s[:n]}
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

// mathBlock is a formula on its own lines: $$ … $$ or \[ … \].
type mathBlock struct {
	ast.BaseBlock
	closing string
	lines   []string
	closed  bool // false while its end is not written yet
}

func (node *mathBlock) Kind() ast.NodeKind { return kindMathBlock }
func (node *mathBlock) Dump(source []byte, level int) {
	ast.DumpHelper(node, source, level, map[string]string{"formula": node.formula()}, nil)
}
func (node *mathBlock) formula() string { return strings.TrimSpace(strings.Join(node.lines, "\n")) }

// add reads a line of the formula; it reports whether the line ends it.
func (node *mathBlock) add(line string) bool {
	if end := strings.Index(line, node.closing); end >= 0 {
		node.lines = append(node.lines, line[:end])
		node.closed = true
		return true
	}
	node.lines = append(node.lines, line)
	return false
}

type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{'$', '\\'} }

func (mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	at := pc.BlockOffset()
	if at < 0 {
		return nil, parser.NoChildren
	}
	rest := strings.TrimRight(string(line[at:]), "\r\n")
	var node *mathBlock
	switch {
	case strings.HasPrefix(rest, "$$"):
		node = &mathBlock{closing: "$$"}
	case strings.HasPrefix(rest, `\[`):
		node = &mathBlock{closing: `\]`}
	default:
		return nil, parser.NoChildren
	}
	reader.AdvanceToEOL()
	node.add(rest[2:]) // when it closes on this line, Continue ends it
	return node, parser.NoChildren
}

func (mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	if line == nil || node.(*mathBlock).closed { // a formula on one line ended where it opened
		return parser.Close
	}
	done := node.(*mathBlock).add(strings.TrimRight(string(line), "\r\n"))
	reader.AdvanceToEOL()
	if done {
		return parser.Close
	}
	return parser.Continue | parser.NoChildren
}

func (mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}
func (mathBlockParser) CanInterruptParagraph() bool                                { return true }
func (mathBlockParser) CanAcceptIndentedLine() bool                                { return false }

// ---------------------------------------------------------------------------
// ~~strikethrough~~: GitHub's, without the single tilde.

type strikeDelimiter struct{}

func (strikeDelimiter) IsDelimiter(b byte) bool { return b == '~' }
func (strikeDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char && opener.OriginalLength == closer.OriginalLength
}
func (strikeDelimiter) OnMatch(consumes int) ast.Node { return east.NewStrikethrough() }

type strikeParser struct{}

func (strikeParser) Trigger() []byte { return []byte{'~'} }

func (strikeParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 1, strikeDelimiter{})
	if node == nil || node.OriginalLength != 2 || before == '~' {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

func (strikeParser) CloseBlock(parent ast.Node, pc parser.Context) {}
