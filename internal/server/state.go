package server

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

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

type serverChat struct {
	ID           string                  `json:"id"`
	Title        string                  `json:"title"`
	Config       agent.Config            `json:"config"`
	History      []agent.Message         `json:"history"`
	Items        []agent.ChatItem        `json:"items"`
	Todo         *agent.TodoState        `json:"todo"`
	Cache        *agent.ContextCache     `json:"contextCache,omitempty"`
	grants       *agent.PermissionGrants // what request_permissions granted, until the server stops
	review       *agent.ReviewSession    // the Auto mode reviewer's conversation, until the server stops
	Browsers     []browser.Info          `json:"browsers,omitempty"`
	run          *serverRun
	tabs         []*serverTab
	session      *browser.Session
	queue, steer []agent.Message
}

type serverRun struct {
	cancel      context.CancelFunc
	calls       map[string]int
	states      map[int]string
	index       int
	used        int
	label, mark string
	approval    *remoteapi.Approval
	question    *remoteapi.Question
}

type serverSettings struct {
	Config            agent.Config       `json:"config"`
	Connections       []agent.Connection `json:"connections"`
	Keys              map[string]string  `json:"keys"`
	UserInstructions  string             `json:"userInstructions"`
	ApprovalMode      string             `json:"approvalMode"`
	ClassicCompaction bool               `json:"classicCompaction"`
}

type headlessServer struct {
	mu                         sync.Mutex
	ctx                        context.Context
	backend                    *agent.App
	engine                     *browser.Engine
	name, root, dir            string
	cfg                        agent.Config
	connections                []agent.Connection
	keys                       map[string]string
	instructions, approvalMode string
	classic                    bool
	chats                      []*serverChat
	devices                    []remoteapi.PairedDevice
	pairs                      map[string]*serverPair
	subs                       map[chan serverEvent]bool
	streams                    map[chan struct{}]bool
	choices                    map[string]agent.ModelChoice
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
		backend:      &agent.App{Approvals: map[string]chan bool{}},
		keys:         map[string]string{},
		pairs:        map[string]*serverPair{},
		subs:         map[chan serverEvent]bool{},
		streams:      map[chan struct{}]bool{},
		choices:      map[string]agent.ModelChoice{},
		approvalMode: "approval",
		loading:      true,
		cfg:          agent.Config{Provider: "compatible", Workspace: root},
	}
	var saved struct {
		Chats   []*serverChat
		Devices []remoteapi.PairedDevice
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
		server.connections = []agent.Connection{{
			ID:        core.Identifier(),
			Name:      agent.ProviderName(server.cfg.Provider),
			Provider:  server.cfg.Provider,
			BaseURL:   server.cfg.BaseURL,
			Enabled:   true,
			AllModels: true,
		}}
		server.cfg.ConnectionID = server.connections[0].ID
	}
	browser.BrowserProfileOverride = filepath.Join(dir, "browser")
	server.engine = browser.NewBrowserEngine(ctx)
	for _, chat := range server.chats {
		chat.Config.Workspace = root
		if chat.Todo == nil {
			chat.Todo = &agent.TodoState{}
		}
		if chat.Cache == nil {
			chat.Cache = &agent.ContextCache{}
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
				chat.Browsers = append(chat.Browsers, tab.browser.Snapshot())
			}
		}
	}
	return saveServerJSON(filepath.Join(server.dir, "state.json"), struct {
		Chats   []*serverChat            `json:"chats"`
		Devices []remoteapi.PairedDevice `json:"devices"`
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
	server.engine.Cancel()
	server.engine.WaitClosed()
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
	choices := map[string]agent.ModelChoice{}
	for _, connection := range server.connections {
		if !connection.Enabled {
			continue
		}
		rows, err := server.backend.ConnectionModels(server.ctx, connection, server.keys[connection.ID])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Models unavailable for %q: %v\n", connection.Name, err)
			continue
		}
		for _, choice := range rows {
			if connection.Includes(choice.ID) {
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

func (server *headlessServer) configFor(choice agent.ModelChoice, previous agent.Config) agent.Config {
	cfg := choice.Connection.Config(server.root)
	cfg.Model = choice.ID
	cfg.Effort = previous.Effort
	cfg.ServiceTier = agent.TierFor(choice, previous.ServiceTier)
	cfg.ReasoningMode = choice.ReasoningMode
	cfg.Session = previous.Session
	if !agent.Contains(choice.Efforts, cfg.Effort) {
		cfg.Effort = choice.DefaultEffort
	}
	options := choice.Connection.ModelOptions[choice.ID]
	cfg.Parameters = agent.MergeOptions(cfg.Parameters, options.Parameters)
	cfg.Routing = agent.MergeOptions(cfg.Routing, options.Provider)
	return cfg
}

func (server *headlessServer) labelLocked(cfg agent.Config) string {
	for label, choice := range server.choices {
		if choice.ID == cfg.Model && choice.Connection.ID == cfg.ConnectionID {
			return label
		}
	}
	return cfg.Model
}

func (server *headlessServer) settingsLocked() map[string]any {
	models := []remoteapi.Model{}
	for label, choice := range server.choices {
		model := remoteapi.Model{Label: label, Name: choice.Display(), Detail: choice.Connection.Name, Efforts: []remoteapi.Effort{}, ServiceTiers: choice.ServiceTiers, DefaultServiceTier: choice.DefaultServiceTier}
		for _, effort := range choice.Efforts {
			model.Efforts = append(model.Efforts, remoteapi.Effort{Value: effort, Label: agent.EffortLabel(effort)})
		}
		models = append(models, model)
	}
	return map[string]any{
		"models":       models,
		"model":        server.labelLocked(server.cfg),
		"effort":       server.cfg.Effort,
		"tier":         server.cfg.ServiceTier,
		"window":       agent.ModelContextWindow(server.cfg),
		"approvalMode": server.approvalMode,
		"autoMode":     agent.AutoModeOffered(server.cfg),
		"loading":      server.loading,
		"computerUse":  false,
	}
}

func (server *headlessServer) summariesLocked() []remoteapi.Chat {
	out := []remoteapi.Chat{}
	for _, chat := range server.chats {
		out = append(out, remoteapi.Chat{ID: chat.ID, Title: chat.Title, Workspace: server.root, Running: chat.run != nil, Items: len(chat.Items)})
	}
	return out
}

func (server *headlessServer) metaLocked(chat *serverChat) remoteapi.Meta {
	meta := remoteapi.Meta{
		Chat:    chat.ID,
		Title:   chat.Title,
		Running: chat.run != nil,
		Items:   len(chat.Items),
		Queue:   []string{},
		Steer:   []string{},
		Model:   server.labelLocked(chat.Config),
		Effort:  chat.Config.Effort,
		Tier:    chat.Config.ServiceTier,
		Window:  agent.ModelContextWindow(chat.Config),
		Todo:    []remoteapi.Todo{},
	}
	for _, m := range chat.queue {
		meta.Queue = append(meta.Queue, m.Content)
	}
	for _, m := range chat.steer {
		meta.Steer = append(meta.Steer, m.Content)
	}
	for _, task := range chat.Todo.Snapshot() {
		item := remoteapi.Todo{ID: task.ID, Text: task.Text, Status: task.Status}
		if task.StartedAt != nil {
			item.Started = task.StartedAt.UnixMilli()
		}
		if task.CompletedAt != nil {
			item.Completed = task.CompletedAt.UnixMilli()
		}
		meta.Todo = append(meta.Todo, item)
	}
	meta.Used = agent.HistoryEstimate(chat.History) + agent.ContextOverhead(chat.Config)
	if chat.run != nil {
		meta.Label, meta.Mark, meta.Approval, meta.Question = chat.run.label, chat.run.mark, chat.run.approval, chat.run.question
		if chat.run.used > 0 {
			meta.Used = chat.run.used
		}
	}
	meta.ContextStatus = agent.TaskContextText(chat.Config, meta.Used, meta.Window, server.classic, meta.Mark)
	return meta
}

func (server *headlessServer) itemLocked(chat *serverChat, index int) remoteapi.Item {
	item := chat.Items[index]
	out := remoteapi.Item{
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
	out.Verb, out.Target, out.Mono = agent.DescribeTool(item, out.State != "done")
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

func (server *headlessServer) addLocked(chat *serverChat, item agent.ChatItem) int {
	if item.ID == "" {
		item.ID = core.Identifier()[:16]
	}
	chat.Items = append(chat.Items, item)
	return len(chat.Items) - 1
}

func (server *headlessServer) runLocked(chat *serverChat, m agent.Message) {
	chat.History = append(chat.History, m)
	index := server.addLocked(chat, agent.ChatItem{Kind: "user", Text: m.Content, Images: m.Images})
	ctx, cancel := context.WithCancel(server.ctx)
	run := &serverRun{cancel: cancel, calls: map[string]int{}, states: map[int]string{}, index: -1, label: "Waiting for the model…", mark: "wait"}
	chat.run = run
	cfg := chat.Config
	cfg.APIKey = server.keys[cfg.ConnectionID]
	cfg.UserInstructions = server.instructions
	cfg.ApprovalMode = agent.ApprovalFor(server.approvalMode, cfg)
	cfg.ClassicCompaction = server.classic
	cfg.Todo = chat.Todo
	cfg.Cache = chat.Cache
	if chat.grants == nil {
		chat.grants = &agent.PermissionGrants{}
	}
	cfg.Grants = chat.grants
	if chat.review == nil {
		chat.review = &agent.ReviewSession{}
	}
	cfg.Review = chat.review
	cfg.ContextKey = chat.ID
	cfg.Browser = server.browserTools(chat)
	cfg.Terminal = server.terminalTools(chat)
	cfg.Recall = func(ctx context.Context) ([]agent.ChatItem, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		return append([]agent.ChatItem{}, chat.Items...), ctx.Err()
	}
	history := append([]agent.Message{}, chat.History...)
	server.changedLocked(chat, index)
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	inboxValue := agent.Inbox{Steer: func() []agent.Message {
		server.mu.Lock()
		defer server.mu.Unlock()
		out := chat.steer
		chat.steer = nil
		return out
	}, Next: func() (agent.Message, bool) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if len(chat.queue) == 0 {
			return agent.Message{}, false
		}
		out := chat.queue[0]
		chat.queue = chat.queue[1:]
		return out, true
	}}
	go func() {
		defer cancel()
		err := server.backend.Run(ctx, cfg, history, inboxValue, func(kind string, value any) { server.emit(chat, run, kind, value) })
		server.mu.Lock()
		defer server.mu.Unlock()
		if chat.run != run {
			return
		}
		chat.run = nil
		if err != nil {
			server.addLocked(chat, agent.ChatItem{Kind: "error", Text: err.Error()})
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
			run.index = server.addLocked(chat, agent.ChatItem{Kind: "assistant"})
		}
		index = run.index
		chat.Items[index].Text += value.(string)
		run.label, run.mark = "Writing…", "write"
	case "message_end":
		run.index = -1
	case "history":
		chat.History = value.([]agent.Message)
	case "injected":
		m := value.(agent.Message)
		index = server.addLocked(chat, agent.ChatItem{Kind: "user", Text: m.Content, Images: m.Images})
	case "activity":
		status := value.(agent.Activity)
		run.mark = status.State
		run.label = strings.TrimSpace(status.State + " " + status.Tool + " " + status.Detail)
	case "tool":
		outcome := value.(agent.ToolOutcome)
		index = server.addLocked(chat, agent.ChatItem{ID: outcome.RecallID, Kind: "tool", Tool: outcome.Name, Args: outcome.Args})
		run.calls[outcome.ID] = index
		run.states[index] = "running"
	case "tool_exec":
		outcome := value.(agent.ToolOutcome)
		if at, ok := run.calls[outcome.ID]; ok {
			index = at
			run.states[index] = "running"
		}
		run.approval = nil
	case "tool_result":
		outcome := value.(agent.ToolOutcome)
		if at, ok := run.calls[outcome.ID]; ok {
			index = at
			item := &chat.Items[index]
			item.Text, item.Diff, item.Millis, item.Images = outcome.Output, outcome.Diff, outcome.Elapsed.Milliseconds(), outcome.Images
			if outcome.Err != nil {
				item.Failed = outcome.Err.Error()
			}
			delete(run.states, index)
		}
		run.approval, run.question = nil, nil
	case "question":
		request := value.(agent.QuestionRequest)
		run.question = &remoteapi.Question{Chat: chat.ID, ID: request.ID, Questions: request.Questions}
		run.mark, run.label = "approval", "Waiting for your answer"
	case "approval":
		data := value.(map[string]any)
		run.approval = &remoteapi.Approval{Chat: chat.ID, ID: fmt.Sprint(data["id"]), Title: fmt.Sprint(data["title"]), Preview: fmt.Sprint(data["preview"])}
		run.mark, run.label = "approval", "Waiting for your approval"
	case "context_note", "note":
		index = server.addLocked(chat, agent.ChatItem{Kind: "note", Text: value.(string)})
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
