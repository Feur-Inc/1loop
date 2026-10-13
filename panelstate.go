package main

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
	Kind    string       `json:"kind"` // "tool", "todo", "changes", "terminal" or "browser"
	Browser *browserInfo `json:"browser,omitempty"`
	Index   int          `json:"index,omitempty"` // tool: the call's item in the conversation
}

// saved describes a state for the chat's file. A new value each time: the
// previous one may be being written.
func (s *panelState) saved() *savedPanel {
	if !s.open && len(s.tabs) == 0 && s.width == 0 {
		return nil
	}
	out := &savedPanel{Open: s.open, Width: s.width, Active: s.active}
	for _, tab := range s.tabs {
		saved := savedTab{Kind: tab.kind, Index: tab.index}
		if tab.browser != nil {
			info := tab.browser.snapshot()
			saved.Browser = &info
		}
		out.Tabs = append(out.Tabs, saved)
	}
	return out
}

// stateOf is a chat's panel; the first time, opened again from what was
// saved with it. nil is the start screen.
func (p *toolPanel) stateOf(chat *conversation) *panelState {
	if chat == nil {
		return &p.home
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
			opened = p.todoTab(chat)
		case "changes":
			opened = p.changesTab(chat.Config.Workspace)
		case "browser":
			info := browserInfo{Adaptive: true}
			if tab.Browser != nil {
				info = *tab.Browser
			}
			opened = p.browserTab(chat, info, state.tabs)
		case "terminal": // a new shell in the chat's folder: the old one ended with 1loop
			terminal, err := p.terminalTab(chat.Config.Workspace)
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
func (p *toolPanel) stash() {
	state := p.stateOf(p.owner)
	state.tabs, state.active, state.open, state.width = p.tabs, p.active, p.target > 0, p.width
	if p.owner != nil {
		p.owner.Panel = state.saved()
	}
}

// switchTo shows a chat's panel in place of the current one: same tabs,
// same width, open if it was. Nothing slides: the panel is simply the
// other chat's.
func (p *toolPanel) switchTo(chat *conversation) {
	p.stash()
	if chat == nil { // the start screen keeps what belongs to the folder it shows
		kept := p.home.tabs[:0]
		for _, tab := range p.home.tabs {
			if tab.workspace != "" && tab.workspace != p.u.cfg.Workspace {
				tab.dispose()
				continue
			}
			kept = append(kept, tab)
		}
		p.home.tabs = kept
	}
	p.owner = chat
	state := p.stateOf(chat)
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
	p.tabs, p.active, p.width = kept, active, state.width
	if p.anim != nil {
		p.anim.Stop()
	}
	p.shown, p.target, p.snap = 0, 0, false
	if state.open {
		p.shown, p.target = 1, 1
	}
	p.u.panelToggle.SetSelected(state.open)
	p.todoChanged(chat)
	p.render()
	relayout(p.u.split)
}

// adopt gives the panel of the start screen to the chat just begun there.
func (p *toolPanel) adopt(chat *conversation) {
	p.stash()
	state := p.home
	p.home = panelState{active: -1}
	for i := range state.tabs {
		if state.tabs[i].browser != nil {
			state.tabs[i].browser.view.chat = chat
		}
		if state.tabs[i].kind == "todo" {
			state.tabs[i].todoChat = chat
		}
	}
	chat.panel = &state
	p.owner, p.tabs = chat, state.tabs
}

// disposePanel ends what a chat's panel runs, when the chat goes.
func (c *conversation) disposePanel() {
	if c.panel != nil {
		for _, tab := range c.panel.tabs {
			tab.dispose()
		}
		c.panel = nil
	}
}

// showTodo opens the panel of a chat on its task list. A chat that is not
// on screen finds it that way when it is opened.
func (p *toolPanel) showTodo(chat *conversation) {
	if chat == p.owner {
		p.openTodo()
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
		state.tabs = append(state.tabs, p.todoTab(chat))
		at = len(state.tabs) - 1
	}
	state.open, state.active = true, at
	chat.Panel = state.saved()
}

// todoUpdated follows a chat's task list. The first time the agent puts
// tasks in it, the side panel opens on the list.
func (u *desktopUI) todoUpdated(chat *conversation) {
	count := len(chat.Todo.snapshot())
	first := chat.todoCount == 0 && count > 0
	chat.todoCount = count
	if first {
		devf("ui", "first tasks in %q: opening its task list", chat.Title)
		u.panel.showTodo(chat)
		u.save()
	}
	u.panel.todoChanged(chat)
}
