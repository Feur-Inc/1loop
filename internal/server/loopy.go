// SPDX-License-Identifier: MPL-2.0

package server

// Loopy on this server (internal/loopy/engine): always on, so its routines
// and watches go on when nobody is there. The phone's own engine is this
// server too. Its views are the desktop and the phone, over /api/loopy.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/githubconnector"
	"1loop/internal/loopy"
	"1loop/internal/loopy/engine"
)

// loopyChat is the id of the hidden chat whose browser is Loopy's: the
// panel's routes show it as any chat's.
const loopyChat = "loopy"

// loopyDir is where this server keeps its Loopy.
func (server *headlessServer) loopyDir() string { return filepath.Join(server.dir, "loopy") }

// loopyEngine is this server's Loopy, woken on first use.
func (server *headlessServer) loopyEngine() (*engine.Engine, error) {
	server.loopyMu.Lock()
	defer server.loopyMu.Unlock()
	if server.loopy != nil {
		return server.loopy, nil
	}
	if server.ctx.Err() != nil || server.loopyClosed {
		return nil, errors.New("The server is stopping")
	}
	woken, err := engine.Open(server.ctx, server.loopyDir(), loopyHost{server})
	if err != nil {
		return nil, err
	}
	server.loopy = woken
	return woken, nil
}

// wakeLoopy wakes a Loopy that has something to do by itself.
func (server *headlessServer) wakeLoopy() {
	if _, err := os.Stat(filepath.Join(server.loopyDir(), "loopy.json")); err != nil {
		return
	}
	if _, err := server.loopyEngine(); err != nil {
		fmt.Fprintln(os.Stderr, "Loopy:", err)
	}
}

func (server *headlessServer) closeLoopy() {
	server.loopyMu.Lock()
	woken := server.loopy
	server.loopy, server.loopyClosed = nil, true
	server.loopyMu.Unlock()
	if woken != nil {
		woken.Close()
	}
}

// loopyTell hands Loopy what a chat does, in order, without this server's
// lock: Loopy asks things of the server in turn.
func (server *headlessServer) loopyTell(tell func(e *engine.Engine)) {
	server.loopyMu.Lock()
	woken := server.loopy
	server.loopyMu.Unlock()
	if woken == nil {
		return
	}
	server.loopyOnce.Do(func() {
		server.loopyEvents = make(chan func(), 1024)
		go func() {
			for {
				select {
				case f := <-server.loopyEvents:
					f()
				case <-server.ctx.Done():
					return
				}
			}
		}()
	})
	select {
	case server.loopyEvents <- func() { tell(woken) }:
	default: // it is told of the end all the same
		go tell(woken)
	}
}

// loopyFollow tells Loopy of a step of a chat it may have started.
func (server *headlessServer) loopyFollow(chat *serverChat, run *serverRun, kind string) {
	if chat.ID == loopyChat {
		return
	}
	id := chat.ID
	switch kind {
	case "tool_exec":
		label := run.label
		server.loopyTell(func(e *engine.Engine) { e.ChatStep(id, strings.TrimSuffix(label, "…"), false) })
	case "approval", "question":
		label := run.label
		server.loopyTell(func(e *engine.Engine) { e.ChatStep(id, label, true) })
	}
}

// loopyEnded tells Loopy that a chat's work is over.
func (server *headlessServer) loopyEnded(chat *serverChat, ended string) {
	result := ""
	for i := len(chat.Items) - 1; i >= 0; i-- {
		if item := chat.Items[i]; (item.Kind == "assistant" || item.Kind == "error") && strings.TrimSpace(item.Text) != "" {
			result = item.Text
			break
		}
	}
	how := map[string]string{agent.SubchatDone: "done", agent.SubchatStopped: "stopped"}[ended]
	id := chat.ID
	server.loopyTell(func(e *engine.Engine) { e.ChatEnded(id, how, result) })
}

// loopyChatLocked is the hidden chat of Loopy's browser.
func (server *headlessServer) loopyChatLocked() *serverChat {
	if server.panelHomes == nil {
		server.panelHomes = map[string]*serverChat{}
	}
	chat := server.panelHomes[loopyChat]
	if chat == nil {
		config := server.cfg
		config.Workspace, config.RanWith, config.MachineID, config.Scratch = filepath.Join(server.loopyDir(), "home"), nil, "", ""
		config.ClearSessions()
		chat = &serverChat{ID: loopyChat, Title: "Loopy", Config: config, Cache: &agent.ContextCache{}}
		server.panelHomes[loopyChat] = chat
	}
	return chat
}

func (server *headlessServer) loopyRoutes(endpoints map[string]http.HandlerFunc) {
	for path, handler := range engine.Handlers(server.loopyEngine) {
		endpoints[path] = handler
	}
}

// loopyHost is the server as Loopy's host.
type loopyHost struct{ server *headlessServer }

func (host loopyHost) Name() string        { return host.server.name }
func (host loopyHost) Backend() *agent.App { return host.server.backend }

func (host loopyHost) Config(settings loopy.Settings, workspace string) (agent.Config, error) {
	server := host.server
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.loopyChatLocked()
	cfg := server.cfg
	if settings.Model != "" {
		choice, ok := findChoice(server.choices, settings.Connection, settings.Model)
		connection, enabled := server.connectionLocked(settings.Connection)
		if !ok || !enabled || !connection.Enabled {
			return cfg, fmt.Errorf("The model you chose for me (%s) is not available here: choose another.", settings.Model)
		}
		cfg = connection.Config(workspace)
		cfg.Model, cfg.Effort = choice.ID, choice.DefaultEffort
	}
	if settings.Effort != "" {
		cfg.Effort = settings.Effort
	}
	cfg.Workspace, cfg.MachineID, cfg.Scratch, cfg.RanWith = workspace, "", "", nil
	cfg.ClearSessions()
	chat.Config = cfg
	cfg = server.runConfigLocked(chat, nil)
	loopyStrip(&cfg)
	if server.hasBrowser() {
		cfg.Browser = server.browserTools(chat)
	}
	return cfg, nil
}

// loopyStrip takes out of a run's configuration what is a coding chat's.
func loopyStrip(cfg *agent.Config) {
	cfg.Todo, cfg.Goal, cfg.Tasks, cfg.Subchats, cfg.Chats, cfg.Settings, cfg.Preferences, cfg.Move, cfg.Review = nil, nil, nil, nil, nil, nil, nil, nil, nil
	cfg.Terminal, cfg.Computer, cfg.Phone, cfg.Packages, cfg.Browser, cfg.ToolAllowed = nil, nil, nil, nil, nil, nil
	cfg.UserInstructions, cfg.AutoResume, cfg.WrapUpOff = "", nil, true
	cfg.ContextKey = loopyChat
}

func findChoice(choices map[string]agent.ModelChoice, connection, model string) (agent.ModelChoice, bool) {
	for _, choice := range choices {
		if choice.Connection.ID == connection && choice.ID == model {
			return choice, true
		}
	}
	return agent.ModelChoice{}, false
}

func (host loopyHost) Models() []loopy.Model {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	return loopyModels(host.server.choices, host.server.connections)
}

func loopyModels(choices map[string]agent.ModelChoice, connections []agent.Connection) []loopy.Model {
	enabled := map[string]bool{}
	for _, connection := range connections {
		enabled[connection.ID] = connection.Enabled
	}
	out := []loopy.Model{}
	for label, choice := range choices {
		if enabled[choice.Connection.ID] && choice.Connection.Provider != "antigravity" && choice.Connection.Provider != "chatgptweb" {
			out = append(out, loopy.Model{Connection: choice.Connection.ID, ID: choice.ID, Label: label, Efforts: choice.Efforts})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (host loopyHost) Page() *browser.Page {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	chat := host.server.panelHomes[loopyChat]
	if chat == nil {
		return nil
	}
	for i := len(chat.tabs) - 1; i >= 0; i-- {
		if chat.tabs[i].browser != nil {
			return chat.tabs[i].browser
		}
	}
	return nil
}

func (host loopyHost) Pages() (out []*browser.Page) {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	if chat := host.server.panelHomes[loopyChat]; chat != nil {
		for _, tab := range chat.tabs {
			if tab.browser != nil {
				out = append(out, tab.browser)
			}
		}
	}
	return
}

func (host loopyHost) Projects() []string {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	var out []string
	for _, project := range host.server.projects {
		if host.server.projectMachines[project] == "" {
			out = append(out, project)
		}
	}
	return out
}

func (host loopyHost) StartChat(project, title, brief string) (string, error) {
	server := host.server
	server.mu.Lock()
	defer server.mu.Unlock()
	workspace, err := server.workspaceLocked(project)
	if err != nil {
		return "", err
	}
	if server.cfg.Model == "" {
		return "", errors.New("Configure a provider and model first")
	}
	cfg := server.cfg
	cfg.Workspace, cfg.Scratch, cfg.MachineID, cfg.RanWith = workspace, "", "", nil
	cfg.ClearSessions()
	name := []rune(strings.Join(strings.Fields(title), " "))
	chat := &serverChat{ID: core.Identifier()[:16], Title: string(name[:min(50, len(name))]), Config: cfg, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	server.chats = append([]*serverChat{chat}, server.chats...)
	server.runLocked(chat, agent.Message{Role: "user", Content: brief})
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	server.scheduleSaveLocked()
	return chat.ID, nil
}

func (host loopyHost) StopChat(id string) {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	if chat := host.server.chatLocked(id); chat != nil && chat.run != nil {
		chat.run.cancel()
		chat.queue, chat.steer = nil, nil
	}
}

// OpenPage opens a page for a script, in a tab of Loopy's panel.
func (host loopyHost) OpenPage(info browser.Info) (*browser.Page, error) {
	server := host.server
	if !server.hasBrowser() {
		return nil, errors.New(browser.NoBrowserProblem)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.ctx.Err() != nil {
		return nil, errors.New("The server is stopping")
	}
	chat := server.loopyChatLocked()
	server.ensureTabsLocked(chat)
	return server.addBrowserLocked(chat, info, ""), nil
}

func (host loopyHost) ClosePage(id string) {
	host.closePages(func(page *browser.Page) bool { return page.Snapshot().ID == id })
}

func (host loopyHost) ClosePages() {
	host.closePages(func(*browser.Page) bool { return true })
}

func (host loopyHost) closePages(which func(*browser.Page) bool) {
	server := host.server
	server.mu.Lock()
	chat := server.panelHomes[loopyChat]
	var closed []*serverTab
	if chat != nil {
		for i := len(chat.tabs) - 1; i >= 0; i-- {
			if chat.tabs[i].browser != nil && which(chat.tabs[i].browser) {
				closed = append(closed, server.detachTabLocked(chat, i))
			}
		}
		if len(closed) > 0 {
			server.scheduleSaveLocked()
		}
	}
	server.mu.Unlock()
	for _, tab := range closed {
		tab.close()
	}
}

func (host loopyHost) Chats() (out []loopy.Chat) {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	for _, chat := range host.server.chats {
		out = append(out, loopy.Chat{ID: chat.ID, Title: chat.Title, Project: chat.Config.Workspace, Running: chat.run != nil})
	}
	return
}

func (host loopyHost) DeleteChat(id string) error {
	return host.server.removeChat(id)
}

// Notify is for the clients: each says it when nobody looks at it.
func (host loopyHost) Notify(notice loopy.Notice) {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	host.server.broadcastLocked("loopy_notice", notice)
}

func (host loopyHost) GitHub(ctx context.Context) string {
	if found := githubconnector.Default.Authorizations(ctx); len(found) > 0 {
		return found[0]
	}
	return ""
}

func (host loopyHost) Changed(change loopy.Change) {
	host.server.mu.Lock()
	defer host.server.mu.Unlock()
	host.server.broadcastLocked("loopy", change)
}
