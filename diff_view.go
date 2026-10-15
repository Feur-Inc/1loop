//go:build !server

package main

import (
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

type diffLine struct {
	kind           byte // f: file, h: hunk, space: context, + or -: change, m: metadata
	text           string
	old, next      int
	added, removed int
	tokens         []chroma.Token
}

var diffHunk = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func patchPath(raw string) string {
	raw = strings.SplitN(raw, "\t", 2)[0]
	if unquoted, err := strconv.Unquote(raw); err == nil {
		raw = unquoted
	}
	return strings.TrimPrefix(strings.TrimPrefix(raw, "a/"), "b/")
}

func parseDiff(patch string) []diffLine {
	var rows []diffLine
	file := -1
	old, next, oldLeft, newLeft, previous := 0, 0, 0, 0, 1
	oldPath := ""
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			name := strings.TrimPrefix(line, "diff --git ")
			if _, after, ok := strings.Cut(name, " b/"); ok {
				name = after
			}
			file = len(rows)
			rows = append(rows, diffLine{kind: 'f', text: name})
			oldLeft, newLeft, previous = 0, 0, 1
		case strings.HasPrefix(line, "--- ") && oldLeft == 0 && newLeft == 0:
			oldPath = patchPath(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ ") && oldLeft == 0 && newLeft == 0:
			name := patchPath(strings.TrimPrefix(line, "+++ "))
			if name == "/dev/null" {
				name = oldPath
			}
			if file < 0 {
				file = len(rows)
				rows = append(rows, diffLine{kind: 'f'})
			}
			rows[file].text = name
		case diffHunk.MatchString(line):
			m := diffHunk.FindStringSubmatch(line)
			old, _ = strconv.Atoi(m[1])
			next, _ = strconv.Atoi(m[3])
			oldLeft, newLeft = 1, 1
			if m[2] != "" {
				oldLeft, _ = strconv.Atoi(m[2])
			}
			if m[4] != "" {
				newLeft, _ = strconv.Atoi(m[4])
			}
			label := line
			if skipped := next - previous; skipped > 0 {
				label = fmt.Sprintf("%d unchanged lines · %s", skipped, line)
			}
			rows = append(rows, diffLine{kind: 'h', text: label})
		case (oldLeft > 0 || newLeft > 0) && len(line) > 0 && strings.ContainsRune(" +-", rune(line[0])):
			row := diffLine{kind: line[0], text: expandTabs(line[1:])}
			if row.kind != '+' {
				row.old = old
				old++
				oldLeft--
			}
			if row.kind != '-' {
				row.next = next
				next++
				newLeft--
				previous = next
			}
			if file >= 0 {
				if row.kind == '+' {
					rows[file].added++
				}
				if row.kind == '-' {
					rows[file].removed++
				}
			}
			rows = append(rows, row)
		case strings.HasPrefix(line, "index "):
		case line != "":
			rows = append(rows, diffLine{kind: 'm', text: line})
		}
	}
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].kind != 'f' {
			end++
		}
		if rows[start].kind == 'f' {
			highlightDiff(rows[start:end])
		}
		start = end
	}
	return rows
}

// Tokenise each side as a stream so multiline strings and comments retain
// their colours across adjacent lines, including deleted lines.
func highlightDiff(rows []diffLine) {
	lexer := lexers.Match(rows[0].text)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	for _, deleted := range []bool{true, false} {
		var indices []int
		var source strings.Builder
		for i, row := range rows {
			if row.kind == ' ' || row.kind == '+' && !deleted || row.kind == '-' && deleted {
				indices = append(indices, i)
				source.WriteString(row.text + "\n")
			}
		}
		iterator, err := lexer.Tokenise(nil, source.String())
		if err != nil {
			continue
		}
		lines := chroma.SplitTokensIntoLines(iterator.Tokens())
		for i, tokens := range lines {
			if i >= len(indices) {
				break
			}
			rows[indices[i]].tokens = tokens
		}
	}
}

func newDiffView(patch string) fyne.CanvasObject {
	rows := parseDiff(patch)
	width := float32(320)
	files := 0
	for _, row := range rows {
		if row.kind == 'f' {
			files++
		}
		if row.kind == ' ' || row.kind == '+' || row.kind == '-' {
			width = fyne.Max(width, float32(utf8.RuneCountInString(row.text))*outputCell().Width+110)
		}
	}
	list := widget.NewList(func() int { return len(rows) }, func() fyne.CanvasObject {
		return newDiffRow(width)
	}, func(i widget.ListItemID, object fyne.CanvasObject) {
		diffRowValue := object.(*diffRow)
		diffRowValue.line = rows[i]
		diffRowValue.Refresh()
	})
	list.HideSeparators = true
	for i, row := range rows {
		if row.kind == 'f' {
			list.SetItemHeight(i, 42)
		}
		if row.kind == 'h' || row.kind == 'm' {
			list.SetItemHeight(i, 30)
		}
	}
	copy := newRow("Copy diff", iconCopy, styleChip, func() { fyne.CurrentApp().Clipboard().SetContent(patch) })
	header := padded(6, 6, 12, 8, container.NewBorder(nil, nil, textLine(fmt.Sprintf("%d file(s)", files), 12, colMuted), copy))
	return container.NewBorder(header, nil, nil, nil, container.NewHScroll(list))
}

type diffRow struct {
	widget.BaseWidget
	line  diffLine
	width float32
}

func newDiffRow(width float32) *diffRow {
	row := &diffRow{width: width}
	row.ExtendBaseWidget(row)
	return row
}

type diffRowRenderer struct {
	row       *diffRow
	bg, mark  *canvas.Rectangle
	old, next *canvas.Text
	code      []*canvas.Text
}

func (row *diffRow) CreateRenderer() fyne.WidgetRenderer {
	old, next := textLine("", outputTextSize, colFaint), textLine("", outputTextSize, colFaint)
	old.TextStyle.Monospace, next.TextStyle.Monospace = true, true
	return &diffRowRenderer{row: row, bg: canvas.NewRectangle(colCode), mark: canvas.NewRectangle(color.Transparent), old: old, next: next}
}

func (renderer *diffRowRenderer) MinSize() fyne.Size { return fyne.NewSize(renderer.row.width, 24) }
func (renderer *diffRowRenderer) Destroy()           {}
func (renderer *diffRowRenderer) Objects() []fyne.CanvasObject {
	o := []fyne.CanvasObject{renderer.bg, renderer.mark, renderer.old, renderer.next}
	for _, text := range renderer.code {
		o = append(o, text)
	}
	return o
}
func (renderer *diffRowRenderer) Layout(size fyne.Size) {
	renderer.bg.Resize(size)
	renderer.mark.Resize(fyne.NewSize(3, size.Height))
	y := (size.Height - renderer.old.MinSize().Height) / 2
	renderer.old.Move(fyne.NewPos(42-renderer.old.MinSize().Width, y))
	renderer.next.Move(fyne.NewPos(86-renderer.next.MinSize().Width, y))
	x := float32(102)
	if renderer.row.line.kind == 'f' || renderer.row.line.kind == 'h' || renderer.row.line.kind == 'm' {
		x = 14
	}
	for _, text := range renderer.code {
		if !text.Visible() {
			continue
		}
		text.Move(fyne.NewPos(x, (size.Height-text.MinSize().Height)/2))
		x += text.MinSize().Width
	}
}

func (renderer *diffRowRenderer) Refresh() {
	line := renderer.row.line
	renderer.bg.FillColor, renderer.mark.FillColor = colCode, color.Transparent
	renderer.old.Text, renderer.next.Text = "", ""
	renderer.old.Color, renderer.next.Color = colFaint, colFaint
	if line.old > 0 {
		renderer.old.Text = strconv.Itoa(line.old)
	}
	if line.next > 0 {
		renderer.next.Text = strconv.Itoa(line.next)
	}
	switch line.kind {
	case '+':
		renderer.bg.FillColor = color.NRGBA{R: 0x1c, G: 0x33, B: 0x23, A: 0xff}
		renderer.mark.FillColor, renderer.next.Color = colAdded, colAdded
	case '-':
		renderer.bg.FillColor = color.NRGBA{R: 0x40, G: 0x21, B: 0x1d, A: 0xff}
		renderer.mark.FillColor, renderer.old.Color = colDanger, colDanger
	case 'f':
		renderer.bg.FillColor = colSurface
	case 'h', 'm':
		renderer.bg.FillColor = colRaised
	}
	tokens := line.tokens
	if len(tokens) == 0 {
		tokens = []chroma.Token{{Type: chroma.Text, Value: line.text}}
	}
	if line.kind == 'f' {
		tokens = []chroma.Token{{Type: chroma.Text, Value: line.text + "   "},
			{Type: chroma.GenericInserted, Value: fmt.Sprintf("+%d  ", line.added)},
			{Type: chroma.GenericDeleted, Value: fmt.Sprintf("−%d", line.removed)}}
	}
	for len(renderer.code) < len(tokens) {
		renderer.code = append(renderer.code, textLine("", outputTextSize, colText))
	}
	style := styles.Get("monokai")
	for i, text := range renderer.code {
		if i >= len(tokens) {
			text.Hide()
			continue
		}
		text.Text = strings.TrimRight(tokens[i].Value, "\r\n")
		text.TextStyle = fyne.TextStyle{Monospace: line.kind == ' ' || line.kind == '+' || line.kind == '-', Bold: line.kind == 'f'}
		text.Color = colText
		if line.kind == 'f' && tokens[i].Type == chroma.GenericInserted {
			text.Color = colAdded
		} else if line.kind == 'f' && tokens[i].Type == chroma.GenericDeleted {
			text.Color = colDanger
		} else if line.kind == 'h' || line.kind == 'm' {
			text.Color = colMuted
		} else if c := style.Get(tokens[i].Type).Colour; c.IsSet() && line.kind != 'f' {
			text.Color = color.NRGBA{R: c.Red(), G: c.Green(), B: c.Blue(), A: 0xff}
		}
		text.Show()
		text.Refresh()
	}
	renderer.bg.Refresh()
	renderer.mark.Refresh()
	renderer.old.Refresh()
	renderer.next.Refresh()
	renderer.Layout(renderer.row.Size())
}
