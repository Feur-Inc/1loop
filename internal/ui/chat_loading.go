package ui

import (
	"reflect"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

const chatLoadThreshold = 200

// Widget creation stays on Fyne's UI thread, but yields between batches so
// the waiting ring and navigation remain responsive during large imports.
type conversationLoad struct {
	chat      *conversation
	started   time.Time
	first     bool
	width     float32
	items     []agent.ChatItem
	states    []string
	positions []int
	checked   int
	measured  int
	copies    bool
}

func (ui *desktopUI) beginChatLoad(chat *conversation) {
	ui.loadingChat = &conversationLoad{chat: chat, started: time.Now(), first: true}
	ui.transcript.Hide()
	ui.welcome.Hide()
	// Show an empty scroll first, avoiding a recursive refresh of every
	// completed row when the finished conversation becomes visible.
	ui.scroll.Show()
	ui.chatWaitView.Show()
	ui.chatWait.setState("wait")
	ui.updateHeader()
	ui.renderChats()
}

func (ui *desktopUI) cancelChatLoad() {
	ui.loadingChat = nil
	if ui.chatWait != nil {
		ui.chatWait.setState("brand")
	}
	if ui.chatWaitView != nil {
		ui.chatWaitView.Hide()
	}
}

func (ui *desktopUI) advanceChatLoad() {
	load := ui.loadingChat
	if load == nil {
		return
	}
	if load.chat != ui.current || (ui.ctx != nil && ui.ctx.Err() != nil) {
		ui.cancelChatLoad()
		return
	}
	if load.first {
		load.first = false
		return // let the waiting state paint before starting the work
	}
	width := fyne.Min(760, fyne.Max(0, ui.chatViewport.Size().Width-48))
	if width <= 0 {
		return
	}
	if width != load.width {
		load.width, load.measured = width, 0
	}
	// A single message can exceed this soft budget; never move Fyne
	// widget operations to a worker goroutine to hide that cost.
	deadline := time.Now().Add(16 * time.Millisecond)
	ui.building = true
	defer func() { ui.building = false }()
	for time.Now().Before(deadline) && ui.loadingChat == load {
		if len(load.chat.Items) < len(load.items) {
			ui.reloadChat(load.chat)
			return
		}
		switch {
		case len(load.items) < len(load.chat.Items):
			index := len(load.items)
			position := len(ui.transcript.Objects)
			ui.appendItem(load.chat, index)
			if position == len(ui.transcript.Objects) {
				position = -1
			}
			load.positions = append(load.positions, position)
			load.items = append(load.items, snapshotChatItem(load.chat.Items[index]))
			load.states = append(load.states, runState(load.chat, index))
			load.copies = false
		case !load.copies:
			// Set copy-control visibility before measuring the rows, so the
			// final layout does not invalidate hundreds of cached heights.
			ui.syncCopies()
			load.copies = true
		case load.measured < len(ui.transcript.Objects):
			view := ui.transcript.Objects[load.measured]
			ui.items.measure(view, load.width)
			view.Hide()
			load.measured++
		case load.checked < len(load.items):
			index := load.checked
			item := load.chat.Items[index]
			state := runState(load.chat, index)
			if item.Kind != load.items[index].Kind {
				ui.reloadChat(load.chat)
				return
			}
			if !reflect.DeepEqual(item, load.items[index]) || state != load.states[index] {
				ui.replaceLoadingItem(load, index)
				load.items[index], load.states[index] = snapshotChatItem(item), state
			}
			if position := load.positions[index]; position >= 0 {
				view := ui.transcript.Objects[position]
				if _, known := ui.items.sizes[view]; !known {
					ui.items.measure(view, load.width)
					view.Hide()
				}
			}
			load.checked++
		default:
			ui.building = false
			ui.items.width = load.width // all cached heights use this width
			ui.rememberChatView(load)
			ui.cancelChatLoad()
			ui.finishChatLoad()
			canvas.Refresh(ui.transcript)
			platform.Devf("ui", "conversation ready: %d items in %s", len(load.items), time.Since(load.started).Round(time.Millisecond))
			return
		}
	}
}

func snapshotChatItem(item agent.ChatItem) agent.ChatItem {
	item.Images = slices.Clone(item.Images)
	item.Files = slices.Clone(item.Files)
	return item
}

func (ui *desktopUI) replaceLoadingItem(load *conversationLoad, index int) {
	position := load.positions[index]
	if position < 0 {
		return
	}
	old := ui.transcript.Objects[position]
	count := len(ui.transcript.Objects)
	ui.appendItem(load.chat, index)
	ui.transcript.Objects[position] = ui.transcript.Objects[count]
	ui.transcript.Objects = ui.transcript.Objects[:count]
	delete(ui.items.sizes, old)
}
