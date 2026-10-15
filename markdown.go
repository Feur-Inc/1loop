//go:build !server

package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// markdownView renders a model answer. The text is cut into blocks
// (splitMarkdown: prose, fenced code, tables, formulas on their own line);
// prose is cut again into headings, paragraphs, lists, quotes and rules
// (renderText), and every line of text is laid out by flowText, which draws
// the inline styles and knows where each word is, for links and selection.
type markdownView struct {
	layer  *selectLayer // over the box: selects its text with the mouse
	box    *fyne.Container
	blocks []mdBlock
	objs   []fyne.CanvasObject
}

type mdBlock struct {
	kind string // "text", "code", "table", "math"
	lang string // code: its language; math: "open" while its end is not written yet
	text string
}

func newMarkdownView(text string) *markdownView {
	view := &markdownView{box: container.New(layout.NewCustomPaddedVBoxLayout(12))}
	view.ParseMarkdown(text)
	return view
}

// ParseMarkdown re-renders the text. Unchanged blocks keep their widgets:
// while an answer streams, only its last block is built again.
func (view *markdownView) ParseMarkdown(text string) {
	blocks := splitMarkdown(text)
	objs := make([]fyne.CanvasObject, len(blocks))
	reshaped := len(blocks) != len(view.blocks)
	for i, mdBlock := range blocks {
		if i < len(view.blocks) && view.blocks[i] == mdBlock {
			objs[i] = view.objs[i]
			continue
		}
		objs[i] = renderBlock(mdBlock)
		reshaped = true
	}
	view.blocks, view.objs = blocks, objs
	if reshaped {
		// New widgets are drawn on their own once placed: lay the box out,
		// without the recursive refresh of Container.Refresh.
		view.box.Objects = objs
		view.box.Layout.Layout(objs, view.box.Size())
		canvas.Refresh(view.box)
	}
	if view.layer != nil {
		view.layer.changed()
	}
}

func renderBlock(mdBlockValue mdBlock) fyne.CanvasObject {
	switch mdBlockValue.kind {
	case "code":
		return markdownCode(mdBlockValue.lang, mdBlockValue.text)
	case "table":
		return newMarkdownTable(mdBlockValue.text)
	case "math":
		if mdBlockValue.lang == "open" { // still being written: not worth composing yet
			return markdownCode("latex", mdBlockValue.text)
		}
		formula := newFlowText([]span{{math: mdBlockValue.text, text: mdBlockValue.text}}, theme.TextSize(), colText)
		formula.center = true
		return padded(4, 4, 0, 0, formula)
	}
	return renderText(mdBlockValue.text)
}

var tableRule = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)

// splitMarkdown cuts the text into prose, fenced code, table and formula
// blocks. An unterminated fence (still streaming) is code up to the end.
func splitMarkdown(text string) []mdBlock {
	lines := strings.Split(text, "\n")
	var blocks []mdBlock
	var plain []string
	flush := func() {
		for _, part := range paragraphs(plain) {
			blocks = append(blocks, mdBlock{kind: "text", text: part})
		}
		plain = nil
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			fence := trimmed[:3]
			lang := strings.TrimSpace(strings.TrimLeft(trimmed, "`~"))
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
				row := lines[i]
				for n := 0; n < indent && len(row) > 0 && (row[0] == ' ' || row[0] == '\t'); n++ {
					row = row[1:] // a fence indented in a list: its code is too
				}
				code = append(code, row)
			}
			blocks = append(blocks, mdBlock{kind: "code", lang: lang, text: strings.Join(code, "\n")})
			continue
		}
		// A formula on its own lines: $$ … $$ or \[ … \].
		if opening, closing := mathFence(trimmed); opening != "" {
			flush()
			body := strings.TrimPrefix(trimmed, opening)
			state := "open"
			var formula []string
			for {
				if end := strings.Index(body, closing); end >= 0 {
					formula = append(formula, body[:end])
					state = ""
					break
				}
				formula = append(formula, body)
				if i++; i >= len(lines) {
					break
				}
				body = lines[i]
			}
			blocks = append(blocks, mdBlock{kind: "math", lang: state, text: strings.TrimSpace(strings.Join(formula, "\n"))})
			continue
		}
		if strings.Contains(line, "|") && i+1 < len(lines) && tableRule.MatchString(lines[i+1]) {
			flush()
			rows := []string{line}
			for i += 2; i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != ""; i++ {
				rows = append(rows, lines[i])
			}
			i--
			blocks = append(blocks, mdBlock{kind: "table", text: strings.Join(rows, "\n")})
			continue
		}
		plain = append(plain, line)
	}
	flush()
	return blocks
}

func mathFence(line string) (opening, closing string) {
	switch {
	case strings.HasPrefix(line, "$$"):
		return "$$", "$$"
	case strings.HasPrefix(line, `\[`):
		return `\[`, `\]`
	}
	return "", ""
}

var (
	listItem   = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	headingRow = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	ruleRow    = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
)

// paragraphs splits prose at blank lines and around headings, so the view
// can space blocks like a page; a list stays in one piece.
func paragraphs(lines []string) []string {
	var parts []string
	var current []string
	flush := func() {
		if text := strings.Trim(strings.Join(current, "\n"), "\n"); strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
		current = nil
	}
	lastContent := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			next := ""
			for _, l := range lines[i+1:] {
				if strings.TrimSpace(l) != "" {
					next = l
					break
				}
			}
			inList := listItem.MatchString(lastContent) || strings.HasPrefix(lastContent, "  ")
			if !(inList && (listItem.MatchString(next) || strings.HasPrefix(next, "  "))) {
				flush()
				continue
			}
		}
		if strings.HasPrefix(trimmed, "#") && len(current) > 0 {
			flush()
		}
		current = append(current, line)
		if trimmed != "" {
			lastContent = line
		}
		if strings.HasPrefix(trimmed, "#") {
			flush() // a heading is its own block
		}
	}
	flush()
	return parts
}

var headingSizes = [...]float32{22, 19, 16.5, 15, 14, 14}

// renderText lays one block of prose out: its headings, paragraphs, lists,
// quotes and rules, top to bottom.
func renderText(text string) fyne.CanvasObject {
	size := theme.TextSize()
	lines := strings.Split(text, "\n")
	var out []fyne.CanvasObject
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, newFlowText(parseInline(strings.Join(para, "\n")), size, colText))
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
		case headingRow.MatchString(trimmed):
			flush()
			found := headingRow.FindStringSubmatch(trimmed)
			heading := newFlowText(parseInline(found[2]), headingSizes[len(found[1])-1], colText)
			heading.bold = true
			out = append(out, padded(6, 0, 0, 0, heading))
		case len(para) > 0 && (strings.Trim(trimmed, "=") == "" || strings.Trim(trimmed, "-") == "") && len(trimmed) >= 2:
			// A line of = or - under a paragraph makes it a heading.
			level := 1
			if trimmed[0] == '-' {
				level = 2
			}
			heading := newFlowText(parseInline(strings.Join(para, " ")), headingSizes[level-1], colText)
			heading.bold = true
			para = nil
			out = append(out, padded(6, 0, 0, 0, heading))
		case ruleRow.MatchString(line):
			rule := canvas.NewRectangle(colBorder)
			rule.SetMinSize(fyne.NewSize(0, 1))
			out = append(out, padded(4, 4, 0, 0, rule))
		case strings.HasPrefix(trimmed, ">"):
			flush()
			var quoted []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"), " "))
			}
			i--
			bar := canvas.NewRectangle(colBorder)
			bar.SetMinSize(fyne.NewSize(3, 0))
			bar.CornerRadius = 1.5
			body := newFlowText(parseInline(strings.Join(quoted, "\n")), size, colMuted)
			out = append(out, container.NewBorder(nil, nil, bar, nil, padded(1, 1, 12, 0, body)))
		case listItem.MatchString(line):
			flush()
			start := i
			for i+1 < len(lines) {
				next := lines[i+1]
				if listItem.MatchString(next) || strings.TrimSpace(next) == "" || strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") {
					i++
					continue
				}
				break
			}
			out = append(out, renderList(lines[start:i+1], size))
		default:
			para = append(para, line)
		}
	}
	flush()
	if len(out) == 1 {
		return out[0]
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(12), out...)
}

// renderList lays a list out: bullets or numbers in a margin, items that
// wrap under themselves, sublists indented under their item.
func renderList(lines []string, size float32) fyne.CanvasObject {
	type item struct {
		level  int
		marker string
		text   []string
	}
	var items []item
	var indents []int // the indentation of each open level
	for _, line := range lines {
		found := listItem.FindStringSubmatch(line)
		if found == nil {
			if len(items) > 0 && strings.TrimSpace(line) != "" { // an item going on
				items[len(items)-1].text = append(items[len(items)-1].text, strings.TrimSpace(line))
			}
			continue
		}
		indent := len(strings.ReplaceAll(found[1], "\t", "    "))
		for len(indents) > 0 && indent < indents[len(indents)-1] {
			indents = indents[:len(indents)-1]
		}
		if len(indents) == 0 || indent > indents[len(indents)-1]+1 {
			indents = append(indents, indent)
		}
		level := len(indents) - 1
		marker, content := found[2], found[3]
		switch {
		case strings.HasPrefix(content, "[ ] "):
			marker, content = "☐", content[4:]
		case strings.HasPrefix(content, "[x] "), strings.HasPrefix(content, "[X] "):
			marker, content = "☑", content[4:]
		case len(marker) == 1: // a bullet
			marker = [...]string{"•", "◦", "▪"}[min(level, 2)]
		default:
			if _, err := strconv.Atoi(marker[:len(marker)-1]); err == nil {
				marker = marker[:len(marker)-1] + "."
			}
		}
		items = append(items, item{level, marker, []string{content}})
	}
	box := container.New(layout.NewCustomPaddedVBoxLayout(6))
	for n, itemValue := range items {
		text := strings.TrimSpace(strings.Join(itemValue.text, "\n"))
		if text == "" {
			continue // a bullet whose text is not written yet
		}
		entry := newFlowText(parseInline(text), size, colText)
		entry.marker, entry.hang, entry.tail = itemValue.marker, 24, "\n"
		if w, _, _ := measureText(itemValue.marker, size, fyne.TextStyle{}); w+10 > entry.hang {
			entry.hang = w + 10 // a long number
		}
		if n == len(items)-1 {
			entry.tail = "\n\n"
		}
		box.Add(padded(0, 0, float32(itemValue.level)*20, 0, entry))
	}
	return box
}

// markdownCode frames a fenced block with its language and a copy button.
func markdownCode(lang, text string) fyne.CanvasObject {
	var spans []span
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			spans = append(spans, span{hard: true})
		}
		spans = append(spans, span{text: line})
	}
	code := newFlowText(spans, 13, colText)
	code.mono = true
	scroll := container.NewHScroll(code)
	if lang == "" {
		lang = "code"
	}
	var copyButton *tapRow
	copyButton = newRow("Copy", iconCopy, styleChip, func() {
		fyne.CurrentApp().Clipboard().SetContent(text)
		copyButton.SetText("Copied")
		time.AfterFunc(1400*time.Millisecond, func() { fyne.Do(func() { copyButton.SetText("Copy") }) })
	})
	header := container.NewBorder(nil, nil, padded(0, 0, 10, 0, textLine(lang, 12, colMuted)), copyButton)
	rule := canvas.NewRectangle(colBorderSoft)
	rule.SetMinSize(fyne.NewSize(0, 1))
	body := container.NewBorder(container.NewVBox(padded(2, 2, 0, 2, header), rule), nil, nil, nil, padded(10, 12, 12, 12, scroll))
	return surface(body, colCode, colBorderSoft, 10, 0)
}

// ---------------------------------------------------------------------------
// markdownTable: a real grid with a header row, row rules and wrapping cells.

type markdownTable struct {
	widget.BaseWidget
	cells   [][]*flowText
	weights []float32
	minimum []float32 // widest single word per column: never broken
	width   float32   // last laid out width, used to size wrapped rows
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// selPieces gives the selection layer the cells' text, row by row, as
// tab-separated columns.
func (table *markdownTable) selPieces() []selPiece {
	var pieces []selPiece
	for _, row := range table.cells {
		for _, cell := range row {
			for _, piece := range cell.selPieces() {
				piece.at = piece.at.Add(cell.Position())
				pieces = append(pieces, piece)
			}
		}
	}
	return pieces
}

// linkAt and Tapped pass on to the cell under the pointer.
func (table *markdownTable) linkAt(at fyne.Position) string {
	for _, row := range table.cells {
		for _, cell := range row {
			if link := cell.linkAt(at.Subtract(cell.Position())); link != "" {
				return link
			}
		}
	}
	return ""
}

func (table *markdownTable) Tapped(e *fyne.PointEvent) { openLink(table.linkAt(e.Position)) }

func newMarkdownTable(text string) *markdownTable {
	table := &markdownTable{width: 600}
	rows := strings.Split(text, "\n")
	columns := len(splitRow(rows[0]))
	table.weights = make([]float32, columns)
	table.minimum = make([]float32, columns)
	for r, line := range rows {
		cells := splitRow(line)
		var row []*flowText
		for c := 0; c < columns; c++ {
			cell := ""
			if c < len(cells) {
				cell = cells[c]
			}
			flow := newFlowText(parseInline(cell), theme.TextSize(), colText)
			flow.bold = r == 0 // the header row
			flow.tail = "\t"
			if c == columns-1 {
				flow.tail = "\n"
			}
			row = append(row, flow)
			// A little slack, so a word that fits is never pushed to a new line.
			natural, widest := flow.extent()
			table.weights[c] = fyne.Max(table.weights[c], fyne.Min(natural+4, 320))
			table.minimum[c] = fyne.Max(table.minimum[c], fyne.Min(widest, 320)+4)
		}
		table.cells = append(table.cells, row)
	}
	table.ExtendBaseWidget(table)
	return table
}

func (table *markdownTable) CreateRenderer() fyne.WidgetRenderer {
	renderer := &tableRenderer{table: table, frame: canvas.NewRectangle(colCode), head: canvas.NewRectangle(colRaised)}
	renderer.frame.CornerRadius = 10
	renderer.frame.StrokeColor = colBorder
	renderer.frame.StrokeWidth = 1
	renderer.head.CornerRadius = 9
	for range table.cells[1:] {
		rule := canvas.NewRectangle(colBorderSoft)
		renderer.rules = append(renderer.rules, rule)
	}
	return renderer
}

type tableRenderer struct {
	table *markdownTable
	frame *canvas.Rectangle
	head  *canvas.Rectangle
	rules []*canvas.Rectangle
	// Fyne asks every object for its MinSize on every frame; measuring the
	// table lays out each cell, so the height is kept for its width.
	measuredAt float32
	height     float32
}

const cellPadX, cellPadY = float32(12), float32(8)

func (renderer *tableRenderer) columns(width float32) []float32 {
	total := float32(0)
	for _, w := range renderer.table.weights {
		total += w + 2*cellPadX
	}
	widths := make([]float32, len(renderer.table.weights))
	chrome := 2 * cellPadX
	for i, w := range renderer.table.weights {
		natural := w + chrome
		if total <= width {
			widths[i] = natural
		} else {
			widths[i] = fyne.Max(width*natural/total, renderer.table.minimum[i]+chrome)
		}
	}
	return widths
}

func (renderer *tableRenderer) rowHeights(width float32) []float32 {
	widths := renderer.columns(width)
	heights := make([]float32, len(renderer.table.cells))
	for i, row := range renderer.table.cells {
		for c, cell := range row {
			cell.Resize(fyne.NewSize(widths[c]-2*cellPadX, 10))
			heights[i] = fyne.Max(heights[i], cell.MinSize().Height+2*cellPadY)
		}
	}
	return heights
}

func (renderer *tableRenderer) MinSize() fyne.Size {
	if renderer.height == 0 || renderer.measuredAt != renderer.table.width {
		renderer.measuredAt, renderer.height = renderer.table.width, 0
		for _, h := range renderer.rowHeights(renderer.table.width) {
			renderer.height += h
		}
	}
	return fyne.NewSize(0, renderer.height)
}

func (renderer *tableRenderer) Layout(size fyne.Size) {
	renderer.table.width = size.Width
	widths := renderer.columns(size.Width)
	heights := renderer.rowHeights(size.Width)
	tableWidth := float32(0)
	for _, w := range widths {
		tableWidth += w
	}
	y := float32(0)
	for i, row := range renderer.table.cells {
		x := float32(0)
		for c, cell := range row {
			cell.Move(fyne.NewPos(x+cellPadX, y+cellPadY))
			cell.Resize(fyne.NewSize(widths[c]-2*cellPadX, heights[i]-2*cellPadY))
			x += widths[c]
		}
		if i > 0 {
			renderer.rules[i-1].Move(fyne.NewPos(0, y))
			renderer.rules[i-1].Resize(fyne.NewSize(tableWidth, 1))
		}
		y += heights[i]
	}
	renderer.frame.Resize(fyne.NewSize(tableWidth, y))
	renderer.head.Move(fyne.NewPos(1, 1))
	renderer.head.Resize(fyne.NewSize(tableWidth-2, heights[0]-1))
}

func (renderer *tableRenderer) Refresh() {
	renderer.Layout(renderer.table.Size())
	renderer.frame.Refresh()
	renderer.head.Refresh()
	for _, row := range renderer.table.cells {
		for _, cell := range row {
			cell.Refresh()
		}
	}
}

func (renderer *tableRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{renderer.frame, renderer.head}
	for _, rule := range renderer.rules {
		objects = append(objects, rule)
	}
	for _, row := range renderer.table.cells {
		for _, cell := range row {
			objects = append(objects, cell)
		}
	}
	return objects
}

func (renderer *tableRenderer) Destroy() {}
