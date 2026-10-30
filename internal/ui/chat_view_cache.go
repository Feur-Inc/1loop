package ui

import (
	"reflect"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

// Retain at most one idle local transcript, not one per chat. A different
// large chat drops this tree before building its own, limiting peak memory.
type conversationViewCache struct {
	chat    *conversation
	items   []agent.ChatItem
	objects []fyne.CanvasObject
	views   map[int]any
	copies  map[int]*fyne.Container
	sizes   map[fyne.CanvasObject]fyne.Size
	width   float32
	scale   float32
}

func (ui *desktopUI) rememberChatView(load *conversationLoad) {
	chat := load.chat
	if chat.server != nil || chat.run != nil || len(chat.Versions) != 0 || len(chat.Items) > 10000 {
		return
	}
	ui.cachedChat = &conversationViewCache{
		chat: chat, items: load.items, objects: ui.transcript.Objects,
		views: ui.views, copies: ui.copyRows, sizes: ui.items.sizes,
		width: load.width, scale: ui.window.Canvas().Scale(),
	}
}

func (ui *desktopUI) restoreChatView(chat *conversation) bool {
	cache := ui.cachedChat
	if cache == nil || cache.chat != chat {
		return false
	}
	if chat.server != nil || chat.run != nil || len(chat.Versions) != 0 {
		ui.cachedChat = nil
		return false
	}
	width := fyne.Min(760, fyne.Max(0, ui.chatViewport.Size().Width-48))
	if width != cache.width || ui.window.Canvas().Scale() != cache.scale || !reflect.DeepEqual(chat.Items, cache.items) {
		ui.cachedChat = nil
		return false
	}
	ui.transcript.Hide()
	ui.scroll.Show() // refresh only the empty transcript, before restoring it
	ui.transcript.Objects, ui.views, ui.copyRows = cache.objects, cache.views, cache.copies
	ui.items.sizes, ui.items.width = cache.sizes, cache.width
	for _, object := range cache.objects {
		if row, ok := object.(*toolRow); ok && row.hover != 0 {
			if row.anim != nil {
				row.anim.Stop()
			}
			row.hover = 0
			row.Refresh()
		}
	}
	ui.panel.syncRows()
	return true
}
