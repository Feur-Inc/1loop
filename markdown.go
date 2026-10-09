package main

import (
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// markdownView renders a model answer. Fyne's RichText handles paragraphs,
// lists and inline styles but not tables, and shows fenced code without a
// frame; those two block kinds get their own widgets here.
type markdownView struct {
	box    *fyne.Container
	blocks []mdBlock
	objs   []fyne.CanvasObject
}

type mdBlock struct {
	kind string // "text", "code", "table"
	lang string
	text string
}

func newMarkdownView(text string) *markdownView {
	m := &markdownView{box: container.New(layout.NewCustomPaddedVBoxLayout(10))}
	m.ParseMarkdown(text)
	return m
}

// ParseMarkdown re-renders the text. Unchanged blocks keep their widgets and
// the growing text block is updated in place, so streaming neither flickers
// nor redraws the blocks that are already complete.
func (m *markdownView) ParseMarkdown(text string) {
	blocks := splitMarkdown(text)
	objs := make([]fyne.CanvasObject, len(blocks))
	reshaped := len(blocks) != len(m.blocks)
	for i, b := range blocks {
		if i < len(m.blocks) && m.blocks[i] == b {
			objs[i] = m.objs[i]
			continue
		}
		if i < len(m.blocks) && b.kind == "text" && m.blocks[i].kind == "text" {
			if rich, ok := m.objs[i].(*widget.RichText); ok {
				rich.ParseMarkdown(b.text) // refreshes itself
				objs[i] = rich
				continue
			}
		}
		objs[i] = renderBlock(b)
		reshaped = true
	}
	m.blocks, m.objs = blocks, objs
	if reshaped {
		// New widgets are drawn on their own once placed: lay the box out,
		// without the recursive refresh of Container.Refresh.
		m.box.Objects = objs
		m.box.Layout.Layout(objs, m.box.Size())
		canvas.Refresh(m.box)
	}
}

func renderBlock(b mdBlock) fyne.CanvasObject {
	switch b.kind {
	case "code":
		return markdownCode(b.lang, b.text)
	case "table":
		return newMarkdownTable(b.text)
	}
	rich := widget.NewRichTextFromMarkdown(b.text)
	rich.Wrapping = fyne.TextWrapWord
	return rich
}

var tableRule = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)

// splitMarkdown cuts the text into text, fenced code and table blocks. An
// unterminated fence (still streaming) is treated as code up to the end.
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
		if strings.HasPrefix(trimmed, "```") {
			flush()
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			blocks = append(blocks, mdBlock{kind: "code", lang: lang, text: strings.Join(code, "\n")})
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

var listItem = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)

// paragraphs splits plain markdown at blank lines and before headings, so
// the view can space blocks like a web page; a list stays in one piece.
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
			if !(listItem.MatchString(lastContent) && listItem.MatchString(next)) {
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

// markdownCode frames a fenced block with its language and a copy button.
func markdownCode(lang, text string) fyne.CanvasObject {
	label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	label.Selectable = true
	scroll := container.NewHScroll(label)
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
	body := container.NewBorder(container.NewVBox(padded(2, 2, 0, 2, header), rule), nil, nil, nil, padded(2, 4, 4, 4, scroll))
	return surface(body, colCode, colBorderSoft, 10, 0)
}

// ---------------------------------------------------------------------------
// markdownTable: a real grid with a header row, row rules and wrapping cells.

type markdownTable struct {
	widget.BaseWidget
	cells   [][]*widget.RichText
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

// cellWidth measures a cell as it renders: inline code is monospace.
func cellWidth(cell string, bold bool) float32 {
	width := float32(0)
	for i, part := range strings.Split(strings.ReplaceAll(cell, "**", ""), "`") {
		style := fyne.TextStyle{Monospace: i%2 == 1, Bold: bold && i%2 == 0}
		width += fyne.MeasureText(part, theme.TextSize(), style).Width
	}
	return width
}

func longestWord(cell string, bold bool) float32 {
	longest := float32(0)
	for _, word := range strings.Fields(cell) {
		longest = fyne.Max(longest, cellWidth(word, bold))
	}
	return longest
}

func newMarkdownTable(text string) *markdownTable {
	t := &markdownTable{width: 600}
	rows := strings.Split(text, "\n")
	columns := len(splitRow(rows[0]))
	t.weights = make([]float32, columns)
	t.minimum = make([]float32, columns)
	for r, line := range rows {
		cells := splitRow(line)
		var row []*widget.RichText
		for c := 0; c < columns; c++ {
			cell := ""
			if c < len(cells) {
				cell = cells[c]
			}
			if r == 0 && cell != "" {
				cell = "**" + cell + "**"
			}
			rich := widget.NewRichTextFromMarkdown(cell)
			rich.Wrapping = fyne.TextWrapWord
			row = append(row, rich)
			// Header cells render bold, which is wider: measure them that way,
			// with a little slack so a fitting word is never pushed to a new line.
			t.weights[c] = fyne.Max(t.weights[c], fyne.Min(cellWidth(cell, r == 0)+4, 320))
			t.minimum[c] = fyne.Max(t.minimum[c], longestWord(cell, r == 0)+4)
		}
		t.cells = append(t.cells, row)
	}
	t.ExtendBaseWidget(t)
	return t
}

func (t *markdownTable) CreateRenderer() fyne.WidgetRenderer {
	r := &tableRenderer{t: t, frame: canvas.NewRectangle(colCode), head: canvas.NewRectangle(colRaised)}
	r.frame.CornerRadius = 10
	r.frame.StrokeColor = colBorder
	r.frame.StrokeWidth = 1
	r.head.CornerRadius = 9
	for range t.cells[1:] {
		rule := canvas.NewRectangle(colBorderSoft)
		r.rules = append(r.rules, rule)
	}
	return r
}

type tableRenderer struct {
	t     *markdownTable
	frame *canvas.Rectangle
	head  *canvas.Rectangle
	rules []*canvas.Rectangle
	// Fyne asks every object for its MinSize on every frame; measuring the
	// table lays out each cell, so the height is kept for its width.
	measuredAt float32
	height     float32
}

const cellPadX, cellPadY = float32(6), float32(2)

func (r *tableRenderer) columns(width float32) []float32 {
	total := float32(0)
	for _, w := range r.t.weights {
		total += w + 2*cellPadX + 2*theme.InnerPadding()
	}
	widths := make([]float32, len(r.t.weights))
	chrome := 2*cellPadX + 2*theme.InnerPadding()
	for i, w := range r.t.weights {
		natural := w + chrome
		if total <= width {
			widths[i] = natural
		} else {
			widths[i] = fyne.Max(width*natural/total, r.t.minimum[i]+chrome)
		}
	}
	return widths
}

func (r *tableRenderer) rowHeights(width float32) []float32 {
	widths := r.columns(width)
	heights := make([]float32, len(r.t.cells))
	for i, row := range r.t.cells {
		for c, cell := range row {
			cell.Resize(fyne.NewSize(widths[c]-2*cellPadX, 10))
			heights[i] = fyne.Max(heights[i], cell.MinSize().Height+2*cellPadY)
		}
	}
	return heights
}

func (r *tableRenderer) MinSize() fyne.Size {
	if r.height == 0 || r.measuredAt != r.t.width {
		r.measuredAt, r.height = r.t.width, 0
		for _, h := range r.rowHeights(r.t.width) {
			r.height += h
		}
	}
	return fyne.NewSize(0, r.height)
}

func (r *tableRenderer) Layout(size fyne.Size) {
	r.t.width = size.Width
	widths := r.columns(size.Width)
	heights := r.rowHeights(size.Width)
	tableWidth := float32(0)
	for _, w := range widths {
		tableWidth += w
	}
	y := float32(0)
	for i, row := range r.t.cells {
		x := float32(0)
		for c, cell := range row {
			cell.Move(fyne.NewPos(x+cellPadX, y+cellPadY))
			cell.Resize(fyne.NewSize(widths[c]-2*cellPadX, heights[i]-2*cellPadY))
			x += widths[c]
		}
		if i > 0 {
			r.rules[i-1].Move(fyne.NewPos(0, y))
			r.rules[i-1].Resize(fyne.NewSize(tableWidth, 1))
		}
		y += heights[i]
	}
	r.frame.Resize(fyne.NewSize(tableWidth, y))
	r.head.Move(fyne.NewPos(1, 1))
	r.head.Resize(fyne.NewSize(tableWidth-2, heights[0]-1))
}

func (r *tableRenderer) Refresh() {
	r.Layout(r.t.Size())
	r.frame.Refresh()
	r.head.Refresh()
	for _, row := range r.t.cells {
		for _, cell := range row {
			cell.Refresh()
		}
	}
}

func (r *tableRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{r.frame, r.head}
	for _, rule := range r.rules {
		objects = append(objects, rule)
	}
	for _, row := range r.t.cells {
		for _, cell := range row {
			objects = append(objects, cell)
		}
	}
	return objects
}

func (r *tableRenderer) Destroy() {}
