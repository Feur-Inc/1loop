// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// Loopy living on this computer (internal/loopy/engine). The app is its
// host: models, browser, projects, notifications. Its views in this app
// reach it directly, the paired phone over the phone server's /api/loopy.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/githubconnector"
	"1loop/internal/loopy"
	"1loop/internal/loopy/engine"
)

// loopyHome is the Loopy of this computer.
type loopyHome struct {
	mu      sync.Mutex
	engine  *engine.Engine
	closed  bool
	chat    *conversation // hidden: what runConfig and its browser need
	events  chan func(*engine.Engine)
	browser *browser.Engine // its own
}

func (app *desktopApp) loopyDir() string {
	if app.statePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(app.statePath), "loopy")
}

// loopyEngine is this computer's Loopy, woken on first use. Never on the
// interface's thread: what it asks of the app goes there.
func (ui *desktopUI) loopyEngine() (*engine.Engine, error) {
	home := &ui.loopyHome
	home.mu.Lock()
	defer home.mu.Unlock()
	if home.engine != nil {
		return home.engine, nil
	}
	if home.closed || ui.ctx.Err() != nil {
		return nil, errors.New("1loop is closing")
	}
	if ui.loopyDir() == "" {
		return nil, errors.New("Loopy needs a place to live: a server")
	}
	// What an earlier version kept beside the state goes with it.
	moveLoopyFile(filepath.Join(filepath.Dir(ui.statePath), "loopy.json"), filepath.Join(ui.loopyDir(), "loopy.json"))
	home.chat = &conversation{ID: loopyChatID, Title: "Loopy"}
	woken, err := engine.Open(ui.ctx, ui.loopyDir(), loopyDesktop{ui})
	if err != nil {
		return nil, err
	}
	home.engine = woken
	home.events = make(chan func(*engine.Engine), 1024)
	go func() {
		for {
			select {
			case f := <-home.events:
				f(woken)
			case <-ui.ctx.Done():
				return
			}
		}
	}()
	return woken, nil
}

const loopyChatID = "loopy"

// localLoopy is this computer's Loopy for a view.
func (ui *desktopUI) localLoopy() (loopy.Client, error) {
	woken, err := ui.loopyEngine()
	if err != nil {
		return nil, err
	}
	return woken.Local(), nil
}

// wakeLoopy wakes a Loopy that lives here at start: its routines go on.
func (ui *desktopUI) wakeLoopy() {
	if ui.loopyDir() == "" || !fileExists(filepath.Join(ui.loopyDir(), "loopy.json")) && !fileExists(filepath.Join(filepath.Dir(ui.statePath), "loopy.json")) {
		return
	}
	go ui.loopyEngine()
}

// closeLoopy puts it to rest with the app (its context is done already).
func (ui *desktopUI) closeLoopy() {
	home := &ui.loopyHome
	home.mu.Lock()
	woken := home.engine
	home.engine, home.closed = nil, true
	home.mu.Unlock()
	if woken == nil {
		return
	}
	done := make(chan bool)
	go func() { woken.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second): // what it wrote is saved as it goes
	}
}

// loopyTell hands Loopy what a chat does, in order and off the interface's
// thread.
func (ui *desktopUI) loopyTell(tell func(*engine.Engine)) {
	home := &ui.loopyHome
	home.mu.Lock()
	events := home.events
	home.mu.Unlock()
	if events == nil {
		return
	}
	select {
	case events <- tell:
	default:
	}
}

// loopyStep tells Loopy of a step of a chat it may have started.
func (ui *desktopUI) loopyStep(chat *conversation, step string, waits bool) {
	if chat.ID == loopyChatID {
		return
	}
	id := chat.ID
	ui.loopyTell(func(e *engine.Engine) { e.ChatStep(id, strings.TrimSuffix(step, "…"), waits) })
}

// loopyEnded tells Loopy that a chat's work is over.
func (ui *desktopUI) loopyEnded(chat *conversation, ended string) {
	result := ""
	for i := len(chat.Items) - 1; i >= 0; i-- {
		if item := chat.Items[i]; (item.Kind == "assistant" || item.Kind == "error") && strings.TrimSpace(item.Text) != "" {
			result = item.Text
			if item.Kind == "error" && ended == "stopped" && (chat.run == nil || !chat.run.stopped) {
				ended = "failed"
			}
			break
		}
	}
	id := chat.ID
	ui.loopyTell(func(e *engine.Engine) { e.ChatEnded(id, ended, result) })
}

// loopyHandlers are the paired phone's way to this computer's Loopy.
func (server *remoteServer) loopyHandlers() map[string]http.HandlerFunc {
	return engine.Handlers(server.ui.loopyEngine)
}

func (server *remoteServer) loopyHook() http.HandlerFunc {
	return engine.HookHandler(server.ui.loopyEngine)
}

// loopyDesktop is the app as Loopy's host.
type loopyDesktop struct{ ui *desktopUI }

func (host loopyDesktop) Name() string        { return "This computer" }
func (host loopyDesktop) Backend() *agent.App { return host.ui.backend }

// on runs f on the interface's thread; false when the app closes.
func (host loopyDesktop) on(f func()) bool {
	if host.ui.ctx.Err() != nil {
		return false
	}
	host.ui.onUI(host.ui.ctx, f)
	return true
}

func (host loopyDesktop) Config(settings loopy.Settings, workspace string) (cfg agent.Config, err error) {
	if !host.on(func() { cfg, err = host.config(settings, workspace) }) {
		err = errors.New("1loop is closing")
	}
	return
}

func (host loopyDesktop) config(settings loopy.Settings, workspace string) (agent.Config, error) {
	ui := host.ui
	chat := ui.loopyHome.chat
	cfg := ui.cfg
	if ui.server != nil { // the window works on a server: this computer's own choice
		cfg = ui.localCfg
	}
	if settings.Model != "" {
		found := false
		for _, choice := range ui.choices {
			if choice.Connection.ID == settings.Connection && choice.ID == settings.Model && choice.Connection.Enabled {
				cfg = choice.Connection.Config(workspace)
				cfg.Model, cfg.Effort, found = choice.ID, choice.DefaultEffort, true
				break
			}
		}
		if !found && ui.modelsLoading { // not listed yet: as it was chosen
			for _, conn := range ui.connectionsList {
				if conn.ID == settings.Connection && conn.Enabled {
					cfg = conn.Config(workspace)
					cfg.Model, found = settings.Model, true
				}
			}
		}
		if !found {
			return cfg, fmt.Errorf("The model you chose for me (%s) is not available here: choose another.", settings.Model)
		}
	}
	if settings.Effort != "" {
		cfg.Effort = settings.Effort
	}
	cfg.Workspace, cfg.Scratch, cfg.MachineID, cfg.RanWith = workspace, "", "", nil
	cfg.ClearSessions()
	chat.Config = cfg
	cfg = ui.runConfig(chat)
	browserTools := cfg.Browser
	cfg.Todo, cfg.Goal, cfg.Tasks, cfg.Subchats, cfg.Chats, cfg.Settings, cfg.Preferences, cfg.Move, cfg.Review = nil, nil, nil, nil, nil, nil, nil, nil, nil
	cfg.Terminal, cfg.Computer, cfg.Phone, cfg.Packages, cfg.ToolAllowed = nil, nil, nil, nil, nil
	cfg.UserInstructions, cfg.AutoResume, cfg.WrapUpOff = "", nil, true
	cfg.ContextKey, cfg.Browser = loopyChatID, browserTools
	return cfg, nil
}

func (host loopyDesktop) Models() (out []loopy.Model) {
	host.on(func() {
		for label, choice := range host.ui.choices {
			if choice.Connection.Enabled && choice.Connection.Provider != "antigravity" && choice.Connection.Provider != "chatgptweb" {
				out = append(out, loopy.Model{Connection: choice.Connection.ID, ID: choice.ID, Label: label, Efforts: choice.Efforts})
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return
}

func (host loopyDesktop) Page() (page *browser.Page) {
	host.on(func() { page = host.ui.loopyPage() })
	return
}

func (host loopyDesktop) Pages() (out []*browser.Page) {
	host.on(func() {
		panel, chat := host.ui.panel, host.ui.loopyHome.chat
		if panel == nil || chat == nil {
			return
		}
		panel = panel.of(chat)
		tabs := panel.stateOf(chat).tabs
		if chat == panel.owner {
			tabs = panel.tabs
		}
		for _, tab := range tabs {
			if tab.browser != nil {
				out = append(out, tab.browser)
			}
		}
	})
	return
}

// loopyPage is the page Loopy works in, on the interface's thread.
func (ui *desktopUI) loopyPage() *browser.Page {
	if ui.loopyHome.chat == nil || ui.panel == nil {
		return nil
	}
	state := ui.panel.stateOf(ui.loopyHome.chat)
	var page *browser.Page
	for i, tab := range state.tabs {
		if tab.browser != nil {
			page = tab.browser
			if i == state.active {
				break
			}
		}
	}
	return page
}

// loopyBrowser is Loopy's own browser: another process and another profile
// than Browser Use's.
func (ui *desktopUI) loopyBrowser() *browser.Engine {
	if ui.loopyHome.browser == nil {
		ui.loopyHome.browser = browser.NewBrowserEngine(ui.ctx)
		ui.loopyHome.browser.Scale = float64(ui.window.Canvas().Scale())
		if ui.loopyDir() != "" {
			ui.loopyHome.browser.Profile = filepath.Join(ui.loopyDir(), "browser")
		}
	}
	return ui.loopyHome.browser
}

// OpenPage opens a page for a script, in a tab of Loopy's panel which it
// does not bring forward.
func (host loopyDesktop) OpenPage(info browser.Info) (page *browser.Page, err error) {
	if !host.on(func() {
		panel, chat := host.ui.panel, host.ui.loopyHome.chat
		if panel == nil || chat == nil {
			err = errors.New("the window is not ready")
			return
		}
		panel = panel.of(chat)
		state := panel.stateOf(chat)
		tabs := state.tabs
		if chat == panel.owner {
			tabs = panel.tabs
		}
		tab := panel.browserTab(chat, info, tabs)
		page = tab.browser
		tabs = append(tabs, tab)
		state.tabs = tabs
		if state.active < 0 {
			state.active = len(tabs) - 1
		}
		if chat == panel.owner {
			panel.tabs = tabs
			if panel.active < 0 {
				panel.active = len(tabs) - 1
			}
			panel.render()
		}
	}) {
		err = errors.New("1loop is closing")
	}
	return
}

func (host loopyDesktop) ClosePage(id string) {
	host.closePages(func(page *browser.Page) bool { return page.Snapshot().ID == id })
}

func (host loopyDesktop) ClosePages() {
	host.closePages(func(*browser.Page) bool { return true })
}

func (host loopyDesktop) closePages(which func(*browser.Page) bool) {
	host.on(func() {
		panel, chat := host.ui.panel, host.ui.loopyHome.chat
		if panel == nil || chat == nil {
			return
		}
		panel = panel.of(chat)
		tabs := panel.stateOf(chat).tabs
		if chat == panel.owner {
			tabs = panel.tabs
		}
		var pages []*browser.Page
		for _, tab := range tabs {
			if tab.browser != nil && which(tab.browser) {
				pages = append(pages, tab.browser)
			}
		}
		for _, page := range pages {
			panel.removeBrowser(chat, page)
		}
	})
}

func (host loopyDesktop) Chats() (out []loopy.Chat) {
	host.on(func() {
		for _, chat := range host.ui.chats {
			out = append(out, loopy.Chat{ID: chat.ID, Title: chat.Title, Project: chat.Config.Workspace, Running: chat.run != nil})
		}
	})
	return
}

func (host loopyDesktop) DeleteChat(id string) (err error) {
	if !host.on(func() {
		chat := host.ui.chatByID(id)
		if chat == nil {
			err = errors.New("it is gone already")
			return
		}
		host.ui.removeTree(chat)
	}) {
		err = errors.New("1loop is closing")
	}
	return
}

func (host loopyDesktop) Projects() (out []string) {
	host.on(func() { out = append(out, host.ui.projects...) })
	return
}

func (host loopyDesktop) StartChat(project, title, brief string) (id string, err error) {
	if !host.on(func() {
		ui := host.ui
		cfg := ui.cfg
		if ui.server != nil {
			cfg = ui.localCfg
		}
		if cfg.Model == "" {
			err = errors.New("Choose a model for your projects first")
			return
		}
		cfg.Workspace, cfg.Scratch, cfg.MachineID, cfg.RanWith = project, "", "", nil
		cfg.ClearSessions()
		name := []rune(title)
		chat := &conversation{ID: core.Identifier()[:16], Title: string(name[:min(36, len(name))]), Config: cfg}
		ui.ensureProject(project)
		ui.chats = append([]*conversation{chat}, ui.chats...)
		ui.renderChats()
		ui.startIn(chat, agent.Message{Role: "user", Content: brief}, "")
		id = chat.ID
	}) {
		err = errors.New("1loop is closing")
	}
	return
}

func (host loopyDesktop) StopChat(id string) {
	host.on(func() {
		if chat := host.ui.chatByID(id); chat != nil && chat.run != nil {
			chat.run.cancel()
		}
	})
}

func (host loopyDesktop) Notify(notice loopy.Notice) {
	host.ui.remote.broadcastLoopy("loopy_notice", notice)
	if host.ui.ctx.Err() != nil {
		return
	}
	fyne.Do(func() { host.ui.loopyNotice(notice) })
}

func (host loopyDesktop) GitHub(ctx context.Context) string {
	if found := githubconnector.Default.Authorizations(ctx); len(found) > 0 {
		return found[0]
	}
	return ""
}

func (host loopyDesktop) Changed(change loopy.Change) {
	host.ui.remote.broadcastLoopy("loopy", change)
	if host.ui.ctx.Err() != nil {
		return
	}
	fyne.Do(func() { host.ui.loopyChanged("", change) })
}

// broadcastLoopy hands an event to the paired phones, if any.
func (server *remoteServer) broadcastLoopy(kind string, value any) {
	if server != nil {
		server.broadcast(kind, value)
	}
}
