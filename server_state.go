//go:build server

package main

// The headless server's state: chats, settings and paired devices, kept as
// private JSON files in its data directory, and the runs of its agent loop.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type serverChat struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Config       config        `json:"config"`
	History      []message     `json:"history"`
	Items        []chatItem    `json:"items"`
	Todo         *todoState    `json:"todo"`
	Cache        *contextCache `json:"contextCache,omitempty"`
	Browsers     []browserInfo `json:"browsers,omitempty"`
	run          *serverRun
	tabs         []*serverTab
	session      *browserSession
	queue, steer []message
}

type serverRun struct {
	cancel      context.CancelFunc
	calls       map[string]int
	states      map[int]string
	index       int
	used        int
	label, mark string
	approval    *remoteApproval
}

type serverSettings struct {
	Config            config            `json:"config"`
	Connections       []connection      `json:"connections"`
	Keys              map[string]string `json:"keys"`
	UserInstructions  string            `json:"userInstructions"`
	ApprovalMode      string            `json:"approvalMode"`
	ClassicCompaction bool              `json:"classicCompaction"`
}

type headlessServer struct {
	mu                         sync.Mutex
	ctx                        context.Context
	backend                    *app
	engine                     *browserEngine
	name, root, dir            string
	cfg                        config
	connections                []connection
	keys                       map[string]string
	instructions, approvalMode string
	classic                    bool
	chats                      []*serverChat
	devices                    []pairedDevice
	pairs                      map[string]*serverPair
	subs                       map[chan serverEvent]bool
	streams                    map[chan struct{}]bool
	choices                    map[string]modelChoice
	loading                    bool
	saveTimer                  *time.Timer
}

type serverEvent struct {
	kind string
	data []byte
}

func newHeadlessServer(ctx context.Context, root, dir, configPath string) (*headlessServer, error) {
	host, _ := os.Hostname()
	server := &headlessServer{
		ctx:          ctx,
		name:         host,
		root:         root,
		dir:          dir,
		backend:      &app{approvals: map[string]chan bool{}},
		keys:         map[string]string{},
		pairs:        map[string]*serverPair{},
		subs:         map[chan serverEvent]bool{},
		streams:      map[chan struct{}]bool{},
		choices:      map[string]modelChoice{},
		approvalMode: "approval",
		loading:      true,
		cfg:          config{Provider: "compatible", Workspace: root},
	}
	var saved struct {
		Chats   []*serverChat
		Devices []pairedDevice
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		if err = json.Unmarshal(data, &saved); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	server.chats, server.devices = saved.Chats, saved.Devices
	customConfig := configPath != ""
	if configPath == "" {
		configPath = filepath.Join(dir, "config.json")
	}
	data, err = os.ReadFile(configPath)
	if err == nil {
		var settings serverSettings
		if err = json.Unmarshal(data, &settings); err != nil {
			return nil, err
		}
		server.cfg = settings.Config
		server.connections = settings.Connections
		if settings.Keys != nil {
			server.keys = settings.Keys
		}
		server.instructions = settings.UserInstructions
		server.classic = settings.ClassicCompaction
		if settings.ApprovalMode != "" {
			server.approvalMode = settings.ApprovalMode
		}
	} else if customConfig || !os.IsNotExist(err) {
		return nil, err
	}
	server.cfg.Workspace = root
	if len(server.connections) == 0 {
		server.connections = []connection{{
			ID:        identifier(),
			Name:      providerName(server.cfg.Provider),
			Provider:  server.cfg.Provider,
			BaseURL:   server.cfg.BaseURL,
			Enabled:   true,
			AllModels: true,
		}}
		server.cfg.ConnectionID = server.connections[0].ID
	}
	browserProfileOverride = filepath.Join(dir, "browser")
	server.engine = newBrowserEngine(ctx)
	for _, chat := range server.chats {
		chat.Config.Workspace = root
		if chat.Todo == nil {
			chat.Todo = &todoState{}
		}
		if chat.Cache == nil {
			chat.Cache = &contextCache{}
		}
	}
	return server, nil
}

func (server *headlessServer) saveLocked() error {
	for _, chat := range server.chats {
		// Unopened chats still hold their saved tabs; no live session owns them yet.
		if chat.session == nil {
			continue
		}
		chat.Browsers = nil
		for _, tab := range chat.tabs {
			if tab.browser != nil {
				chat.Browsers = append(chat.Browsers, tab.browser.snapshot())
			}
		}
	}
	return saveServerJSON(filepath.Join(server.dir, "state.json"), struct {
		Chats   []*serverChat  `json:"chats"`
		Devices []pairedDevice `json:"devices"`
	}{server.chats, server.devices})
}

func (server *headlessServer) scheduleSaveLocked() {
	if server.saveTimer != nil {
		server.saveTimer.Stop()
	}
	server.saveTimer = time.AfterFunc(400*time.Millisecond, func() {
		server.mu.Lock()
		defer server.mu.Unlock()
		if err := server.saveLocked(); err != nil {
			fmt.Fprintln(os.Stderr, "Save:", err)
		}
	})
}

func (server *headlessServer) close() {
	server.mu.Lock()
	if server.saveTimer != nil {
		server.saveTimer.Stop()
	}
	for _, chat := range server.chats {
		if chat.run != nil {
			chat.run.cancel()
		}
	}
	if err := server.saveLocked(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	for _, chat := range server.chats {
		for _, tab := range chat.tabs {
			tab.close()
		}
	}
	server.disconnectStreamsLocked()
	server.mu.Unlock()
	server.engine.cancel()
	server.engine.waitClosed()
}

func (server *headlessServer) chatLocked(id string) *serverChat {
	for _, chat := range server.chats {
		if chat.ID == id {
			return chat
		}
	}
	return nil
}

func (server *headlessServer) loadModels() {
	choices := map[string]modelChoice{}
	for _, connection := range server.connections {
		if !connection.Enabled {
			continue
		}
		rows, err := server.backend.connectionModels(server.ctx, connection, server.keys[connection.ID])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Models unavailable for %q: %v\n", connection.Name, err)
			continue
		}
		for _, choice := range rows {
			if connection.includes(choice.ID) {
				choices[choice.ID+" · "+connection.Name] = choice
			}
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	server.choices = choices
	server.loading = false
	if server.cfg.Model == "" {
		for _, choice := range choices {
			server.cfg = server.configFor(choice, server.cfg)
			break
		}
	}
	server.broadcastLocked("settings", server.settingsLocked())
}

func (server *headlessServer) configFor(choice modelChoice, previous config) config {
	cfg := choice.Connection.config(server.root)
	cfg.Model = choice.ID
	cfg.Effort = previous.Effort
	cfg.ReasoningMode = choice.ReasoningMode
	cfg.Session = previous.Session
	if !contains(choice.Efforts, cfg.Effort) {
		cfg.Effort = choice.DefaultEffort
	}
	options := choice.Connection.ModelOptions[choice.ID]
	cfg.Parameters = mergeOptions(cfg.Parameters, options.Parameters)
	cfg.Routing = mergeOptions(cfg.Routing, options.Provider)
	return cfg
}

func (server *headlessServer) labelLocked(cfg config) string {
	for label, choice := range server.choices {
		if choice.ID == cfg.Model && choice.Connection.ID == cfg.ConnectionID {
			return label
		}
	}
	return cfg.Model
}

func (server *headlessServer) settingsLocked() map[string]any {
	models := []remoteModel{}
	for label, choice := range server.choices {
		model := remoteModel{Label: label, Name: choice.display(), Detail: choice.Connection.Name, Efforts: []remoteEffort{}}
		for _, effort := range choice.Efforts {
			model.Efforts = append(model.Efforts, remoteEffort{Value: effort, Label: effortLabel(effort)})
		}
		models = append(models, model)
	}
	return map[string]any{
		"models":       models,
		"model":        server.labelLocked(server.cfg),
		"effort":       server.cfg.Effort,
		"window":       modelContextWindow(server.cfg),
		"approvalMode": server.approvalMode,
		"autoMode":     true,
		"loading":      server.loading,
		"computerUse":  false,
	}
}

func (server *headlessServer) summariesLocked() []remoteChat {
	out := []remoteChat{}
	for _, chat := range server.chats {
		out = append(out, remoteChat{ID: chat.ID, Title: chat.Title, Workspace: server.root, Running: chat.run != nil, Items: len(chat.Items)})
	}
	return out
}

func (server *headlessServer) metaLocked(chat *serverChat) remoteMeta {
	meta := remoteMeta{
		Chat:    chat.ID,
		Title:   chat.Title,
		Running: chat.run != nil,
		Items:   len(chat.Items),
		Queue:   []string{},
		Steer:   []string{},
		Model:   server.labelLocked(chat.Config),
		Effort:  chat.Config.Effort,
		Window:  modelContextWindow(chat.Config),
		Todo:    []remoteTodo{},
	}
	for _, m := range chat.queue {
		meta.Queue = append(meta.Queue, m.Content)
	}
	for _, m := range chat.steer {
		meta.Steer = append(meta.Steer, m.Content)
	}
	for _, task := range chat.Todo.snapshot() {
		item := remoteTodo{ID: task.ID, Text: task.Text, Status: task.Status}
		if task.StartedAt != nil {
			item.Started = task.StartedAt.UnixMilli()
		}
		if task.CompletedAt != nil {
			item.Completed = task.CompletedAt.UnixMilli()
		}
		meta.Todo = append(meta.Todo, item)
	}
	meta.Used = historyEstimate(chat.History) + contextOverhead(chat.Config)
	if chat.run != nil {
		meta.Label, meta.Mark, meta.Approval = chat.run.label, chat.run.mark, chat.run.approval
		if chat.run.used > 0 {
			meta.Used = chat.run.used
		}
	}
	meta.ContextStatus = taskContextText(chat.Config, meta.Used, meta.Window, server.classic, meta.Mark)
	return meta
}

func (server *headlessServer) itemLocked(chat *serverChat, index int) remoteItem {
	item := chat.Items[index]
	out := remoteItem{
		Kind:   item.Kind,
		Text:   item.Text,
		Images: len(item.Images),
		Tool:   item.Tool,
		Args:   item.Args,
		Diff:   item.Diff,
		Millis: item.Millis,
		Failed: item.Failed,
		State:  "done",
	}
	if chat.run != nil {
		if state := chat.run.states[index]; state != "" {
			out.State = state
		}
	}
	if item.Kind != "tool" {
		return out
	}
	out.Verb, out.Target, out.Mono = describeTool(item, out.State != "done")
	if item.Failed != "" {
		out.Meta, out.Tone = item.Failed, "danger"
	}
	return out
}

func (server *headlessServer) broadcastLocked(kind string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	for channel := range server.subs {
		select {
		case channel <- serverEvent{kind, data}:
		default:
			close(channel)
			delete(server.subs, channel)
		}
	}
}

func (server *headlessServer) disconnectStreamsLocked() {
	for channel := range server.subs {
		close(channel)
		delete(server.subs, channel)
	}
	for done := range server.streams {
		close(done)
		delete(server.streams, done)
	}
}

func (server *headlessServer) changedLocked(chat *serverChat, index int) {
	if index >= 0 {
		server.broadcastLocked("item", map[string]any{"chat": chat.ID, "index": index, "item": server.itemLocked(chat, index)})
	}
	server.broadcastLocked("meta", server.metaLocked(chat))
	server.scheduleSaveLocked()
}

func (server *headlessServer) addLocked(chat *serverChat, item chatItem) int {
	if item.ID == "" {
		item.ID = identifier()[:16]
	}
	chat.Items = append(chat.Items, item)
	return len(chat.Items) - 1
}

func (server *headlessServer) runLocked(chat *serverChat, m message) {
	chat.History = append(chat.History, m)
	index := server.addLocked(chat, chatItem{Kind: "user", Text: m.Content, Images: m.Images})
	ctx, cancel := context.WithCancel(server.ctx)
	run := &serverRun{cancel: cancel, calls: map[string]int{}, states: map[int]string{}, index: -1, label: "Waiting for the model…", mark: "wait"}
	chat.run = run
	cfg := chat.Config
	cfg.APIKey = server.keys[cfg.ConnectionID]
	cfg.UserInstructions = server.instructions
	cfg.ApprovalMode = approvalFor(server.approvalMode, cfg)
	cfg.ClassicCompaction = server.classic
	cfg.Todo = chat.Todo
	cfg.Cache = chat.Cache
	cfg.ContextKey = chat.ID
	cfg.Browser = server.browserTools(chat)
	cfg.Terminal = server.terminalTools(chat)
	cfg.Recall = func(ctx context.Context) ([]chatItem, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		return append([]chatItem{}, chat.Items...), ctx.Err()
	}
	history := append([]message{}, chat.History...)
	server.changedLocked(chat, index)
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	inboxValue := inbox{steer: func() []message {
		server.mu.Lock()
		defer server.mu.Unlock()
		out := chat.steer
		chat.steer = nil
		return out
	}, next: func() (message, bool) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if len(chat.queue) == 0 {
			return message{}, false
		}
		out := chat.queue[0]
		chat.queue = chat.queue[1:]
		return out, true
	}}
	go func() {
		defer cancel()
		err := server.backend.run(ctx, cfg, history, inboxValue, func(kind string, value any) { server.emit(chat, run, kind, value) })
		server.mu.Lock()
		defer server.mu.Unlock()
		if chat.run != run {
			return
		}
		chat.run = nil
		if err != nil {
			server.addLocked(chat, chatItem{Kind: "error", Text: err.Error()})
		}
		server.broadcastLocked("replace", map[string]string{"chat": chat.ID})
		server.broadcastLocked("meta", server.metaLocked(chat))
		server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
		server.scheduleSaveLocked()
		if len(chat.queue) > 0 && server.ctx.Err() == nil {
			next := chat.queue[0]
			chat.queue = chat.queue[1:]
			server.runLocked(chat, next)
		}
	}()
}

func (server *headlessServer) emit(chat *serverChat, run *serverRun, kind string, value any) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if chat.run != run {
		return
	}
	index := -1
	switch kind {
	case "delta":
		if run.index < 0 {
			run.index = server.addLocked(chat, chatItem{Kind: "assistant"})
		}
		index = run.index
		chat.Items[index].Text += value.(string)
		run.label, run.mark = "Writing…", "write"
	case "message_end":
		run.index = -1
	case "history":
		chat.History = value.([]message)
	case "injected":
		m := value.(message)
		index = server.addLocked(chat, chatItem{Kind: "user", Text: m.Content, Images: m.Images})
	case "activity":
		status := value.(activity)
		run.mark = status.State
		run.label = strings.TrimSpace(status.State + " " + status.Tool + " " + status.Detail)
	case "tool":
		outcome := value.(toolOutcome)
		index = server.addLocked(chat, chatItem{ID: outcome.RecallID, Kind: "tool", Tool: outcome.Name, Args: outcome.Args})
		run.calls[outcome.ID] = index
		run.states[index] = "running"
	case "tool_exec":
		outcome := value.(toolOutcome)
		if at, ok := run.calls[outcome.ID]; ok {
			index = at
			run.states[index] = "running"
		}
		run.approval = nil
	case "tool_result":
		outcome := value.(toolOutcome)
		if at, ok := run.calls[outcome.ID]; ok {
			index = at
			item := &chat.Items[index]
			item.Text, item.Diff, item.Millis, item.Images = outcome.Output, outcome.Diff, outcome.Elapsed.Milliseconds(), outcome.Images
			if outcome.Err != nil {
				item.Failed = outcome.Err.Error()
			}
			delete(run.states, index)
		}
		run.approval = nil
	case "approval":
		data := value.(map[string]any)
		run.approval = &remoteApproval{Chat: chat.ID, ID: fmt.Sprint(data["id"]), Title: fmt.Sprint(data["title"]), Preview: fmt.Sprint(data["preview"])}
		run.mark, run.label = "approval", "Waiting for your approval"
	case "context_note", "note":
		index = server.addLocked(chat, chatItem{Kind: "note", Text: value.(string)})
	case "usage":
		run.used = value.(int)
	case "session":
		chat.Config.Session = value.(string)
	case "notice":
		run.label = value.(string)
	}
	server.changedLocked(chat, index)
}

func (server *headlessServer) saveSettingsLocked() error {
	return saveServerJSON(filepath.Join(server.dir, "config.json"), serverSettings{
		Config:            server.cfg,
		Connections:       server.connections,
		Keys:              server.keys,
		UserInstructions:  server.instructions,
		ApprovalMode:      server.approvalMode,
		ClassicCompaction: server.classic,
	})
}
