// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/pmezard/go-difflib/difflib"

	"1loop/internal/agent"
)

// An edit waiting for approval, reviewed in the side panel (#242): each file
// whole with its changes, a change left out with a click, the proposed text
// edited by hand, the files approved one by one, a reason with a refusal.

// maxReviewEdit is the largest text the review edits: past it, typing lags.
const maxReviewEdit = 256 << 10

// reviewChange is a run of lines the proposal replaces.
type reviewChange struct{ beforeFrom, beforeTo, afterFrom, afterTo int }

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// reviewChanges is what differs between two texts, by lines. What both start
// and end with is skipped first: only the part that changes is compared.
func reviewChanges(before, after []string) []reviewChange {
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	end := 0
	for end < len(before)-start && end < len(after)-start && before[len(before)-1-end] == after[len(after)-1-end] {
		end++
	}
	a, b := before[start:len(before)-end], after[start:len(after)-end]
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	if len(a)*len(b) > 25_000_000 { // thousands of lines rewritten: one change
		return []reviewChange{{start, start + len(a), start, start + len(b)}}
	}
	var changes []reviewChange
	for _, code := range difflib.NewMatcherWithJunk(a, b, false, nil).GetOpCodes() {
		if code.Tag != 'e' {
			changes = append(changes, reviewChange{start + code.I1, start + code.I2, start + code.J1, start + code.J2})
		}
	}
	return changes
}

// reviewFile is a file under review and what the user makes of it.
type reviewFile struct {
	agent.ReviewFile
	text   string // what is approved: the proposal, then the user's version
	denied bool
	offset float32 // how far down its changes are read; <0 before the first time
}

func (file *reviewFile) edited() bool { return file.text != lf(file.After) }

func lf(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }

// dropChange takes a change out of the text: its lines are the file's own.
func (file *reviewFile) dropChange(change reviewChange) {
	before, after := splitLines(lf(file.Before)), splitLines(file.text)
	var lines []string
	lines = append(lines, after[:change.afterFrom]...)
	lines = append(lines, before[change.beforeFrom:change.beforeTo]...)
	lines = append(lines, after[change.afterTo:]...)
	file.text = strings.Join(lines, "")
}

// reviewAnswer is the answer the files make, allowed.
func reviewAnswer(files []*reviewFile, note string) agent.ApprovalAnswer {
	answer := agent.ApprovalAnswer{Note: strings.TrimSpace(note)}
	for _, file := range files {
		switch {
		case file.denied:
			answer.Denied = append(answer.Denied, file.Path)
		default:
			answer.Allow = true
			if !file.Fixed && file.edited() {
				if answer.Files == nil {
					answer.Files = map[string]string{}
				}
				answer.Files[file.Path] = file.text
			}
		}
	}
	if !answer.Allow {
		answer.Denied = nil
	}
	return answer
}

// reviewRows is a file whole: its lines, and before each change a row that
// says which it is (changes[i] for the row numbers given).
func reviewRows(file *reviewFile) ([]diffLine, map[int]reviewChange) {
	before, after := splitLines(lf(file.Before)), splitLines(file.text)
	changes := reviewChanges(before, after)
	rows := []diffLine{{kind: 'f', text: file.Path}}
	at := map[int]reviewChange{}
	line := func(kind byte, text string, old, next int) {
		rows = append(rows, diffLine{kind: kind, text: expandTabs(strings.TrimRight(text, "\r\n")), old: old, next: next})
	}
	i, j := 0, 0
	same := func(until int) {
		for ; i < until; i, j = i+1, j+1 {
			line(' ', before[i], i+1, j+1)
		}
	}
	for n, change := range changes {
		same(change.beforeFrom)
		label := fmt.Sprintf("Change %d of %d", n+1, len(changes))
		if !file.Fixed {
			label += "  ·  click to leave it out"
			at[len(rows)] = change
		}
		rows = append(rows, diffLine{kind: 'h', text: label})
		for ; i < change.beforeTo; i++ {
			line('-', before[i], i+1, 0)
			rows[0].removed++
		}
		for ; j < change.afterTo; j++ {
			line('+', after[j], 0, j+1)
			rows[0].added++
		}
	}
	same(len(before))
	highlightDiff(rows)
	return rows, at
}

// reviewKeys takes the keyboard while the changes show.
type reviewKeys struct {
	widget.BaseWidget
	view *reviewView
}

func (keys *reviewKeys) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
func (keys *reviewKeys) FocusGained()   {}
func (keys *reviewKeys) FocusLost()     {}
func (keys *reviewKeys) TypedRune(rune) {}
func (keys *reviewKeys) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		keys.view.deny()
	}
}
func (keys *reviewKeys) TypedShortcut(s fyne.Shortcut) { keys.view.shortcut(s) }

// reviewEntry is a text of the review: Ctrl+Enter approves from it too.
type reviewEntry struct {
	widget.Entry
	view   *reviewView
	escape func()
}

func (entry *reviewEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape {
		entry.escape()
		return
	}
	entry.Entry.TypedKey(key)
}

func (entry *reviewEntry) TypedShortcut(s fyne.Shortcut) {
	if !entry.view.shortcut(s) {
		entry.Entry.TypedShortcut(s)
	}
}

// reviewView is the review of one approval.
type reviewView struct {
	ui       *desktopUI
	window   fyne.Window
	files    []*reviewFile
	active   int
	editing  bool
	answered bool
	answer   func(agent.ApprovalAnswer)

	view    fyne.CanvasObject
	strip   *fyne.Container
	body    *fyne.Container
	tools   *fyne.Container
	state   *canvas.Text
	bar     *fyne.Container // both, laid out again as they change
	list    *widget.List
	keys    *reviewKeys
	reason  *reviewEntry
	approve *tapRow
}

func newReviewView(ui *desktopUI, window fyne.Window, title string, files []agent.ReviewFile, answer func(agent.ApprovalAnswer)) *reviewView {
	view := &reviewView{ui: ui, window: window, answer: answer}
	for _, file := range files {
		view.files = append(view.files, &reviewFile{ReviewFile: file, text: lf(file.After), offset: -1})
	}
	view.keys = &reviewKeys{view: view}
	view.keys.ExtendBaseWidget(view.keys)
	view.strip = container.New(layout.NewCustomPaddedHBoxLayout(6))
	view.body, view.tools = container.NewStack(), container.New(layout.NewCustomPaddedHBoxLayout(6))
	view.state = textLine("", 12, colMuted)
	view.reason = &reviewEntry{view: view}
	view.reason.ExtendBaseWidget(view.reason)
	view.reason.SetPlaceHolder("Why, for the model (optional, sent with a refusal)")
	view.reason.escape = func() { view.focus() }
	refuse := newRow("Deny", nil, styleOutline, view.deny)
	view.approve = newRow("Approve", nil, stylePrimary, view.allow)
	heading := textLine(strings.TrimSuffix(title, "?"), 14, colText)
	heading.TextStyle.Bold = true
	top := container.NewVBox(padded(12, 2, 14, 14, container.New(&clipLayout{}, heading)))
	if len(files) > 1 {
		top.Add(padded(2, 2, 14, 14, strip(view.strip)))
	}
	view.bar = container.NewBorder(nil, nil, container.NewCenter(view.state), view.tools)
	top.Add(padded(0, 4, 14, 14, view.bar))
	hint := textLine("Ctrl+Enter approves  ·  Esc denies", 11.5, colFaint)
	bottom := padded(8, 12, 14, 14, container.NewVBox(view.reason,
		padded(6, 0, 0, 0, container.NewBorder(nil, nil, container.NewCenter(hint), container.New(layout.NewCustomPaddedHBoxLayout(8), refuse, view.approve)))))
	view.view = container.NewStack(view.keys, container.NewBorder(top, bottom, nil, nil, view.body))
	view.draw()
	return view
}

// clipLayout gives a heading the width there is, and asks for none.
type clipLayout struct{}

func (*clipLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, o[0].MinSize().Height)
}
func (*clipLayout) Layout(o []fyne.CanvasObject, size fyne.Size) { o[0].Resize(size) }

// focus gives the review the keyboard.
func (view *reviewView) focus() { view.focusOn(view.keys) }

// focusOn waits for what is being handled (a tap, a tab that opens), which
// moves the focus itself, and for the object to be on screen.
func (view *reviewView) focusOn(object fyne.Focusable) {
	if view.window == nil {
		return
	}
	go func() {
		for range 30 { // a panel that slides in takes no focus before its end
			done := false
			fyne.DoAndWait(func() {
				if view.answered {
					done = true
					return
				}
				canvas := view.window.Canvas()
				canvas.Focus(object)
				done = canvas.Focused() == object
			})
			if done {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
}

func (view *reviewView) shortcut(s fyne.Shortcut) bool {
	if c, ok := s.(*desktop.CustomShortcut); ok && c.Modifier == fyne.KeyModifierControl && (c.KeyName == fyne.KeyReturn || c.KeyName == fyne.KeyEnter) {
		view.allow()
		return true
	}
	return view.ui != nil && view.ui.shortcut(s)
}

func (view *reviewView) finish(answer agent.ApprovalAnswer) {
	if view.answered {
		return
	}
	view.answered = true
	view.answer(answer)
}

func (view *reviewView) deny() {
	view.finish(agent.ApprovalAnswer{Note: strings.TrimSpace(view.reason.Text)})
}

func (view *reviewView) allow() { view.finish(reviewAnswer(view.files, view.reason.Text)) }

// draw shows the open file and what is decided.
func (view *reviewView) draw() {
	file := view.files[view.active]
	view.strip.RemoveAll()
	for i, other := range view.files {
		i, other := i, other
		mark := "✓ "
		if other.denied {
			mark = "✕ "
		}
		chipRow := newRow(mark+filepath.Base(other.Path), nil, styleChip, func() { view.open(i) })
		chipRow.SetSelected(i == view.active)
		view.strip.Add(chipRow)
	}
	view.strip.Refresh()

	view.tools.RemoveAll()
	var notes []string
	switch file.Op {
	case "add":
		notes = append(notes, "New file")
	case "delete":
		notes = append(notes, "Deletes the file")
	}
	if file.From != "" {
		notes = append(notes, "Moved from "+file.From)
	}
	if !file.Fixed && file.edited() {
		notes = append(notes, "Edited by you")
		view.tools.Add(chip("Restore the proposal", func() { file.text, view.editing = lf(file.After), false; view.draw() }))
	}
	if !file.Fixed && !file.denied {
		if view.editing {
			view.tools.Add(chip("Show changes", func() { view.editing = false; view.draw(); view.focus() }))
		} else if len(file.text) <= maxReviewEdit {
			view.tools.Add(chip("Edit", func() { view.editing = true; view.draw() }))
		} else {
			notes = append(notes, "Too large to edit here")
		}
	}
	if len(view.files) > 1 {
		label := "Leave this file out"
		if file.denied {
			label = "Take this file"
			notes = []string{"Not written"}
		}
		view.tools.Add(chip(label, func() { file.denied, view.editing = !file.denied, false; view.draw() }))
	}
	view.state.Text = strings.Join(notes, "  ·  ")
	view.state.Refresh()
	view.bar.Refresh()
	for _, object := range view.bar.Objects {
		object.Refresh()
	}
	view.label()
	if view.editing {
		view.body.Objects = []fyne.CanvasObject{view.editor(file)}
	} else {
		view.body.Objects = []fyne.CanvasObject{view.changes(file)}
	}
	view.body.Refresh()
}

// label says what approving does.
func (view *reviewView) label() {
	taken, edited := 0, false
	for _, file := range view.files {
		if !file.denied {
			taken++
			edited = edited || !file.Fixed && file.edited()
		}
	}
	text := "Approve"
	if taken < len(view.files) && taken > 0 {
		text = fmt.Sprintf("Approve %d of %d files", taken, len(view.files))
	}
	if edited {
		text += " with your edits"
	}
	if taken == 0 {
		text = "Deny all"
	}
	view.approve.SetText(text)
}

func (view *reviewView) open(i int) {
	view.keepOffset()
	view.active, view.editing = i, false
	view.draw()
	view.focus()
}

func (view *reviewView) keepOffset() {
	if view.list != nil && !view.editing {
		view.files[view.active].offset = view.list.GetScrollOffset()
	}
}

// changes is the whole file, its changes marked.
func (view *reviewView) changes(file *reviewFile) fyne.CanvasObject {
	rows, at := reviewRows(file)
	width := float32(320)
	first := 0
	for i, row := range rows {
		width = fyne.Max(width, float32(utf8.RuneCountInString(row.text))*outputCell().Width+110)
		if first == 0 && row.kind == 'h' {
			first = i
		}
	}
	list := widget.NewList(func() int { return len(rows) }, func() fyne.CanvasObject { return newDiffRow(width) },
		func(i widget.ListItemID, object fyne.CanvasObject) {
			row := object.(*diffRow)
			row.line = rows[i]
			row.Refresh()
		})
	list.HideSeparators = true
	for i, row := range rows {
		switch row.kind {
		case 'f':
			list.SetItemHeight(i, 42)
		case 'h':
			list.SetItemHeight(i, 30)
		}
	}
	list.OnSelected = func(i widget.ListItemID) {
		list.UnselectAll()
		if change, ok := at[i]; ok && !file.denied {
			view.keepOffset()
			file.dropChange(change)
			view.draw()
		}
		view.focus()
	}
	view.list = list
	// Once the list has its size: back where it was read, or a few lines
	// above the first change.
	offset := file.offset
	if offset < 0 {
		offset = 42 + float32(max(first-5, 0)-1)*24
	}
	if offset > 0 {
		go func() {
			for range 40 {
				time.Sleep(25 * time.Millisecond)
				done := false
				fyne.DoAndWait(func() {
					if done = list.Size().Height > 0 || view.answered; done && view.list == list {
						list.ScrollToOffset(offset)
					}
				})
				if done {
					return
				}
			}
		}()
	}
	content := container.NewThemeOverride(list, diffListTheme{fyne.CurrentApp().Settings().Theme()})
	return container.NewHScroll(content)
}

// editor is the proposed text, to change by hand.
func (view *reviewView) editor(file *reviewFile) fyne.CanvasObject {
	entry := &reviewEntry{view: view}
	entry.MultiLine = true
	entry.TextStyle = fyne.TextStyle{Monospace: true}
	entry.Wrapping = fyne.TextWrapOff
	entry.ExtendBaseWidget(entry)
	entry.SetText(file.text)
	entry.OnChanged = func(text string) {
		file.text = text
		view.label()
	}
	entry.escape = func() { view.editing = false; view.draw(); view.focus() }
	view.focusOn(entry)
	return padded(0, 0, 14, 14, entry)
}

// reviewFiles is the files of an approval, this computer's or the server's.
func (ui *desktopUI) reviewFiles(ctx context.Context, chat *conversation, id string) ([]agent.ReviewFile, error) {
	if chat.server == nil {
		return ui.backend.Review(id), nil
	}
	var reply struct{ Files []agent.ReviewFile }
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := chat.server.client.json(ctx, "GET", "/api/review?id="+url.QueryEscape(id), nil, &reply)
	return reply.Files, err
}

// openReview opens an approval's tab in its chat's panel. The returned
// function takes it away.
func (panel *toolPanel) openReview(chat *conversation, title string, files []agent.ReviewFile, answer func(agent.ApprovalAnswer)) func() {
	folded := panel.target == 0
	view := newReviewView(panel.ui, panel.ui.window, title, files, answer)
	name := "Review"
	if len(files) == 1 {
		name += " · " + filepath.Base(files[0].Path)
	}
	panel.tabs = append(panel.tabs, panelTab{kind: "review", title: name, icon: toolGlyph("edit_file"), content: view.view, review: view, cancel: view.deny})
	panel.choose(len(panel.tabs) - 1)
	if panel.target < 1 {
		panel.unfold()
	}
	if panel.ui.prompt == nil || panel.ui.prompt.Text == "" { // never under what is being typed
		view.focus()
	}
	return func() {
		view.answered = true
		for i, tab := range panel.tabs {
			if tab.review == view {
				panel.close(i)
				if folded && panel.target > 0 && panel.owner == chat {
					panel.fold()
				}
				if panel.owner == chat && panel.ui.prompt != nil {
					panel.ui.window.Canvas().Focus(panel.ui.prompt)
				}
				return
			}
		}
		if state := chat.panel; state != nil { // its chat is not on screen any more
			for i, tab := range state.tabs {
				if tab.review == view {
					state.tabs = append(state.tabs[:i], state.tabs[i+1:]...)
					state.active = activeAfterClose(state.active, i, len(state.tabs))
					state.open = state.open && !folded && len(state.tabs) > 0
					return
				}
			}
		}
	}
}
