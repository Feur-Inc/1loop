// SPDX-License-Identifier: MPL-2.0

package ui

// The prompt editor (#33): a discreet button at the right of the message
// box, or Ctrl+E, makes the composer rise over the conversation into a tall
// text editor, its lines numbered in a gutter. The draft stays the same text
// throughout. The same button, the shortcut or Escape put it back; sending
// the prompt does too.

import (
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var (
	iconExpandEditor   = lineIcon("expand-editor", `<path d="M9.5 2.5h4v4M13.5 2.5L9 7M6.5 13.5h-4v-4M2.5 13.5L7 9"/>`)
	iconCollapseEditor = lineIcon("collapse-editor", `<path d="M13.5 6.5h-4v-4M9.5 6.5L14 2M2.5 9.5h4v4M6.5 9.5L2 14"/>`)
)

// editorTop is the room the open editor leaves above it, over the
// conversation.
const editorTop = 14

type promptEditor struct {
	ui     *desktopUI
	open   bool    // open, or opening
	moving bool    // the animation runs
	extra  float32 // the height the input gains, animated
	target float32 // the height it gains once open: the room left
	anim   *fyne.Animation
	button *tapRow
	gutter *lineGutter
}

func newPromptEditor(ui *desktopUI) *promptEditor {
	editor := &promptEditor{ui: ui, gutter: newLineGutter(ui.prompt)}
	editor.gutter.Hide()
	editor.button = newRow("", iconExpandEditor, styleWindow, editor.toggle)
	editor.button.onHover = func(in bool) {
		if !in {
			ui.tips.hide()
			return
		}
		tip := "Expand the editor"
		if editor.open {
			tip = "Back to the message box"
		}
		ui.tips.show(editor.button, ui.withShortcut(tip, "shortcuts.editor"))
	}
	ui.prompt.OnScrolled = func(fyne.Position) { editor.gutter.update() }
	ui.prompt.OnCursorChanged = func() { go fyne.Do(editor.gutter.update) }
	ui.prompt.sent = editor.close
	return editor
}

// active says the editor takes room over the conversation.
func (editor *promptEditor) active() bool { return editor != nil && (editor.open || editor.moving) }

func (editor *promptEditor) toggle() {
	editor.ui.tips.hide()
	if editor.open {
		editor.close()
	} else {
		editor.expand()
	}
}

func (editor *promptEditor) expand() {
	if editor.open {
		return
	}
	editor.open = true
	editor.button.icon = iconCollapseEditor
	editor.button.Refresh()
	editor.gutter.Show()
	editor.gutter.update()
	editor.animate(1)
	editor.ui.window.Canvas().Focus(editor.ui.prompt)
}

func (editor *promptEditor) close() {
	if !editor.open {
		return
	}
	editor.open = false
	editor.button.icon = iconExpandEditor
	editor.button.Refresh()
	editor.animate(0)
}

// animate grows the input to the room left (to 1) or back (to 0): the
// composer rises over the conversation, and the line numbers fade in.
func (editor *promptEditor) animate(to float32) {
	stopMotion(&editor.anim)
	from := float32(0)
	if editor.target > 0 {
		from = editor.extra / editor.target
	}
	editor.moving = true
	length := 300 * time.Millisecond
	if to == 0 {
		length = 220 * time.Millisecond
	}
	editor.anim = motion(length, easeOutQuint, func(p float32) {
		at := from + (to-from)*p
		editor.extra = editor.target * at
		editor.gutter.fade(at)
		if p >= 1 {
			editor.moving = false
			if to == 0 {
				editor.gutter.Hide()
			}
		}
		editor.ui.prompt.resized()
	})
}

// fit is told the conversation's height and the footer's: the room the
// open editor fills. While it moves, only the end it moves to changes.
func (editor *promptEditor) fit(height, footer float32) {
	base := footer - editor.extra
	target := fyne.Max(0, height-editorTop-base)
	if target == editor.target {
		return
	}
	editor.target = target
	if editor.open && !editor.moving {
		editor.extra = target
		go fyne.Do(editor.ui.prompt.resized)
	}
}

// editorGrowLayout gives the input line the editor's extra height.
type editorGrowLayout struct{ editor *promptEditor }

func (layoutSpec editorGrowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := objects[0].MinSize()
	return fyne.NewSize(size.Width, size.Height+layoutSpec.editor.extra)
}

func (layoutSpec editorGrowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.Position{})
	objects[0].Resize(size)
}

// promptLineLayout lays out the input line: the "+" at the left, then the
// line numbers while the editor is open, the text, and the editor's button
// at the right; both buttons face the first line.
type promptLineLayout struct{}

const promptLineGap = 4.0

func (promptLineLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	input, attach, gutter, button := objects[0], objects[1], objects[2], objects[3]
	width := attach.MinSize().Width + promptLineGap + input.MinSize().Width + promptLineGap + button.MinSize().Width
	if gutter.Visible() {
		width += gutter.MinSize().Width
	}
	return fyne.NewSize(width, fyne.Max(input.MinSize().Height, fyne.Max(attach.MinSize().Height, button.MinSize().Height)))
}

func (promptLineLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	input, attach, gutter, button := objects[0], objects[1], objects[2], objects[3]
	line := textSize("Ag", theme.TextSize(), fyne.TextStyle{}).Height
	firstLine := func(object fyne.CanvasObject) float32 {
		return fyne.Max(0, theme.InnerPadding()+line/2-object.MinSize().Height/2)
	}
	attach.Resize(attach.MinSize())
	attach.Move(fyne.NewPos(0, firstLine(attach)))
	button.Resize(button.MinSize())
	button.Move(fyne.NewPos(size.Width-button.MinSize().Width, firstLine(button)))
	left := attach.MinSize().Width + promptLineGap
	if gutter.Visible() {
		gutter.Move(fyne.NewPos(left, 0))
		gutter.Resize(fyne.NewSize(gutter.MinSize().Width, size.Height))
		left += gutter.MinSize().Width
	}
	input.Move(fyne.NewPos(left, 0))
	input.Resize(fyne.NewSize(size.Width-left-promptLineGap-button.MinSize().Width, size.Height))
}

// ---------------------------------------------------------------------------
// The line numbers.

// lineGutter numbers the prompt's lines beside it: a line that wraps over
// several rows has its number on its first row, and the numbers follow the
// text as it scrolls. The cursor's line is brighter.
type lineGutter struct {
	widget.BaseWidget
	entry   *chatEntry
	labels  []*canvas.Text
	alpha   float32
	width   float32
	content *fyne.Container
}

func newLineGutter(entry *chatEntry) *lineGutter {
	gutter := &lineGutter{entry: entry, alpha: 1, content: &fyne.Container{}}
	gutter.ExtendBaseWidget(gutter)
	return gutter
}

func (gutter *lineGutter) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(gutter.content)
}

func (gutter *lineGutter) MinSize() fyne.Size {
	return fyne.NewSize(fyne.Max(gutter.width, 28), 0)
}

func (gutter *lineGutter) Resize(size fyne.Size) {
	gutter.BaseWidget.Resize(size)
	gutter.update()
}

func (gutter *lineGutter) fade(alpha float32) {
	gutter.alpha = alpha
	gutter.update()
}

// update places the numbers on the rows the entry shows now.
func (gutter *lineGutter) update() {
	if !gutter.Visible() {
		return
	}
	entry := gutter.entry
	th := entry.Theme()
	textSizeValue := th.Size(theme.SizeNameText)
	row := textSize("M", textSizeValue, entry.TextStyle).Height
	top := th.Size(theme.SizeNameInputBorder) + th.Size(theme.SizeNameInnerPadding) - entry.ScrollOffset().Y
	starts := entry.LineStarts()
	lines := 0
	for _, start := range starts {
		if start {
			lines++
		}
	}
	digits := textSize(strconv.Itoa(max(lines, 10)), textSizeValue*0.9, fyne.TextStyle{Monospace: true}).Width
	if width := digits + 14; width != gutter.width {
		gutter.width = width
		go fyne.Do(gutter.entry.resized)
	}
	cursor := entry.CursorRow
	cursorLine, number, shown := 0, 0, 0
	for r, start := range starts {
		if start {
			number++
		}
		if r == cursor {
			cursorLine = number
		}
	}
	height := gutter.Size().Height
	number = 0
	for r, start := range starts {
		if !start {
			continue
		}
		number++
		y := top + float32(r)*row
		if y < -row/2 || y+row > height {
			continue
		}
		if shown == len(gutter.labels) {
			label := canvas.NewText("", colFaint)
			label.TextStyle = fyne.TextStyle{Monospace: true}
			label.Alignment = fyne.TextAlignTrailing
			gutter.labels = append(gutter.labels, label)
			gutter.content.Objects = append(gutter.content.Objects, label)
		}
		label := gutter.labels[shown]
		shown++
		label.Text = strconv.Itoa(number)
		label.TextSize = textSizeValue * 0.9
		ink := colFaint
		if number == cursorLine {
			ink = colMuted
		}
		label.Color = withAlpha(ink, gutter.alpha)
		// On the text's baseline, the digits being a little smaller.
		label.Move(fyne.NewPos(0, y+(row-textSize("0", label.TextSize, label.TextStyle).Height)/2-2))
		label.Resize(fyne.NewSize(gutter.width-10, row))
		label.Show()
		label.Refresh()
	}
	for _, label := range gutter.labels[shown:] {
		label.Hide()
	}
}
