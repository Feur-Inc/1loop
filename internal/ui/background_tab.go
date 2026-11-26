// SPDX-License-Identifier: MPL-2.0

package ui

// The side panel's Background tab (#597): the chat's background commands,
// running and finished, each with its command, its state and how long it
// ran, and its output as it comes, with Stop while it runs. A click on a
// background command's call in the conversation opens it here, rather than
// the call's raw arguments.

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

// backgroundShown is how much of a command's output the tab shows: its end.
const backgroundShown = 6 << 10

// openBackground shows a chat's background commands, id's opened and in
// view ("" for the list alone).
func (panel *toolPanel) openBackground(chat *conversation, id string) {
	panel = panel.of(chat)
	panel.backgroundFocus = id
	if id != "" {
		panel.backgroundOpen[id] = true
	}
	for i, tab := range panel.tabs {
		if tab.kind == "commands" {
			panel.choose(i)
			panel.unfold()
			panel.backgroundChanged(chat)
			return
		}
	}
	panel.tabs = append(panel.tabs, panel.backgroundTab(chat))
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

// backgroundTab is the tab, drawn from the chat's tasks.
func (panel *toolPanel) backgroundTab(chat *conversation) panelTab {
	box := container.NewVBox()
	scroll := newKineticVScroll(padded(16, 18, 18, 18, box))
	tab := panelTab{kind: "commands", title: "Background", icon: toolGlyph("shell"), backgroundChat: chat, backgroundView: box, backgroundScroll: scroll, content: scroll}
	panel.drawBackground(tab)
	return tab
}

// backgroundChanged draws the tab again when a task starts, writes or ends.
func (panel *toolPanel) backgroundChanged(chat *conversation) {
	panel = panel.of(chat)
	for _, tab := range panel.tabs {
		if tab.kind == "commands" && tab.backgroundChat == chat {
			panel.drawBackground(tab)
		}
	}
}

// tickBackground follows the running commands while the tab is shown: their
// time and output, once a second.
func (panel *toolPanel) tickBackground() {
	if panel.active < 0 || panel.active >= len(panel.tabs) || panel.target == 0 {
		return
	}
	tab := panel.tabs[panel.active]
	if tab.kind != "commands" || tab.backgroundChat == nil {
		return
	}
	for _, task := range tab.backgroundChat.tasks.Snapshot() {
		if task.Running {
			panel.drawBackground(tab)
			return
		}
	}
}

// backgroundState is a task's state in a few words, with how long it ran.
func backgroundState(task agent.BackgroundTaskInfo, now time.Time) (status, state string) {
	if task.Running {
		return "in_progress", "Running · " + now.Sub(task.Started).Round(time.Second).String()
	}
	took := task.Ended.Sub(task.Started).Round(time.Second).String()
	switch {
	case task.Stopped:
		return "pending", "Stopped after " + took
	case task.ExitCode == 0:
		return "completed", "Done in " + took
	}
	return "pending", fmt.Sprintf("Failed with code %d after %s", task.ExitCode, took)
}

// drawBackground lays the tab's commands out, the newest first.
func (panel *toolPanel) drawBackground(tab panelTab) {
	box, chat := tab.backgroundView, tab.backgroundChat
	if box == nil {
		return
	}
	box.RemoveAll()
	var tasks []agent.BackgroundTaskInfo
	if chat != nil {
		tasks = chat.tasks.Snapshot()
	}
	if len(tasks) == 0 {
		box.Add(note("No background commands in this chat. The agent runs one with run_in_background: a server, a watcher, a long build."))
		box.Refresh()
		return
	}
	title := textLine("Background commands", 16, colText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	box.Add(padded(0, 10, 0, 0, title))
	now := time.Now()
	var focused fyne.CanvasObject
	for i := len(tasks) - 1; i >= 0; i-- {
		task := tasks[i]
		card := panel.backgroundCard(chat, task, now)
		if task.ID == panel.backgroundFocus {
			focused = card
		}
		box.Add(card)
	}
	box.Refresh()
	if focused != nil && tab.backgroundScroll != nil {
		scroll := tab.backgroundScroll
		panel.backgroundFocus = "" // once: then the reader scrolls
		fyne.Do(func() {
			if pos, ok := offsetIn(scroll.Content, focused); ok {
				scroll.ScrollToOffset(fyne.NewPos(0, fyne.Max(0, pos.Y-12)))
			}
		})
	}
}

// backgroundCard is one command: its mark, command, state and Stop, then
// its output when open (a running command, or one asked for, is open).
func (panel *toolPanel) backgroundCard(chat *conversation, task agent.BackgroundTaskInfo, now time.Time) fyne.CanvasObject {
	id := task.ID
	status, state := backgroundState(task, now)
	mark := newTaskMark(status, func() bool { return false })
	redraw := func() { panel.backgroundChanged(chat) }
	command := backgroundCommand(task.Command, panel.openCommands, id, redraw)
	var action fyne.CanvasObject = layout.NewSpacer()
	if task.Running {
		action = newRow("Stop", iconStop, styleChip, func() { chat.tasks.Stop(id) })
	}
	open, asked := panel.backgroundOpen[id]
	if !asked {
		open = task.Running
	}
	toggleIcon := iconChevronRight
	if open {
		toggleIcon = iconChevron
	}
	toggle := newRow("Output", toggleIcon, styleChip, func() {
		panel.backgroundOpen[id] = !open
		redraw()
	})
	toggle.iconAfter = true
	head := container.NewBorder(nil, nil, container.NewVBox(mark), container.NewVBox(action),
		padded(1, 0, 12, 8, container.NewVBox(command, textLine(state, 11.5, colMuted))))
	parts := container.NewVBox(head, padded(4, 0, 30, 0, container.NewHBox(toggle)))
	if open {
		_, output, _ := chat.tasks.Output(id, backgroundShown)
		output = strings.TrimRight(output, "\n")
		if output == "" {
			output = "(no output yet)"
		}
		parts.Add(padded(4, 0, 30, 0, surface(codeText(output), colCode, colBorderSoft, 10, 4)))
	}
	frame := canvas.NewRectangle(colSurface)
	frame.CornerRadius = rounded(12)
	frame.StrokeColor, frame.StrokeWidth = colBorderSoft, 1
	if id == panel.backgroundFocus {
		frame.StrokeColor = colAmber
	}
	return padded(0, 10, 0, 0, container.NewStack(frame, padded(12, 12, 14, 14, parts)))
}
