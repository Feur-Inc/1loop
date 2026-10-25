package ui

import (
	"1loop/internal/browser"
	"1loop/internal/platform"
)

// The side panel belongs to the chat. Each conversation keeps its own: open
// or folded, how wide, which tabs and which one is on show. Going to another
// chat puts this one's panel aside (its terminals keep running) and brings
// the other's back exactly as it was left; with the chat is saved enough to
// open the same tabs again after 1loop restarts.

// panelState is a chat's panel while 1loop runs.
type panelState struct {
	tabs   []panelTab
	active int
	open   bool
	width  float32 // 0: the default width
}

// savedPanel is what is written with the chat.
type savedPanel struct {
	Open   bool       `json:"open,omitempty"`
	Width  float32    `json:"width,omitempty"`
	Active int        `json:"active,omitempty"`
	Tabs   []savedTab `json:"tabs,omitempty"`
}

type savedTab struct {
	Kind    string        `json:"kind"` // "tool", "todo", "changes", "terminal" or "browser"
	Browser *browser.Info `json:"browser,omitempty"`
	Index   int           `json:"index,omitempty"` // tool: the call's item in the conversation
}

// saved describes a state for the chat's file. A new value each time: the
// previous one may be being written.
func (state *panelState) saved() *savedPanel {
	if !state.open && len(state.tabs) == 0 && state.width == 0 {
		return nil
	}
	out := &savedPanel{Open: state.open, Width: state.width, Active: state.active}
	for _, tab := range state.tabs {
		saved := savedTab{Kind: tab.kind, Index: tab.index}
		if tab.browser != nil {
			info := tab.browser.Snapshot()
			saved.Browser = &info
		}
		out.Tabs = append(out.Tabs, saved)
	}
	return out
}

// stateOf is a chat's panel; the first time, opened again from what was
// saved with it. nil is the start screen.
func (panel *toolPanel) stateOf(chat *conversation) *panelState {
	if chat == nil {
		return &panel.home
	}
	if chat.panel != nil {
		return chat.panel
	}
	state := &panelState{active: -1}
	chat.panel = state
	saved := chat.Panel
	if saved == nil {
		return state
	}
	state.open, state.width = saved.Open, saved.Width
	for i, tab := range saved.Tabs {
		var opened panelTab
		switch tab.Kind {
		case "tool":
			opened = panelTab{kind: "tool", chat: chat, index: tab.Index}
		case "todo":
			opened = panel.todoTab(chat)
		case "changes":
			opened = panel.changesTab(chat.Config.Workspace)
		case "files":
			opened = panel.filesTab(chat.Config.Workspace)
		case "browser":
			info := browser.Info{Adaptive: true}
			if tab.Browser != nil {
				info = *tab.Browser
			}
			opened = panel.browserTab(chat, info, state.tabs)
		case "terminal": // a new shell in the chat's folder: the old one ended with 1loop
			terminal, err := panel.terminalTab(chat.Config.Workspace)
			if err != nil {
				continue
			}
			opened = terminal
		default:
			continue
		}
		if i == saved.Active {
			state.active = len(state.tabs)
		}
		state.tabs = append(state.tabs, opened)
	}
	return state
}

// stash puts the panel on screen back into its chat's state.
func (panel *toolPanel) stash() {
	state := panel.stateOf(panel.owner)
	state.tabs, state.active, state.open, state.width = panel.tabs, panel.active, panel.target > 0, panel.width
	if panel.owner != nil {
		panel.owner.Panel = state.saved()
	}
}

// switchTo shows a chat's panel in place of the current one: same tabs,
// same width, open if it was. Nothing slides: the panel is simply the
// other chat's.
func (panel *toolPanel) switchTo(chat *conversation) {
	panel.stash()
	if chat == nil { // the start screen keeps what belongs to the folder it shows
		kept := panel.home.tabs[:0]
		for _, tab := range panel.home.tabs {
			if tab.workspace != "" && tab.workspace != panel.ui.cfg.Workspace {
				tab.dispose()
				continue
			}
			kept = append(kept, tab)
		}
		panel.home.tabs = kept
	}
	panel.owner = chat
	state := panel.stateOf(chat)
	// A call's tab only makes sense while its item is still that call.
	kept, active := state.tabs[:0], -1
	for i, tab := range state.tabs {
		if tab.kind == "tool" && (chat == nil || tab.index >= len(chat.Items) || chat.Items[tab.index].Kind != "tool") {
			continue
		}
		if i == state.active {
			active = len(kept)
		}
		kept = append(kept, tab)
	}
	if active < 0 && len(kept) > 0 {
		active = 0
	}
	panel.tabs, panel.active, panel.width = kept, active, state.width
	if panel.anim != nil {
		panel.anim.Stop()
	}
	panel.shown, panel.target, panel.snap = 0, 0, false
	if state.open {
		panel.shown, panel.target = 1, 1
	}
	panel.ui.panelToggle.SetSelected(state.open)
	panel.todoChanged(chat)
	if panel.ui.cfg.Provider == "chatgptweb" {
		panel.ui.ensureWebTab()
	}
	panel.render()
	relayout(panel.ui.split)
}

// adopt gives the panel of the start screen to the chat just begun there.
func (panel *toolPanel) adopt(chat *conversation) {
	panel.stash()
	state := panel.home
	panel.home = panelState{active: -1}
	for i := range state.tabs {
		if state.tabs[i].browser != nil {
			state.tabs[i].browser.View.(*browserView).chat = chat
		}
		if state.tabs[i].kind == "todo" {
			state.tabs[i].todoChat = chat
		}
	}
	chat.panel = &state
	panel.owner, panel.tabs = chat, state.tabs
}

// disposePanel ends what a chat's panel runs, when the chat goes.
func (chat *conversation) disposePanel() {
	if chat.panel != nil {
		for _, tab := range chat.panel.tabs {
			tab.dispose()
		}
		chat.panel = nil
	}
}

// showTodo opens the panel of a chat on its task list. A chat that is not
// on screen finds it that way when it is opened.
func (panel *toolPanel) showTodo(chat *conversation) {
	if chat == panel.owner {
		panel.openTodo()
		return
	}
	if chat.panel == nil { // not opened since 1loop started: only what was saved changes
		saved := savedPanel{}
		if chat.Panel != nil {
			saved = *chat.Panel
			saved.Tabs = append([]savedTab{}, saved.Tabs...)
		}
		at := -1
		for i, tab := range saved.Tabs {
			if tab.Kind == "todo" {
				at = i
			}
		}
		if at < 0 {
			saved.Tabs = append(saved.Tabs, savedTab{Kind: "todo"})
			at = len(saved.Tabs) - 1
		}
		saved.Open, saved.Active = true, at
		chat.Panel = &saved
		return
	}
	state := chat.panel
	at := -1
	for i, tab := range state.tabs {
		if tab.kind == "todo" {
			at = i
		}
	}
	if at < 0 {
		state.tabs = append(state.tabs, panel.todoTab(chat))
		at = len(state.tabs) - 1
	}
	state.open, state.active = true, at
	chat.Panel = state.saved()
}

// todoUpdated follows a chat's task list. The first time the agent puts
// tasks in it, the side panel opens on the list.
func (ui *desktopUI) todoUpdated(chat *conversation) {
	count := len(chat.Todo.Snapshot())
	first := chat.todoCount == 0 && count > 0
	chat.todoCount = count
	if first {
		platform.Devf("ui", "first tasks in %q: opening its task list", chat.Title)
		ui.panel.showTodo(chat)
		ui.save()
	}
	ui.panel.todoChanged(chat)
}
