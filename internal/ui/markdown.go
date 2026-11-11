// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// markdownView renders a model answer. goldmark parses it (mdinline.go);
// each top-level block (a paragraph, a heading, a list, a quote, code, a
// table, a formula) becomes widgets, and every run of text is laid out by
// flowText, which draws the inline styles and knows where each word is,
// for links and selection.
type markdownView struct {
	layer  *selectLayer // over the box: selects its text with the mouse
	box    *fyne.Container
	blocks []mdBlock
	objs   []fyne.CanvasObject
}

// mdBlock identifies a top-level block: while an answer streams, a block
// whose source did not change keeps its widgets.
type mdBlock struct {
	kind string // "text", "code", "table", "math"
	lang string // math: "open" while its end is not written yet
	text string // its source
}

func newMarkdownView(text string) *markdownView {
	view := &markdownView{box: container.New(layout.NewCustomPaddedVBoxLayout(12))}
	view.ParseMarkdown(text)
	return view
}

// ParseMarkdown re-renders the text. Unchanged blocks keep their widgets:
// while an answer streams, only its last block is built again.
func (view *markdownView) ParseMarkdown(text string) {
	source := []byte(hideOpenTag(text))
	nodes, blocks := splitMarkdown(source)
	objs := make([]fyne.CanvasObject, len(blocks))
	reshaped := len(blocks) != len(view.blocks)
	for i, block := range blocks {
		if i < len(view.blocks) && view.blocks[i] == block {
			objs[i] = view.objs[i]
			continue
		}
		objs[i] = renderNode(nodes[i], source, colText)
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

// hideOpenTag cuts a citation tag still arriving at the end of a streamed
// answer: it shows once whole.
func hideOpenTag(text string) string {
	if at := strings.LastIndex(text, "<cite"); at >= 0 && !strings.Contains(text[at:], ">") {
		return text[:at]
	}
	return text
}

// splitMarkdown parses the text and gives its top-level blocks, each with
// its source.
func splitMarkdown(source []byte) ([]ast.Node, []mdBlock) {
	var nodes []ast.Node
	for node := parseMarkdown(source).FirstChild(); node != nil; node = node.NextSibling() {
		nodes = append(nodes, node)
	}
	starts := make([]int, len(nodes)+1)
	starts[len(nodes)] = len(source)
	for i, node := range nodes {
		starts[i] = blockStart(node)
		if i > 0 && starts[i] < starts[i-1] {
			starts[i] = starts[i-1] // unknown: right after the previous block
		}
		starts[i] = max(starts[i], 0)
	}
	blocks := make([]mdBlock, len(nodes))
	for i, node := range nodes {
		block := mdBlock{kind: "text", text: strings.TrimRight(string(source[starts[i]:max(starts[i], starts[i+1])]), " \t\n")}
		switch node := node.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			block.kind = "code"
		case *east.Table:
			block.kind = "table"
		case *mathBlock:
			block.kind = "math"
			if !node.closed {
				block.lang = "open"
			}
		}
		blocks[i] = block
	}
	return nodes, blocks
}

// blockStart is where a block starts in the source, or -1.
func blockStart(node ast.Node) int {
	if node.Pos() >= 0 {
		return node.Pos()
	}
	if node.Type() == ast.TypeBlock && node.Lines().Len() > 0 {
		return node.Lines().At(0).Start
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if at := blockStart(child); at >= 0 {
			return at
		}
	}
	return -1
}

var headingSizes = [...]float32{22, 19, 16.5, 15, 14, 14}

// renderNode lays one block out, its own blocks inside it top to bottom.
func renderNode(node ast.Node, source []byte, ink color.Color) fyne.CanvasObject {
	size := theme.TextSize()
	switch node := node.(type) {
	case *ast.Heading:
		heading := newFlowText(inlineSpans(node, source), headingSizes[min(max(node.Level, 1), 6)-1], ink)
		heading.bold = true
		return padded(6, 0, 0, 0, heading)
	case *ast.Paragraph, *ast.TextBlock:
		return newFlowText(inlineSpans(node, source), size, ink)
	case *ast.HTMLBlock:
		var raw strings.Builder
		for i := 0; i < node.Lines().Len(); i++ {
			line := node.Lines().At(i)
			raw.Write(line.Value(source))
		}
		return newFlowText(htmlSpans(raw.String()), size, ink)
	case *ast.ThematicBreak:
		rule := canvas.NewRectangle(colBorder)
		rule.SetMinSize(fyne.NewSize(0, 1))
		return padded(4, 4, 0, 0, rule)
	case *ast.Blockquote:
		bar := canvas.NewRectangle(colBorder)
		bar.SetMinSize(fyne.NewSize(3, 0))
		bar.CornerRadius = 1.5
		return container.NewBorder(nil, nil, bar, nil, padded(1, 1, 12, 0, renderChildren(node, source, colMuted)))
	case *ast.List:
		var last *flowText
		list := renderList(node, source, 0, ink, &last)
		if last != nil {
			last.tail = "\n\n"
		}
		return list
	case *ast.FencedCodeBlock:
		return markdownCode(string(node.Language(source)), codeBlockText(node, source))
	case *ast.CodeBlock:
		return markdownCode("", codeBlockText(node, source))
	case *east.Table:
		return newMarkdownTable(tableSpans(node, source))
	case *mathBlock:
		if !node.closed { // still being written: not worth composing yet
			return markdownCode("latex", node.formula())
		}
		formula := newFlowText([]span{{math: node.formula(), text: node.formula()}}, size, colText)
		formula.center = true
		return padded(4, 4, 0, 0, formula)
	}
	return renderChildren(node, source, ink)
}

// renderChildren stacks a container block's blocks.
func renderChildren(node ast.Node, source []byte, ink color.Color) fyne.CanvasObject {
	var out []fyne.CanvasObject
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		out = append(out, renderNode(child, source, ink))
	}
	if len(out) == 1 {
		return out[0]
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(12), out...)
}

// renderList lays a list out: bullets or numbers in a margin, items that
// wrap under themselves, sublists and the items' other blocks indented
// under their item. last is the list's last line of text, which ends it
// in copied text.
func renderList(list *ast.List, source []byte, level int, ink color.Color, last **flowText) fyne.CanvasObject {
	size := theme.TextSize()
	box := container.New(layout.NewCustomPaddedVBoxLayout(6))
	indent := float32(level) * 20
	number := list.Start
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := [...]string{"•", "◦", "▪"}[min(level, 2)]
		if list.IsOrdered() {
			marker = strconv.Itoa(number) + "."
			number++
		}
		child := item.FirstChild()
		switch child.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			if box, ok := child.FirstChild().(*east.TaskCheckBox); ok {
				marker = "☐"
				if box.IsChecked {
					marker = "☑"
				}
			}
			if spans := inlineSpans(child, source); len(spans) > 0 { // a bullet whose text is not written yet waits
				entry := newFlowText(spans, size, ink)
				entry.marker, entry.hang, entry.tail = marker, 24, "\n"
				if w, _, _ := measureText(marker, size, fyne.TextStyle{}); w+10 > entry.hang {
					entry.hang = w + 10 // a long number
				}
				box.Add(padded(0, 0, indent, 0, entry))
				*last = entry
			}
			child = child.NextSibling()
		}
		for ; child != nil; child = child.NextSibling() {
			if sublist, ok := child.(*ast.List); ok {
				box.Add(renderList(sublist, source, level+1, ink, last))
				continue
			}
			box.Add(padded(0, 0, indent+24, 0, renderNode(child, source, ink)))
			*last = nil
		}
	}
	return box
}

// codeBlockText is a code block's text.
func codeBlockText(node ast.Node, source []byte) string {
	var code strings.Builder
	for i := 0; i < node.Lines().Len(); i++ {
		line := node.Lines().At(i)
		code.Write(line.Value(source))
	}
	return strings.TrimSuffix(code.String(), "\n")
}

// tableSpans are a table's cells, row by row, the header first.
func tableSpans(table *east.Table, source []byte) [][][]span {
	var rows [][][]span
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		var cells [][]span
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, inlineSpans(cell, source))
		}
		rows = append(rows, cells)
	}
	return rows
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
	scroll := newSideScroll(code)
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

func newMarkdownTable(rows [][][]span) *markdownTable {
	table := &markdownTable{width: 600}
	columns := len(rows[0])
	table.weights = make([]float32, columns)
	table.minimum = make([]float32, columns)
	for r, cells := range rows {
		var row []*flowText
		for c := 0; c < columns; c++ {
			var cell []span
			if c < len(cells) {
				cell = cells[c]
			}
			flow := newFlowText(cell, theme.TextSize(), colText)
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
