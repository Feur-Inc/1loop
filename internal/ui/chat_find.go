// SPDX-License-Identifier: MPL-2.0

package ui

// Find in the chat (Ctrl+F): a bar over the conversation's top right corner
// finds words in the messages of the chat on screen. Enter (or the arrows)
// goes from one match to the next, scrolling to it; the match is selected,
// as the mouse would select it, so it shows and Ctrl+C copies it.

import (
	"fmt"
	"strings"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
)

// findMatch is the nth place of the words in a message.
type findMatch struct{ item, nth int }

type chatFind struct {
	ui      *desktopUI
	view    *fyne.Container // the layer over the conversation
	field   *keyEntry
	count   *canvas.Text
	matches []findMatch
	at      int
}

func (ui *desktopUI) buildFind() fyne.CanvasObject {
	find := &chatFind{ui: ui}
	find.field = newKeyEntry()
	find.field.SetPlaceHolder("Find in this chat")
	find.field.shortcut = ui.shortcut
	find.field.OnChanged = func(string) { find.search(true) }
	find.field.onKey = func(key *fyne.KeyEvent) bool {
		switch key.Name {
		case fyne.KeyReturn, fyne.KeyEnter:
			step := 1
			if driver, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok && driver.CurrentKeyModifiers()&fyne.KeyModifierShift != 0 {
				step = -1
			}
			find.step(step)
		case fyne.KeyDown:
			find.step(1)
		case fyne.KeyUp:
			find.step(-1)
		case fyne.KeyEscape:
			find.close()
		default:
			return false
		}
		return true
	}
	find.count = textLine("", 12, colFaint)
	up := newRow("", iconChevronUp, styleWindow, func() { find.step(-1) })
	down := newRow("", iconChevron, styleWindow, func() { find.step(1) })
	closeRow := newRow("", iconClose, styleWindow, find.close)
	for row, tip := range map[*tapRow]string{up: "Previous match · Shift+Enter", down: "Next match · Enter", closeRow: "Close · Escape"} {
		row, tip := row, tip
		row.onHover = func(in bool) {
			if in {
				ui.tips.show(row, tip)
			} else {
				ui.tips.hide()
			}
		}
	}
	icon := canvas.NewImageFromResource(tinted(iconSearch, colFaint))
	icon.SetMinSize(fyne.NewSquareSize(15))
	bar := surface(container.NewBorder(nil, nil, padded(0, 0, 6, 2, container.NewCenter(icon)),
		container.New(layout.NewCustomPaddedHBoxLayout(2), container.NewCenter(find.count), up, down, closeRow),
		sized(220, find.field)), colRaised, colBorder, 10, 3)
	find.view = container.NewVBox(padded(8, 0, 0, 16, container.NewHBox(layout.NewSpacer(), bar)))
	find.view.Hide()
	ui.find = find
	return find.view
}

// findInChat opens the bar, or selects what it holds when it is open.
func (ui *desktopUI) findInChat() {
	find := ui.find
	if ui.prefs.open {
		ui.prefs.close()
	}
	if !find.view.Visible() {
		find.view.Show()
		if find.field.Text != "" {
			find.search(true)
		}
	}
	ui.window.Canvas().Focus(find.field)
	find.field.TypedShortcut(&fyne.ShortcutSelectAll{})
}

// close hides the bar and gives the message box the focus again.
func (find *chatFind) close() {
	if !find.view.Visible() {
		return
	}
	find.view.Hide()
	find.ui.clearSelection()
	find.ui.window.Canvas().Focus(find.ui.prompt)
}

// reset forgets the matches of the chat left; the bar stays if it is open.
func (find *chatFind) reset() {
	find.matches, find.at = nil, 0
	if find.view.Visible() {
		find.search(false)
	}
}

// findable are the kinds of messages the bar finds in.
func findable(kind string) bool { return kind == "user" || kind == "assistant" }

// collect finds every place of the words in the chat on screen.
func (find *chatFind) collect() []findMatch {
	chat := find.ui.current
	query := strings.ToLower(strings.TrimSpace(find.field.Text))
	if chat == nil || query == "" {
		return nil
	}
	var matches []findMatch
	for i, item := range chat.Items {
		if !findable(item.Kind) {
			continue
		}
		for n := range strings.Count(strings.ToLower(item.Text), query) {
			matches = append(matches, findMatch{i, n})
		}
	}
	return matches
}

// search finds again; jump goes to the first match from the top of the view.
func (find *chatFind) search(jump bool) {
	find.matches = find.collect()
	find.at = 0
	if jump && len(find.matches) > 0 {
		top := find.ui.scroll.Offset.Y
		origin, _ := offsetIn(find.ui.scroll.Content, find.ui.transcript)
		for i, match := range find.matches {
			if object := find.ui.itemObject(match.item); object != nil && origin.Y+object.Position().Y+object.Size().Height >= top {
				find.at = i
				break
			}
		}
		find.show()
	} else if len(find.matches) == 0 {
		find.ui.clearSelection()
	}
	find.label()
}

func (find *chatFind) label() {
	switch {
	case strings.TrimSpace(find.field.Text) == "":
		find.count.Text = ""
	case len(find.matches) == 0:
		find.count.Text = "No matches"
	default:
		find.count.Text = fmt.Sprintf("%d of %d", find.at+1, len(find.matches))
	}
	find.count.Refresh()
}

// step goes to the next (1) or previous (-1) match, round the ends.
func (find *chatFind) step(by int) {
	before := findMatch{-1, 0}
	if find.at < len(find.matches) {
		before = find.matches[find.at]
	}
	find.matches = find.collect() // the chat may have grown
	if len(find.matches) == 0 {
		find.label()
		return
	}
	find.at = 0
	for i, match := range find.matches {
		if match == before {
			find.at = (i + by + len(find.matches)) % len(find.matches)
		}
	}
	find.show()
	find.label()
}

// show scrolls to the current match and selects it.
func (find *chatFind) show() {
	ui := find.ui
	if ui.loadingChat != nil || find.at >= len(find.matches) {
		return
	}
	match := find.matches[find.at]
	object := ui.itemObject(match.item)
	if object == nil {
		return
	}
	// First to the message, which brings it into view and lays it out,
	// then to the line of the match within it.
	origin, _ := offsetIn(ui.scroll.Content, ui.transcript)
	ui.followTail = false
	find.scrollTo(origin.Y+object.Position().Y, object.Size().Height)
	layer, start, end, found := findIn(object, strings.ToLower(strings.TrimSpace(find.field.Text)), match.nth)
	if !found {
		ui.clearSelection()
		return
	}
	layer.from, layer.point, layer.active = start, end, true
	layer.Refresh()
	ui.selectIn(layer)
	if at, ok := offsetIn(ui.scroll.Content, layer); ok {
		piece := layer.pieces[start.index]
		find.scrollTo(at.Y+piece.at.Y, piece.size.Height)
	}
}

// scrollTo brings the band from y, height high, into view: a third of the
// way down when it was not.
func (find *chatFind) scrollTo(y, height float32) {
	scroll := find.ui.scroll
	view := scroll.Size().Height
	if y >= scroll.Offset.Y+40 && y+fyne.Min(height, view/2) <= scroll.Offset.Y+view-20 {
		return
	}
	limit := fyne.Max(0, scroll.Content.Size().Height-view)
	scroll.ScrollToOffset(fyne.NewPos(0, fyne.Max(0, fyne.Min(y-view/3, limit))))
	relayout(find.ui.transcript) // the view moved without a scroll event: bring its items in
	find.ui.relayoutColumn()
}

// itemObject is the transcript's view of chat item index, nil when it has
// none on screen.
func (ui *desktopUI) itemObject(index int) fyne.CanvasObject {
	var part fyne.CanvasObject
	switch view := ui.views[index].(type) {
	case *markdownView:
		part = view.box
	case *userImageView:
		part = view.box
	}
	if part == nil {
		return nil
	}
	for _, object := range ui.transcript.Objects {
		if _, holds := offsetIn(object, part); holds {
			return object
		}
	}
	return nil
}

// findIn finds the nth place of query in the text a message draws, in its
// selection layers, as a selection.
func findIn(object fyne.CanvasObject, query string, nth int) (*selectLayer, selPoint, selPoint, bool) {
	needle := []rune(query)
	if len(needle) == 0 {
		return nil, selPoint{}, selPoint{}, false
	}
	var layers []*selectLayer
	collectLayers(object, &layers)
	var last *selectLayer
	var lastStart, lastEnd selPoint
	seen := 0
	for _, layer := range layers {
		layer.gather()
		// The layer's text in reading order, and where each piece starts.
		var text []rune
		starts := make([]int, len(layer.pieces))
		for i, piece := range layer.pieces {
			starts[i] = len(text)
			for _, r := range piece.runes {
				text = append(text, unicode.ToLower(r))
			}
			if i < len(layer.pieces)-1 {
				text = append(text, []rune(piece.after)...)
			}
		}
		point := func(at int, end bool) selPoint {
			for i := len(starts) - 1; i >= 0; i-- {
				if starts[i] < at || (!end && starts[i] == at) {
					return selPoint{i, min(at-starts[i], len(layer.pieces[i].runes))}
				}
			}
			return selPoint{}
		}
		for at := 0; at+len(needle) <= len(text); at++ {
			if !runesAt(text, at, needle) {
				continue
			}
			start, end := point(at, false), point(at+len(needle), true)
			if seen == nth {
				return layer, start, end, true
			}
			seen++
			last, lastStart, lastEnd = layer, start, end
			at += len(needle) - 1
		}
	}
	// Fewer places drawn than written (a link's address, markdown's marks):
	// the last one drawn.
	return last, lastStart, lastEnd, last != nil
}

func runesAt(text []rune, at int, needle []rune) bool {
	for i, r := range needle {
		if text[at+i] != r {
			return false
		}
	}
	return true
}

func collectLayers(object fyne.CanvasObject, layers *[]*selectLayer) {
	switch o := object.(type) {
	case *selectLayer:
		*layers = append(*layers, o)
	case *fyne.Container:
		for _, child := range o.Objects {
			collectLayers(child, layers)
		}
	}
}
