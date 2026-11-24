// SPDX-License-Identifier: MPL-2.0

package ui

// How a server window keeps up with its server (#591): the server's events
// are applied as they come (an item changes its row, a meta the chat's
// state, chats and projects the sidebar, settings the models), and the
// state or a chat is read in full only when that is needed: on connecting
// or reconnecting, on opening a chat, on "replace", or when an event cannot
// be applied (an item past the chat's end, a chat not read yet). An older
// server that sends fewer events still works: what an action changes is
// read again after it.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
	"1loop/internal/sse"
)

// What refreshLoop reads at its next turn.
const (
	wantState = 1 << iota // the server's state: its chats, projects and settings
	wantChat              // the open chat's items and meta
	wantPanel             // the open chat's tabs
)

// remoteSync is what the window keeps of the server between its events.
type remoteSync struct {
	wanted   atomic.Int32
	cache    map[string]*remoteChatCache // the chats opened in this session, by their ID on the server
	listed   []string                    // the projects the server lists
	stateTag string                      // the ETag of the last state read
	answered bool                        // its state came once: the sidebar lists its chats
	seqs     map[string]uint64           // the last applied event of each kind ("chats", "projects", "settings")
	pending  map[string]*remotePending   // the actions on their way, by key (remote_desktop_actions.go)
	inflight int                         // the requests on their way
	slow     int                         // counts the activity line's timers: only the last one shows it
}

// remoteChatCache is what the window holds of a server chat beside its
// conversation (whose items are the chat's as last seen).
type remoteChatCache struct {
	loaded  bool   // its items came once
	tag     string // the ETag of the last full read, for the next one
	meta    remoteapi.Meta
	hasMeta bool
	tabs    []remoteapi.Tab
	hasTabs bool
	reading bool          // a full read is on its way
	during  []serverEvent // the chat's events meanwhile, applied again over the read
}

// serverEvent is an event of the server, with its number (0 from a server
// older than #591).
type serverEvent struct {
	kind string
	seq  uint64
	data string
}

func (remote *remoteDesktop) chatCache(id string) *remoteChatCache {
	if remote.live.cache == nil {
		remote.live.cache = map[string]*remoteChatCache{}
	}
	cache := remote.live.cache[id]
	if cache == nil {
		cache = &remoteChatCache{}
		remote.live.cache[id] = cache
	}
	return cache
}

// want asks refreshLoop to read parts at its next turn.
func (remote *remoteDesktop) want(parts int) {
	remote.live.wanted.Or(int32(parts))
	select {
	case remote.wake <- struct{}{}:
	default:
	}
}

// notify reads everything again: the state, and the open chat with its tabs.
func (remote *remoteDesktop) notify() {
	remote.want(wantState | wantChat | wantPanel)
}

func (remote *remoteDesktop) events() {
	for remote.ctx.Err() == nil {
		response, err := remote.client.request(remote.ctx, "GET", "/api/events", nil)
		if err == nil {
			fyne.Do(remote.eventsConnected)
			for event, err := range sse.Events(response.Body, 32<<20) {
				if err != nil {
					break
				}
				if event.Type == "pairing" {
					remote.askPairing(event.Data)
					continue
				}
				seq, _ := strconv.ParseUint(event.ID, 10, 64)
				received := serverEvent{event.Type, seq, event.Data}
				fyne.Do(func() {
					if remote.ctx.Err() == nil && remote.parent.server == remote {
						remote.apply(received)
					}
				})
			}
			response.Body.Close()
		}
		remote.want(wantState) // says whether it still answers
		select {
		case <-remote.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// apply brings an event of the server to the window, on its thread.
func (remote *remoteDesktop) apply(event serverEvent) {
	switch event.kind {
	case "item":
		var changed struct {
			Chat  string
			Index int
			Item  remoteapi.Item
		}
		if json.Unmarshal([]byte(event.data), &changed) != nil {
			remote.want(wantChat)
			return
		}
		if cache := remote.live.cache[changed.Chat]; cache != nil && cache.reading {
			cache.during = append(cache.during, event)
		}
		if !remote.applyItem(changed.Chat, changed.Index, changed.Item) && changed.Chat == remote.current {
			remote.want(wantChat) // read in full: the safety net
		}
		if changed.Chat == remote.current && (strings.HasPrefix(changed.Item.Tool, "browser") || strings.HasPrefix(changed.Item.Tool, "terminal")) {
			remote.want(wantPanel) // the agent may have opened or closed a tab
		}
	case "meta":
		var meta remoteapi.Meta
		if json.Unmarshal([]byte(event.data), &meta) != nil || meta.Chat == "" {
			return
		}
		if cache := remote.live.cache[meta.Chat]; cache != nil && cache.reading {
			cache.during = append(cache.during, event)
		}
		remote.applyMeta(meta)
	case "replace":
		var replaced struct{ Chat string }
		if json.Unmarshal([]byte(event.data), &replaced) == nil && replaced.Chat == remote.current {
			remote.want(wantChat)
		}
	case "chats":
		var listed struct{ Chats []remoteapi.Chat }
		if json.Unmarshal([]byte(event.data), &listed) != nil || listed.Chats == nil {
			return
		}
		remote.markSeq("chats", event.seq)
		if !remote.live.answered {
			// Kept for the first state, which may be older (a chat made
			// while it was on its way): applyState keeps the newer (#600).
			remote.chats = listed.Chats
			return
		}
		remote.show(remote.name, remote.defaultWorkspace, listed.Chats, remote.live.listed)
	case "projects":
		var listed struct {
			Projects  []string
			Worktrees map[string]string
		}
		if json.Unmarshal([]byte(event.data), &listed) != nil {
			return
		}
		remote.markSeq("projects", event.seq)
		remote.worktrees, remote.live.listed = listed.Worktrees, listed.Projects
		if !remote.live.answered {
			return // kept for the first state, as chats are
		}
		remote.show(remote.name, remote.defaultWorkspace, remote.chats, listed.Projects)
	case "settings":
		var settings remoteSettings
		if json.Unmarshal([]byte(event.data), &settings) != nil {
			return
		}
		remote.markSeq("settings", event.seq)
		remote.applySettings(settings)
		remote.want(wantState) // the rest of the state may follow the settings
	}
}

func (remote *remoteDesktop) markSeq(kind string, seq uint64) {
	if remote.live.seqs == nil {
		remote.live.seqs = map[string]uint64{}
	}
	remote.live.seqs[kind] = max(remote.live.seqs[kind], seq)
}

// fresh says whether a part of a read taken at seq is newer than the
// last event of its kind applied (always, from an older server).
func (remote *remoteDesktop) fresh(kind string, seq uint64) bool {
	return seq == 0 || seq >= remote.live.seqs[kind]
}

// applyItem brings one item to the chat's conversation, shown or not. It
// says whether it could: false asks for the chat in full.
func (remote *remoteDesktop) applyItem(chat string, index int, item remoteapi.Item) bool {
	cache, conv := remote.live.cache[chat], remote.convs[chat]
	if cache == nil || !cache.loaded || conv == nil || index < 0 || index > len(conv.Items) {
		return cache != nil && cache.reading // the read on its way brings it, and the event is applied again over it
	}
	if versions := item.Versions; versions != nil && conv.versions[versions.Anchor] != *versions {
		return false // a new version: the chat is drawn again
	}
	next, state := chatItem(item), ""
	if item.Kind == "tool" {
		state = item.State
	}
	ui := remote.parent
	shown := ui.current == conv
	if index < len(conv.Items) {
		old := conv.Items[index]
		if item.Images > 0 && len(old.Images) > 0 && old.ID == item.ID {
			next.Images = old.Images // fetched already
		}
		if !shown {
			conv.Items[index], conv.states[index] = next, state
		} else if !remote.updateRow(conv, index, next, state) {
			conv.Items[index], conv.states[index] = next, state
			ui.reloadChat(conv)
		}
	} else {
		conv.Items = append(conv.Items, next)
		conv.states[index] = state
		if shown {
			ui.appendItem(conv, index)
			ui.syncCopies()
			ui.relayoutColumn()
		}
	}
	remote.fetchImage(conv, index, item)
	return true
}

// applyMeta keeps a chat's meta, and shows it when the chat is open.
func (remote *remoteDesktop) applyMeta(meta remoteapi.Meta) {
	if cache := remote.live.cache[meta.Chat]; cache != nil {
		cache.meta, cache.hasMeta = meta, true
	}
	if meta.Chat != remote.current {
		return
	}
	remote.meta = meta
	conv := remote.conv(meta.Chat)
	conv.Config.Model, conv.Config.Effort = meta.Model, meta.Effort // shown at once when it opens again
	remote.parent.model.row.SetText(remote.modelText(meta.Model, meta.Effort))
	remote.showMeta(conv)
	remote.renderTasks()
}

// remoteSettings are the server's settings, in its state and its settings
// event.
type remoteSettings struct {
	Models                      []remoteapi.Model
	Accounts                    []remoteapi.Account
	DefaultPresets              bool
	Model, Effort, ApprovalMode string
	Tier                        string
	AutoMode                    bool // the server's model has an Auto mode
	NamingModel                 *agent.NamingModel
	Loading                     bool // its accounts' models are still being read
}

func (remote *remoteDesktop) applySettings(settings remoteSettings) {
	if len(settings.Models) > 0 || !settings.Loading { // kept while the server reads them again
		remote.models = settings.Models
	}
	remote.accounts, remote.defaultPresets = settings.Accounts, settings.DefaultPresets
	remote.model, remote.effort, remote.tier = settings.Model, settings.Effort, settings.Tier
	remote.approvalMode, remote.autoMode = settings.ApprovalMode, settings.AutoMode
	remote.namingModel = settings.NamingModel
	remote.parent.updateApprovalControl()
	if remote.current == "" {
		remote.parent.model.row.SetText(remote.modelText(remote.model, remote.effort))
	}
}

// remoteState is the server's /api/state.
type remoteState struct {
	Name, Workspace string
	Chats           []remoteapi.Chat
	Projects        *[]string // nil from a server that lists them only at /api/projects
	Worktrees       map[string]string
	Settings        remoteSettings
}

// remoteChatRead is a full read of a chat.
type remoteChatRead struct {
	changed bool // false: the copy tagged as asked is still the server's
	tag     string
	seq     uint64
	items   []remoteapi.Item
	meta    remoteapi.Meta
	err     error
}

func (remote *remoteDesktop) readChat(ctx context.Context, id, tag string) remoteChatRead {
	var chat struct {
		Items []remoteapi.Item
		Meta  remoteapi.Meta
	}
	read := remoteChatRead{}
	read.changed, read.tag, read.seq, read.err = remote.client.snapshot(ctx, "/api/chats/"+id, tag, &chat)
	read.items, read.meta = chat.Items, chat.Meta
	return read
}

// refreshLoop reads what was asked (at most every 100 ms), and shows it.
func (remote *remoteDesktop) refreshLoop() {
	for {
		select {
		case <-remote.ctx.Done():
			return
		case <-remote.wake:
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-remote.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		parts := int(remote.live.wanted.Swap(0))
		var id, tag, stateTag string
		fyne.DoAndWait(func() {
			remote.busy(1)
			id, stateTag = remote.current, remote.live.stateTag
			if id != "" && parts&wantChat != 0 {
				cache := remote.chatCache(id)
				cache.reading, cache.during = true, nil
				if cache.loaded {
					tag = cache.tag
				}
			}
		})
		if id == "" {
			parts &^= wantChat | wantPanel
		}
		if parts == 0 {
			fyne.Do(func() { remote.busy(-1) })
			continue
		}
		ctx, cancel := context.WithTimeout(remote.ctx, 10*time.Second)
		var state remoteState
		var stateRead remoteChatRead // its tag and number
		var stateErr error           // whether the server answers (an open chat's error may only be the chat's)
		if parts&wantState != 0 {
			stateRead.changed, stateRead.tag, stateRead.seq, stateErr = remote.client.snapshot(ctx, "/api/state", stateTag, &state)
			if stateErr == nil && stateRead.changed && state.Projects == nil {
				var projects struct{ Projects []string }
				remote.client.json(ctx, "GET", "/api/projects", nil, &projects) // an older server has none to list
				state.Projects = &projects.Projects
			}
		}
		var read remoteChatRead
		if parts&wantChat != 0 && stateErr == nil {
			read = remote.readChat(ctx, id, tag)
		}
		var tabs struct{ Tabs []remoteapi.Tab }
		var tabsErr error
		if parts&wantPanel != 0 && stateErr == nil {
			tabsErr = remote.client.json(ctx, "GET", "/api/chats/"+id+"/panel", nil, &tabs)
		}
		cancel()
		if remote.ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			remote.busy(-1)
			if remote.ctx.Err() != nil || remote.parent.server != remote {
				return
			}
			if parts&wantState != 0 {
				remote.applyState(state, stateRead, stateErr)
			}
			if parts&wantChat != 0 {
				if stateErr != nil {
					read.err = stateErr
				}
				remote.applyRead(id, read)
			}
			if parts&wantPanel != 0 && stateErr == nil && tabsErr == nil {
				remote.applyTabs(id, tabs.Tabs)
			}
			if err := read.err; err == nil && stateErr == nil && tabsErr != nil {
				remote.status.SetText(tabsErr.Error())
			}
		})
	}
}

// applyState shows the server's state: whether it answers, its chats and
// projects in the sidebar, its settings.
func (remote *remoteDesktop) applyState(state remoteState, read remoteChatRead, err error) {
	target := serverTarget(remote.client.address.URL)
	if err != nil {
		remote.parent.setReach(target, reachOffline, err.Error())
		remote.status.SetText(err.Error())
		remote.connected = "" // the next answer says it is connected again
		return
	}
	remote.parent.setReach(target, reachOnline, "")
	if !read.changed && remote.live.answered {
		return // as the server last said
	}
	remote.live.stateTag = read.tag
	seq := read.seq
	chats, projects := remote.chats, remote.live.listed
	if remote.fresh("chats", seq) { // else a newer event's, even before the first state
		chats = state.Chats
	}
	if remote.fresh("projects", seq) {
		remote.worktrees, projects = state.Worktrees, *state.Projects
		remote.live.listed = projects
	}
	first := !remote.live.answered
	remote.live.answered = true
	remote.show(state.Name, state.Workspace, chats, projects)
	if first {
		remote.parent.renderChats() // its chats, no longer "Connecting…"
	}
	if remote.fresh("settings", seq) {
		remote.applySettings(state.Settings)
	}
}

// applyRead shows a chat read in full, then the events that came while it
// was on its way, which are newer.
func (remote *remoteDesktop) applyRead(id string, read remoteChatRead) {
	cache := remote.chatCache(id)
	during := cache.during
	cache.reading, cache.during = false, nil
	if read.err != nil {
		if id == remote.current {
			remote.status.SetText(read.err.Error())
			remote.connected = ""
			go func() { // tried again, unless the chat is left
				select {
				case <-remote.ctx.Done():
				case <-time.After(2 * time.Second):
					remote.want(wantChat)
				}
			}()
		}
		return
	}
	conv := remote.conv(id)
	if read.changed {
		cache.tag = read.tag
		remote.showItems(conv, read.items)
		if !cache.loaded && id == remote.current {
			remote.showLoading(false)
		}
		cache.loaded = true
		remote.applyMeta(read.meta)
	}
	for _, event := range during {
		if read.seq == 0 || event.seq > read.seq {
			remote.apply(event)
		}
	}
}

// applyTabs keeps a chat's tabs, and shows them when the chat is open.
func (remote *remoteDesktop) applyTabs(id string, tabs []remoteapi.Tab) {
	cache := remote.chatCache(id)
	cache.tabs, cache.hasTabs = tabs, true
	if id == remote.current {
		remote.syncTabs(remote.conv(id), tabs)
	}
}

// eventsConnected reads everything: the events stream is connected, or
// again, and what changed meanwhile came in no event. The numbers of the
// events seen are forgotten: a server that restarted numbers them from 1
// again, and every read of its state would seem older than them (#600).
func (remote *remoteDesktop) eventsConnected() {
	clear(remote.live.seqs)
	remote.notify()
}
