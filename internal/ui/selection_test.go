// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/scrollbar"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// answer lays a markdown answer with its selection layer in a window, as the
// conversation does, and returns the layer with its text collected.
func answer(t *testing.T, ui *desktopUI, text string) (*selectLayer, fyne.CanvasObject) {
	t.Helper()
	view := newMarkdownView(text)
	view.layer = newSelectLayer(ui, view.box)
	return view.layer, container.NewStack(view.box, view.layer)
}

// pieceAt is where a piece of text starting with prefix is drawn in the layer.
func pieceAt(t *testing.T, layer *selectLayer, prefix string) selPiece {
	t.Helper()
	layer.gather()
	for _, piece := range layer.pieces {
		if strings.HasPrefix(string(piece.runes), prefix) {
			return piece
		}
	}
	t.Fatalf("no piece starts with %q", prefix)
	return selPiece{}
}

func press(layer *selectLayer, at fyne.Position) {
	layer.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary, PointEvent: fyne.PointEvent{Position: at,
		AbsolutePosition: fyne.CurrentApp().Driver().AbsolutePositionForObject(layer).Add(at)}})
}

func drag(layer *selectLayer, at fyne.Position) {
	layer.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at,
		AbsolutePosition: fyne.CurrentApp().Driver().AbsolutePositionForObject(layer).Add(at)}})
}

func TestSelectionGoesIntoCodeBlock(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: &desktopApp{}}
	ui.chatSel.ui = ui
	layer, content := answer(t, ui, "Run this first:\n\n```go\nfmt.Println(\"hello\")\nos.Exit(2)\n```\n\nThen look.")
	ui.transcript = container.NewVBox(content)
	window := test.NewWindow(ui.transcript)
	defer window.Close()
	window.Resize(fyne.NewSize(600, 400))

	// From the start of the prose into the middle of the code's first line.
	prose, code := pieceAt(t, layer, "Run this"), pieceAt(t, layer, "fmt.Println")
	press(layer, prose.at.AddXY(1, prose.size.Height/2))
	drag(layer, code.at.AddXY(code.width(len("fmt.Print")), code.size.Height/2))
	got := ui.chatSel.selectedText()
	if !strings.HasPrefix(got, "Run this first:") || !strings.HasSuffix(got, "fmt.Print") {
		t.Fatalf("prose into code selected %q", got)
	}

	// Inside the code block alone: part of its first line and the second.
	second := pieceAt(t, layer, "os.Exit")
	press(layer, code.at.AddXY(code.width(len("fmt.")), code.size.Height/2))
	drag(layer, second.at.AddXY(second.width(len("os.Exit")), second.size.Height/2))
	if got := ui.chatSel.selectedText(); got != "Println(\"hello\")\nos.Exit" {
		t.Fatalf("inside the code block selected %q", got)
	}
}

func TestSelectionAcrossMessages(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: &desktopApp{}}
	ui.chatSel.ui = ui
	first, a := answer(t, ui, "First answer, one line.")
	_, b := answer(t, ui, "Middle answer.")
	last, c := answer(t, ui, "Last answer, two words.")
	ui.transcript = container.NewVBox(a, b, c)
	window := test.NewWindow(ui.transcript)
	defer window.Close()
	window.Resize(fyne.NewSize(600, 400))

	start := pieceAt(t, first, "First")
	end := pieceAt(t, last, "Last")
	press(first, start.at.AddXY(start.width(len("First ")), start.size.Height/2))
	// The pointer is over the last message, but the drag stays the first's.
	target := fyne.CurrentApp().Driver().AbsolutePositionForObject(last).Add(end.at.AddXY(end.width(len("Last answer")), end.size.Height/2))
	first.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{
		Position:         target.Subtract(fyne.CurrentApp().Driver().AbsolutePositionForObject(first)),
		AbsolutePosition: target}})
	want := "answer, one line.\n\nMiddle answer.\n\nLast answer"
	if got := ui.chatSel.selectedText(); got != want {
		t.Fatalf("across messages selected %q, want %q", got, want)
	}

	// Back into the first message: the others let go.
	drag(first, start.at.AddXY(start.width(len("First answer")), start.size.Height/2))
	if got := ui.chatSel.selectedText(); got != "answer" {
		t.Fatalf("back in the first message selected %q", got)
	}
}

// Copying a table's cells gives each cell's text once, the cells of a row
// separated by tabs and the rows by line breaks, emoji and accents intact
// (#438).
func TestSelectionCopiesTableCells(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: &desktopApp{}}
	ui.chatSel.ui = ui
	const why = "Il n'y a de compositeur Wayland (weston, sway, cage) ni dans l'image ni sur la machine. On ne peut donc tester que X11."
	layer, content := answer(t, ui, "Before the table.\n\n"+
		"| Cible | État | Ce que j'ai vérifié |\n|---|---|---|\n"+
		"| Docker X11 | ✅ OK | L'image a Xvfb. |\n"+
		"| Docker Wayland | ❌ absent | "+why+" |\n"+
		"| Dev local | ✅ OK | Go 1.25.0 correspond. |\n\nAfter the table.")
	ui.transcript = container.NewVBox(content)
	window := test.NewWindow(ui.transcript)
	defer window.Close()
	window.Resize(fyne.NewSize(900, 600))

	// at is a point in the last piece starting with prefix, n runes in.
	at := func(prefix string, n int) fyne.Position {
		layer.gather()
		var piece selPiece
		for _, p := range layer.pieces {
			if strings.HasPrefix(string(p.runes), prefix) {
				piece = p
			}
		}
		if piece.runes == nil {
			t.Fatalf("no piece starts with %q", prefix)
		}
		if n < 0 {
			n = len(piece.runes)
		}
		return piece.at.AddXY(piece.width(n), piece.size.Height/2)
	}
	selects := func(what string, from, to fyne.Position, want string) {
		t.Helper()
		press(layer, from)
		drag(layer, to)
		got := ui.chatSel.selectedText()
		if got != want {
			t.Fatalf("%s copied %q, want %q", what, got, want)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("%s copied invalid UTF-8: %q", what, got)
		}
	}

	// The row of the issue, from its first cell's start to its last's end.
	selects("a row", at("Docker Wayland", 0), at("peut", -1),
		"Docker Wayland\t❌ absent\t"+why)
	// The state column, from its first cell to its last: the selection
	// follows the reading order, so it takes the cells between them.
	selects("a column", at("État", 0), at("✅", -1),
		"État\tCe que j'ai vérifié\nDocker X11\t✅ OK\tL'image a Xvfb.\n"+
			"Docker Wayland\t❌ absent\t"+why+"\nDev local\t✅ OK")
	// Part of the table and the text around it.
	selects("the table and its surroundings", at("Before", 7), at("After", 5),
		"the table.\n\nCible\tÉtat\tCe que j'ai vérifié\nDocker X11\t✅ OK\tL'image a Xvfb.\n"+
			"Docker Wayland\t❌ absent\t"+why+"\nDev local\t✅ OK\tGo 1.25.0 correspond.\nAfter")
}

// A code block's sideways bar lies under the answer's selection layer: the
// layer passes the press, the drag and a tap on it on to the bar, and
// selects nothing (#633).
func TestSelectionPassesTheCodeBarOn(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui := &desktopUI{desktopApp: &desktopApp{}}
	ui.chatSel.ui = ui
	line := "x := compute(" + strings.Repeat("argument, ", 40) + ")"
	layer, content := answer(t, ui, "Wide:\n\n```go\n"+line+"\n```")
	ui.transcript = container.NewVBox(content)
	window := test.NewWindow(ui.transcript)
	defer window.Close()
	window.Resize(fyne.NewSize(600, 400))

	layer.gather()
	var bar *selTap
	var code *sideScroll
	for i, tap := range layer.taps {
		if isScrollBar(tap.object) {
			bar = &layer.taps[i]
		}
	}
	collectScrolls(layer.under, &code)
	if bar == nil || code == nil {
		t.Fatal("the code block's bar is not among what the layer passes the pointer to")
	}
	at := bar.at.AddXY(scrollbar.Edge+scrollbar.MinThumb/2, bar.object.Size().Height/2) // on the thumb
	layer.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at}})
	if layer.Cursor() != desktop.DefaultCursor {
		t.Fatal("no plain pointer over the bar")
	}
	press(layer, at)
	layer.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at.AddXY(100, 0)}, Dragged: fyne.NewDelta(100, 0)})
	layer.DragEnd()
	layer.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary, PointEvent: fyne.PointEvent{Position: at.AddXY(100, 0)}})
	if code.Offset.X <= 0 {
		t.Fatal("dragging the code block's thumb did not scroll it")
	}
	if got := ui.chatSel.selectedText(); got != "" {
		t.Fatalf("dragging the bar selected %q", got)
	}
	before := code.Offset.X
	end := bar.at.AddXY(bar.object.Size().Width-scrollbar.Edge-2, bar.object.Size().Height/2)
	press(layer, end)
	layer.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary, PointEvent: fyne.PointEvent{Position: end}})
	if code.Offset.X <= before {
		t.Fatal("a tap on the code block's track did not page")
	}
}

func collectScrolls(object fyne.CanvasObject, found **sideScroll) {
	switch o := object.(type) {
	case *sideScroll:
		*found = o
	case *fyne.Container:
		for _, child := range o.Objects {
			collectScrolls(child, found)
		}
	}
}
