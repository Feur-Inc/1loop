package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

type chatSnapshot = agent.VersionSnapshot
type chatVersion = agent.ChatVersion

func (chat *conversation) branching() agent.VersionState {
	return agent.VersionState{Config: chat.Config, History: chat.History, Items: chat.Items, Version: chat.Version, Versions: chat.Versions}
}
func (chat *conversation) applyBranch(state agent.VersionState) {
	chat.Config, chat.History, chat.Items = state.Config, state.History, state.Items
	chat.Version, chat.Versions = state.Version, state.Versions
}
func (chat *conversation) identifyMessages() {
	state := chat.branching()
	state.IdentifyMessages()
	chat.applyBranch(state)
}
func (chat *conversation) versionFamily(anchor string) ([]int, int) {
	state := chat.branching()
	return state.Family(anchor)
}
func (chat *conversation) forkMessage(index int, response bool) string {
	state := chat.branching()
	id := state.ForkMessage(index, response)
	chat.applyBranch(state)
	chat.queue, chat.steer = nil, nil
	return id
}
func (chat *conversation) selectVersion(anchor string, delta int) bool {
	state := chat.branching()
	if !state.SelectVersion(anchor, delta) {
		return false
	}
	chat.applyBranch(state)
	chat.queue, chat.steer = nil, nil
	return true
}
func copyVersions(versions []chatVersion) []chatVersion { return agent.CopyVersions(versions) }

func userItemBefore(chat *conversation, index int) int {
	for i := index; i >= 0; i-- {
		if chat.Items[i].Kind == "user" {
			return i
		}
	}
	return -1
}

func (ui *desktopUI) versionReady(chat *conversation) bool {
	if chat != ui.current || chat.run != nil {
		ui.notice("Response in progress", "Stop the current response before editing or switching versions.")
		return false
	}
	return true
}

func (ui *desktopUI) startVersion(chat *conversation, index int, messageValue agent.Message, response bool) bool {
	if !ui.versionReady(chat) || !ui.ready() {
		return false
	}
	ui.finishEdit(false)
	id := chat.forkMessage(index, response)
	ui.reloadChat(chat)
	ui.startWithID(messageValue, id)
	ui.remote.replaceChat(chat)
	return true
}

func (ui *desktopUI) editMessage(chat *conversation, index int) {
	if chat.server == nil && !ui.versionReady(chat) {
		return
	}
	item := chat.Items[index]
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(5)
	entry.SetText(item.Text)
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	send := newRow("Save & send", nil, stylePrimary, func() {
		text := strings.TrimSpace(entry.Text)
		if text == "" && len(item.Images) == 0 {
			return
		}
		if chat.server != nil {
			chat.server.action("/api/chats/"+chat.serverID+"/message", map[string]any{"index": index, "action": "edit", "text": text})
			dialog.Hide()
			return
		}
		if ui.startVersion(chat, index, agent.Message{Role: "user", Content: text, Images: item.Images}, false) {
			dialog.Hide()
		}
	})
	dialog = ui.modal("Edit message", container.NewVBox(entry, note("A new version will start here. The previous conversation remains available with the version arrows.")), 600, nil, cancel, send)
	ui.window.Canvas().Focus(entry)
}

func (ui *desktopUI) retryMessage(chat *conversation, index int) {
	if chat.server != nil {
		chat.server.action("/api/chats/"+chat.serverID+"/message", map[string]any{"index": index, "action": "retry"})
		return
	}
	if at := userItemBefore(chat, index); at >= 0 {
		item := chat.Items[at]
		ui.startVersion(chat, at, agent.Message{Role: "user", Content: item.Text, Images: item.Images}, true)
	}
}

func (ui *desktopUI) versionControls(chat *conversation, anchor string) fyne.CanvasObject {
	family, selected := chat.versionFamily(anchor)
	if chat.server != nil { // the server keeps the versions: it sends where this one stands
		version := chat.versions[anchor]
		family, selected = make([]int, version.Total), version.Current-1
	}
	if len(family) < 2 {
		return spacer(0, 0)
	}
	move := func(delta int) {
		if chat.server != nil {
			chat.server.action("/api/chats/"+chat.serverID+"/version", map[string]any{"anchor": anchor, "delta": delta})
			return
		}
		if !ui.versionReady(chat) {
			return
		}
		ui.finishEdit(false)
		if chat.selectVersion(anchor, delta) {
			ui.reloadChat(chat)
			ui.save()
			ui.remote.replaceChat(chat)
		}
	}
	previous := newRow("", iconChevronLeft, styleChip, func() { move(-1) })
	next := newRow("", iconChevronRight, styleChip, func() { move(1) })
	if selected == 0 {
		previous.Disable()
	}
	if selected == len(family)-1 {
		next.Disable()
	}
	return container.New(layout.NewCustomPaddedHBoxLayout(0), previous,
		container.NewCenter(textLine(fmt.Sprintf("%d / %d", selected+1, len(family)), 12, colMuted)), next)
}

func (ui *desktopUI) responseControls(chat *conversation, index int) *fyne.Container {
	controls := container.NewHBox(copyChip(func() string {
		if chat.Items[index].Kind == "error" {
			return chat.Items[index].Text
		}
		return answerText(chat, index)
	}))
	if at := userItemBefore(chat, index); at >= 0 {
		controls.Add(newRow("Retry", iconArrowTurn, styleChip, func() { ui.retryMessage(chat, index) }))
		controls.Add(ui.versionControls(chat, chat.Items[at].ID+":reply"))
	}
	return controls
}
