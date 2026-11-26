// SPDX-License-Identifier: MPL-2.0

package ui

// The gestures of issue #626 on real answers, with Fyne's test driver.

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// answers lays answers out as the transcript does, in a window, and
// returns their layers.
func answers(t *testing.T, width float32, answers ...string) (*desktopUI, []*selectLayer, fyne.Window) {
	t.Helper()
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: &desktopApp{}, views: map[int]any{}}
	ui.chatSel.ui = ui
	ui.transcript = container.NewVBox()
	var layers []*selectLayer
	for i, text := range answers {
		view := newMarkdownView(text)
		view.layer = newSelectLayer(ui, view.box)
		ui.views[i] = view
		layers = append(layers, view.layer)
		ui.transcript.Add(container.NewStack(view.box, view.layer))
	}
	window := test.NewWindow(ui.transcript)
	t.Cleanup(window.Close)
	window.Resize(fyne.NewSize(width, 600))
	ui.window = window
	return ui, layers, window
}

// runeAt is a point of layer over the rune n of the first piece holding
// what (n counted from the start of what).
func runeAt(t *testing.T, layer *selectLayer, what string, n int) fyne.Position {
	t.Helper()
	layer.gather()
	for _, piece := range layer.pieces {
		at := strings.Index(string(piece.runes), what)
		if at < 0 {
			continue
		}
		i := len([]rune(string(piece.runes)[:at])) + n
		left, right := piece.width(i), piece.width(i+1)
		return piece.at.AddXY((left+right)/2, piece.size.Height/2)
	}
	t.Fatalf("no piece holds %q", what)
	return fyne.Position{}
}

func mouseEvent(layer *selectLayer, at fyne.Position, button desktop.MouseButton, modifier fyne.KeyModifier) *desktop.MouseEvent {
	return &desktop.MouseEvent{Button: button, Modifier: modifier, PointEvent: fyne.PointEvent{Position: at,
		AbsolutePosition: fyne.CurrentApp().Driver().AbsolutePositionForObject(layer).Add(at)}}
}

// click presses and releases the button count times at a point, quickly.
func click(layer *selectLayer, at fyne.Position, count int, modifier fyne.KeyModifier) {
	for range count {
		layer.MouseDown(mouseEvent(layer, at, desktop.MouseButtonPrimary, modifier))
		layer.MouseUp(mouseEvent(layer, at, desktop.MouseButtonPrimary, modifier))
	}
}

func selected(t *testing.T, ui *desktopUI, what, want string) {
	t.Helper()
	if ui.selected == nil {
		t.Fatalf("%s: nothing selected, want %q", what, want)
	}
	if got := ui.selected.selectedText(); got != want {
		t.Fatalf("%s selected %q, want %q", what, got, want)
	}
}

func TestDoubleClickSelectsWordAcrossStyles(t *testing.T) {
	ui, layers, _ := answers(t, 600, "Some un**bold**ed text, a `foo`_bar and [li](https://example.com)nk word.")
	layer := layers[0]
	click(layer, runeAt(t, layer, "bold", 1), 2, 0)
	selected(t, ui, "a double click on a bold run", "unbolded")
	ui.chatSel.clicks.reset()
	click(layer, runeAt(t, layer, "nk word", 0), 2, 0)
	selected(t, ui, "a double click after a link", "link")
	ui.chatSel.clicks.reset()
	click(layer, runeAt(t, layer, "foo", 1), 2, 0)
	selected(t, ui, "a double click on code", "foo_bar")
}

func TestTripleClickSelectsParagraphAndCodeLine(t *testing.T) {
	ui, layers, _ := answers(t, 260,
		"A first paragraph, **long enough** to wrap on several lines in a narrow window.\n\n"+
			"- one item\n- second *item* here\n\n"+
			"```go\nfmt.Println(\"hello\")\nos.Exit(2)\n```\n\nThe end.")
	layer := layers[0]
	click(layer, runeAt(t, layer, "enough", 0), 3, 0)
	selected(t, ui, "a triple click on a wrapped paragraph",
		"A first paragraph, long enough to wrap on several lines in a narrow window.")
	ui.chatSel.clicks.reset()
	click(layer, runeAt(t, layer, "second", 2), 3, 0)
	selected(t, ui, "a triple click on a list item", "second item here")
	ui.chatSel.clicks.reset()
	click(layer, runeAt(t, layer, "os.Exit", 3), 3, 0)
	selected(t, ui, "a triple click on a line of code", "os.Exit(2)")
	// A fourth click starts over: a caret, nothing selected.
	click(layer, runeAt(t, layer, "os.Exit", 3), 1, 0)
	selected(t, ui, "a fourth click", "")
}

func TestTripleClickSelectsTableCell(t *testing.T) {
	ui, layers, _ := answers(t, 700, "| Name | Notes |\n|---|---|\n| one | the **first** cell |\n| two | other |")
	layer := layers[0]
	click(layer, runeAt(t, layer, "first", 1), 3, 0)
	selected(t, ui, "a triple click in a table cell", "the first cell")
}

func TestDoubleClickThenDragExtendsByWords(t *testing.T) {
	ui, layers, _ := answers(t, 600, "alpha beta gamma delta epsilon")
	layer := layers[0]
	click(layer, runeAt(t, layer, "gamma", 2), 2, 0)
	selected(t, ui, "the double click", "gamma")
	drag(layer, runeAt(t, layer, "delta", 1))
	selected(t, ui, "a drag forward", "gamma delta")
	drag(layer, runeAt(t, layer, "beta", 2))
	selected(t, ui, "a drag back", "beta gamma")
	drag(layer, runeAt(t, layer, "alpha", 4))
	selected(t, ui, "further back", "alpha beta gamma")
}

func TestTripleClickThenDragExtendsByParagraphs(t *testing.T) {
	ui, layers, _ := answers(t, 600, "First one.\n\nSecond one.\n\nThird one.")
	layer := layers[0]
	click(layer, runeAt(t, layer, "Second", 2), 3, 0)
	drag(layer, runeAt(t, layer, "Third", 1))
	selected(t, ui, "a drag by paragraphs", "Second one.\n\nThird one.")
}

func TestShiftClickExtendsAcrossMessages(t *testing.T) {
	ui, layers, _ := answers(t, 600, "First answer, one line.", "Middle answer.", "Last answer, two words.")
	first, last := layers[0], layers[2]
	click(first, runeAt(t, first, "answer", 0), 1, 0)
	click(last, runeAt(t, last, ", two", 0), 1, fyne.KeyModifierShift)
	selected(t, ui, "Shift+click two messages down", "answer, one line.\n\nMiddle answer.\n\nLast answer")
	// Shift+click again, back in the first message: from the same anchor.
	click(first, runeAt(t, first, "line", 0), 1, fyne.KeyModifierShift)
	selected(t, ui, "Shift+click back in the first message", "answer, one ")
}

func TestSelectAllThenEscape(t *testing.T) {
	ui, _, window := answers(t, 600, "First **answer**.", "Second answer.")
	ui.registerShortcuts()
	window.Canvas().(fyne.Shortcutable).TypedShortcut(&fyne.ShortcutSelectAll{})
	selected(t, ui, "Ctrl+A", "First answer.\n\nSecond answer.")
	if got := ui.selected.selectedMarkdown(); got != "First **answer**.\n\nSecond answer." {
		t.Fatalf("Ctrl+A as Markdown: %q", got)
	}
	ui.selectionKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if ui.selected != nil && ui.selected.selectedText() != "" {
		t.Fatalf("Escape left %q selected", ui.selected.selectedText())
	}
}

func TestRightClickCopy(t *testing.T) {
	ui, layers, window := answers(t, 600, "Copy *this* word and `that` one.")
	layer := layers[0]
	click(layer, runeAt(t, layer, "this", 1), 2, 0)
	layer.MouseDown(mouseEvent(layer, runeAt(t, layer, "this", 1), desktop.MouseButtonSecondary, 0))
	menu := openMenu(window)
	if menu == nil {
		t.Fatalf("no menu after a right click on the selection")
	}
	labels := []string{}
	for _, item := range menu.Items {
		labels = append(labels, menuLabel(item))
	}
	if strings.Join(labels, ",") != "Copy,Copy as Markdown,Quote in reply,,Select all" {
		t.Fatalf("menu items: %q", labels)
	}
	test.Tap(menu.Items[0].(fyne.Tappable))
	if got := fyne.CurrentApp().Clipboard().Content(); got != "this" {
		t.Fatalf("Copy put %q on the clipboard", got)
	}
	// Copy as Markdown, of part of the answer.
	ui.chatSel.clicks.reset()
	click(layer, runeAt(t, layer, "this", 1), 1, 0)
	drag(layer, runeAt(t, layer, "that", 3))
	layer.MouseDown(mouseEvent(layer, runeAt(t, layer, "word", 1), desktop.MouseButtonSecondary, 0))
	test.Tap(openMenu(window).Items[1].(fyne.Tappable))
	if got := fyne.CurrentApp().Clipboard().Content(); got != "*his* word and `tha`" {
		t.Fatalf("Copy as Markdown put %q on the clipboard", got)
	}
}

// openMenu is the menu shown over the window, nil when there is none.
func openMenu(window fyne.Window) *widget.Menu {
	var found *widget.Menu
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *widget.Menu:
			found = o
		case *widget.PopUpMenu:
			found = o.Menu
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(o).Objects() {
				if found == nil {
					walk(child)
				}
			}
		case *fyne.Container:
			for _, child := range o.Objects {
				if found == nil {
					walk(child)
				}
			}
		}
	}
	if top := window.Canvas().Overlays().Top(); top != nil {
		walk(top)
	}
	return found
}

// menuLabel is the text a menu item shows, "" for a separator.
func menuLabel(item fyne.CanvasObject) string {
	var found string
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		if found != "" {
			return
		}
		switch o := object.(type) {
		case *canvas.Text:
			found = o.Text
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(o).Objects() {
				walk(child)
			}
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		}
	}
	walk(item)
	return found
}

func TestQuoteInReply(t *testing.T) {
	if got := quoted("one\n\ntwo"); got != "> one\n>\n> two" {
		t.Fatalf("quoted: %q", got)
	}
}

// The selection stays through a new width and while the answer streams,
// and goes when its text changes.
func TestSelectionSurvivesRelayoutAndStreaming(t *testing.T) {
	ui, layers, window := answers(t, 600, "Keep this selection while the answer goes on.")
	layer := layers[0]
	click(layer, runeAt(t, layer, "selection", 2), 2, 0)
	selected(t, ui, "the double click", "selection")
	window.Resize(fyne.NewSize(200, 600))
	layer.changed()
	selected(t, ui, "after a new width", "selection")
	view := ui.views[0].(*markdownView)
	view.ParseMarkdown("Keep this selection while the answer goes on.\n\nAnd on, with **more**.")
	selected(t, ui, "while streaming", "selection")
	view.ParseMarkdown("Some text before. Keep this selection while the answer goes on.\n\nAnd on.")
	selected(t, ui, "with text before it", "selection")
	view.ParseMarkdown("Keep this one instead.")
	if text := layer.selectedText(); text != "" {
		t.Fatalf("its text gone, the selection is still %q", text)
	}
}

func TestWrappedLongWordCopiesWhole(t *testing.T) {
	address := "https://example.com/a/very/long/path/that/cannot/fit/on/one/line/of/this/narrow/window"
	ui, layers, _ := answers(t, 180, "See "+address+" now.")
	layer := layers[0]
	click(layer, runeAt(t, layer, "example", 2), 2, 0)
	selected(t, ui, "a double click on a wrapped address", address)
}
