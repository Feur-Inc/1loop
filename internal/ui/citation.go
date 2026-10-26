package ui

// A citation's card (the cite tool): the file and its lines under the tool
// row, a few of them shown; Open shows the file in the Files tab with the
// cited lines marked.

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/agent"
)

const citationCardLines = 12

func (ui *desktopUI) showCitation(box *fyne.Container, item agent.ChatItem) {
	citation, ok := agent.CitationOf(item)
	if !ok {
		box.RemoveAll()
		return
	}
	lines := fmt.Sprintf("lines %d–%d", citation.Start, citation.End)
	from, to := citation.Start, citation.End
	if citation.Start == 1 && citation.End == citation.Total {
		lines = fmt.Sprintf("whole file · %d lines", citation.Total)
		from, to = 0, 0 // nothing to mark
	}
	title := textLine(citation.Path, 12.5, colText)
	title.TextStyle = fyne.TextStyle{Monospace: true}
	open := newRow("Open", iconDocument, styleChip, func() { ui.panel.openFileAt(citation.Path, from, to) })
	header := padded(6, 6, 12, 6, container.NewBorder(nil, nil, nil, open,
		container.NewHBox(title, textLine("· "+lines, 12, colMuted))))
	shown := citation.Code
	if len(shown) > citationCardLines {
		shown = shown[:citationCardLines]
	}
	rows, width := codeLines(citation.Path, shown, citation.Start)
	code := container.NewVBox()
	for _, line := range rows {
		row := newDiffRow(width)
		row.line = line
		code.Add(row)
	}
	parts := []fyne.CanvasObject{header, container.NewHScroll(code)}
	if more := citation.End - citation.Start + 1 - len(shown); more > 0 {
		parts = append(parts, padded(6, 8, 12, 12, textLine(fmt.Sprintf("… %d more lines: Open shows them all", more), 12, colFaint)))
	}
	box.Objects = []fyne.CanvasObject{padded(4, 2, 30, 0, surface(container.NewVBox(parts...), colCode, colBorderSoft, 10, 0))}
	box.Refresh()
}
