// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
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
