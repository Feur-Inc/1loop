//go:build !server

package main

import (
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// Only inactive versions hold snapshots. The active conversation remains
// the source used by the runner, persistence and remote views.
type chatSnapshot struct {
	Config  config     `json:"config"`
	History []message  `json:"history"`
	Items   []chatItem `json:"items"`
}

type chatVersion struct {
	ID       string            `json:"id"`
	Parent   string            `json:"parent,omitempty"`
	Fork     string            `json:"fork,omitempty"`
	Snapshot *chatSnapshot     `json:"snapshot,omitempty"`
	Selected map[string]string `json:"selected,omitempty"` // fork -> last continuation visited
}

func (chat *conversation) identifyMessages() {
	for i := range chat.Items {
		if chat.Items[i].ID == "" {
			chat.Items[i].ID = identifier()[:16]
		}
	}
}

func (chat *conversation) versionIndex(id string) int {
	return slices.IndexFunc(chat.Versions, func(chatVersionValue chatVersion) bool { return chatVersionValue.ID == id })
}

// Edits of one prompt (or retries of its response) form a family. Later
// forks at another message stay inside their chosen family's continuation.
func (chat *conversation) versionFamily(anchor string) (family []int, selected int) {
	edges := make([][]int, len(chat.Versions))
	for i, chatVersion := range chat.Versions {
		if chatVersion.Fork == anchor {
			if parent := chat.versionIndex(chatVersion.Parent); parent >= 0 {
				edges[i] = append(edges[i], parent)
				edges[parent] = append(edges[parent], i)
			}
		}
	}
	member := chat.versionIndex(chat.Version)
	for member >= 0 && len(edges[member]) == 0 {
		if strings.HasSuffix(anchor, ":reply") && chat.Versions[member].Fork == strings.TrimSuffix(anchor, ":reply") {
			return nil, 0 // retries of another wording belong to that prompt's version
		}
		member = chat.versionIndex(chat.Versions[member].Parent)
	}
	if member < 0 {
		return nil, 0
	}
	seen := map[int]bool{member: true}
	queue := []int{member}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, next := range edges[at] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	for i := range chat.Versions {
		if seen[i] {
			if i == member {
				selected = len(family)
			}
			family = append(family, i)
		}
	}
	return family, selected
}

func (chat *conversation) rememberVersion() {
	anchors := map[string]bool{}
	for _, chatVersion := range chat.Versions {
		if chatVersion.Fork != "" {
			anchors[chatVersion.Fork] = true
		}
	}
	for anchor := range anchors {
		prompt := strings.TrimSuffix(anchor, ":reply")
		if !slices.ContainsFunc(chat.Items, func(item chatItem) bool { return item.Kind == "user" && item.ID == prompt }) {
			continue // an edit of an earlier prompt removed this later branch point
		}
		family, selected := chat.versionFamily(anchor)
		if len(family) > 1 {
			chatVersionValue := &chat.Versions[family[selected]]
			if chatVersionValue.Selected == nil {
				chatVersionValue.Selected = map[string]string{}
			}
			chatVersionValue.Selected[anchor] = chat.Version
		}
	}
	chat.Versions[chat.versionIndex(chat.Version)].Snapshot = &chatSnapshot{
		Config: chat.Config, History: append([]message{}, chat.History...), Items: append([]chatItem{}, chat.Items...),
	}
}

// Match user messages backwards: this also works after compaction removes
// an earlier prefix, and disambiguates repeated prompts such as "continue".
func (chat *conversation) historyBefore(index int) []message {
	cursor := len(chat.History)
	for i := len(chat.Items) - 1; i >= index; i-- {
		item := chat.Items[i]
		if item.Kind != "user" {
			continue
		}
		found := -1
		for j := cursor - 1; j >= 0; j-- {
			messageValue := chat.History[j]
			if messageValue.Role == "user" && messageValue.Content == item.Text && slices.Equal(messageValue.Images, item.Images) {
				found = j
				break
			}
		}
		if found < 0 {
			break
		}
		cursor = found
		if i == index {
			return append([]message{}, chat.History[:cursor]...)
		}
	}
	// Older messages may exist only in the transcript after compaction.
	// Rebuild that visible prefix without pulling in the discarded future.
	var history []message
	for i, item := range chat.Items[:index] {
		switch item.Kind {
		case "user", "assistant":
			history = append(history, message{Role: item.Kind, Content: item.Text, Images: item.Images})
		case "tool":
			call := toolCall{ID: fmt.Sprintf("earlier-%d", i), Type: "function"}
			call.Function.Name, call.Function.Arguments = item.Tool, item.Args
			if call.Function.Arguments == "" {
				call.Function.Arguments = "{}"
			}
			history = append(history, message{Role: "assistant", ToolCalls: []toolCall{call}},
				message{Role: "tool", ToolCallID: call.ID, Content: item.Text})
		}
	}
	return history
}

func (chat *conversation) forkMessage(index int, response bool) string {
	chat.identifyMessages()
	if chat.Version == "" {
		chat.Version = identifier()[:16]
		chat.Versions = []chatVersion{{ID: chat.Version}}
	}
	chat.rememberVersion()
	anchor := chat.Items[index].ID
	if response {
		anchor += ":reply"
	}
	parent := chat.Version
	if family, selected := chat.versionFamily(anchor); len(family) > 1 {
		parent = chat.Versions[family[selected]].ID
	}
	chat.Version = identifier()[:16]
	chat.Versions = append(chat.Versions, chatVersion{ID: chat.Version, Parent: parent, Fork: anchor})
	chat.History = chat.historyBefore(index)
	id := chat.Items[index].ID
	chat.Items = append([]chatItem{}, chat.Items[:index]...)
	chat.Config.Session = "" // Claude must not resume a session containing the discarded future.
	chat.queue, chat.steer = nil, nil
	return id
}

func (chat *conversation) selectVersion(anchor string, delta int) bool {
	family, selected := chat.versionFamily(anchor)
	to := selected + delta
	if to < 0 || to >= len(family) {
		return false
	}
	chat.rememberVersion()
	target := family[to]
	if leaf := chat.versionIndex(chat.Versions[target].Selected[anchor]); leaf >= 0 {
		target = leaf
	}
	chatVersion := &chat.Versions[target]
	if chatVersion.Snapshot == nil {
		return false
	}
	chat.Version, chat.Config = chatVersion.ID, chatVersion.Snapshot.Config
	chat.History = append([]message{}, chatVersion.Snapshot.History...)
	chat.Items = append([]chatItem{}, chatVersion.Snapshot.Items...)
	chatVersion.Snapshot = nil
	chat.queue, chat.steer = nil, nil
	return true
}

func copyVersions(versions []chatVersion) []chatVersion {
	copy := append([]chatVersion{}, versions...)
	for i, chatVersionValue := range copy {
		if chatVersionValue.Selected != nil {
			copy[i].Selected = map[string]string{}
			for anchor, id := range chatVersionValue.Selected {
				copy[i].Selected[anchor] = id
			}
		}
	}
	return copy
}

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

func (ui *desktopUI) startVersion(chat *conversation, index int, messageValue message, response bool) bool {
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
	if !ui.versionReady(chat) {
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
		if ui.startVersion(chat, index, message{Role: "user", Content: text, Images: item.Images}, false) {
			dialog.Hide()
		}
	})
	dialog = ui.modal("Edit message", container.NewVBox(entry, note("A new version will start here. The previous conversation remains available with the version arrows.")), 600, nil, cancel, send)
	ui.window.Canvas().Focus(entry)
}

func (ui *desktopUI) retryMessage(chat *conversation, index int) {
	if at := userItemBefore(chat, index); at >= 0 {
		item := chat.Items[at]
		ui.startVersion(chat, at, message{Role: "user", Content: item.Text, Images: item.Images}, true)
	}
}

func (ui *desktopUI) versionControls(chat *conversation, anchor string) fyne.CanvasObject {
	family, selected := chat.versionFamily(anchor)
	if len(family) < 2 {
		return spacer(0, 0)
	}
	move := func(delta int) {
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
