package server

// The headless server's state: chats, settings and paired devices, kept as
// private JSON files in its data directory, and the runs of its agent loop.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	Version      string                  `json:"version,omitempty"`
	Versions     []agent.ChatVersion     `json:"versions,omitempty"`
	Todo         *agent.TodoState        `json:"todo"`
	Cache        *agent.ContextCache     `json:"contextCache,omitempty"`
	grants       *agent.PermissionGrants // what request_permissions granted, until the server stops
	review       *agent.ReviewSession    // the Auto mode reviewer's conversation, until the server stops
	tasks        *agent.BackgroundTasks  // shell commands run in the background, until the server stops
	Browsers     []browser.Info          `json:"browsers,omitempty"`
	run          *serverRun
	tabs         []*serverTab
	session      *browser.Session
	queue, steer []agent.Message
	handoff      *pendingHandoff // how the next turn's new model receives the chat
}

type serverRun struct {
	capabilities chan struct{}
	cancel       context.CancelFunc
	calls        map[string]int
	states       map[int]string
	index        int
	used         int
	label, mark  string
	approval     *remoteapi.Approval
	question     *remoteapi.Question
}

type serverSettings struct {
	Projects          []string           `json:"projects,omitempty"`
	Config            agent.Config       `json:"config"`
	Connections       []agent.Connection `json:"connections"`
	Keys              map[string]string  `json:"keys"`
	UserInstructions  string             `json:"userInstructions"`
	ApprovalMode      string             `json:"approvalMode"`
	ClassicCompaction bool               `json:"classicCompaction"`
}

type headlessServer struct {
	mu                         sync.Mutex
	terminalRevision           uint64
	closing                    bool
	ctx                        context.Context
	shutdown                   context.CancelFunc
	backend                    *agent.App
	engine                     *browser.Engine
	name, root, dir            string
	projects                   []string
	cfg                        agent.Config
	connections                []agent.Connection
	keys                       map[string]string
	instructions, approvalMode string
	classic                    bool
	panelHomes                 map[string]*serverChat // project panels before the first prompt; not persisted
	chats                      []*serverChat
	devices                    []remoteapi.PairedDevice
	pairs                      map[string]*serverPair
	subs                       map[chan serverEvent]bool
	streams                    map[chan struct{}]bool
	choices                    map[string]agent.ModelChoice
	modelReload                sync.Mutex
	modelRevision              uint64
	modelProblems              map[string]string
	limitsCache                map[string]agent.AccountLimits
	logins                     map[string]*mobileLogin
	claudeLogin                *mobileLogin
	loading                    bool
	local                      bool
	bridge                     *mobileBridge
	phoneAvailable             atomic.Bool
	saveTimer                  *time.Timer
	// Local phone mode: the phone app's token hash, apart from the paired
	// network devices, and the network listener while the phone is shared.
	localHash   string
	sharing     *http.Server
	sharingPort int
	fingerprint string
	handler     http.Handler
	tlsConfig   *tls.Config
	// Changes waiting for the next flush to the clients: a streamed answer
	// reaches them at most ten times a second, as the desktop sends it.
	pending  map[*serverChat]map[int]bool
	flushing bool
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
		server.projects = settings.Projects
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
	if !slices.Contains(server.projects, root) {
		server.projects = append([]string{root}, server.projects...)
	}
	if server.connections == nil && (server.cfg.Model != "" || server.cfg.BaseURL != "") {
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
		if chat.Config.Workspace == "" {
			chat.Config.Workspace = root
		}
		if !slices.Contains(server.projects, chat.Config.Workspace) {
			server.projects = append(server.projects, chat.Config.Workspace)
		}
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
	server.closing = true
	shared := server.sharing
	server.sharing = nil
	if server.saveTimer != nil {
		server.saveTimer.Stop()
	}
	for _, chat := range server.chats {
		if chat.run != nil {
			chat.run.cancel()
		}
		chat.tasks.StopAll() // a background server must not outlive this one
	}
	if err := server.saveLocked(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	var tabs []*serverTab
	for _, chat := range server.panelContextsLocked() {
		tabs = append(tabs, chat.tabs...)
		chat.tabs = nil
	}
	server.disconnectStreamsLocked()
	server.mu.Unlock()
	if shared != nil {
		shared.Close()
	}
	for _, tab := range tabs {
		tab.close()
	}
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
	server.modelReload.Lock()
	defer server.modelReload.Unlock()
	server.mu.Lock()
	connections := append([]agent.Connection(nil), server.connections...)
	keys := make(map[string]string, len(server.keys))
	for id, key := range server.keys {
		keys[id] = key
	}
	revision := server.modelRevision
	previous := server.choices
	server.mu.Unlock()
	choices := map[string]agent.ModelChoice{}
	problems := map[string]string{}
	for _, connection := range connections {
		if !connection.Enabled {
			continue
		}
		rows, err := server.backend.ConnectionModels(server.ctx, connection, keys[connection.ID])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Models unavailable for %q: %v\n", connection.Name, err)
			problems[connection.ID] = "Models unavailable"
			for _, choice := range previous {
				if choice.Connection.ID == connection.ID && connection.Includes(choice.ID) {
					choice.Connection = connection
					choices[choice.ID+" · "+connection.Name] = choice
				}
			}
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
	if server.modelRevision != revision {
		return
	}
	server.choices = choices
	server.modelProblems = problems
	server.loading = false
	if server.cfg.Model == "" {
		for _, choice := range choices {
			server.cfg = server.configFor(choice, server.cfg)
			break
		}
	}
	server.broadcastLocked("settings", server.settingsLocked())
	if server.bridge != nil {
		// Local phone can open a saved chat before its accounts finish loading.
		for _, chat := range server.chats {
			server.broadcastLocked("meta", server.metaLocked(chat))
		}
	}
}

func (server *headlessServer) configFor(choice agent.ModelChoice, previous agent.Config) agent.Config {
	workspace := previous.Workspace
	if workspace == "" {
		workspace = server.root
	}
	cfg := choice.Connection.Config(workspace)
	cfg.Model = choice.ID
	cfg.Effort = previous.Effort
	cfg.ServiceTier = agent.TierFor(choice, previous.ServiceTier)
	cfg.ReasoningMode = choice.ReasoningMode
	cfg.Session, cfg.SessionMark = previous.Session, previous.SessionMark
	cfg.RanWith = previous.RanWith // its next turn knows the model changed
	if cfg.RanWith == nil && previous.Provider != "" && previous.Model != "" {
		identity := agent.IdentityOf(previous)
		cfg.RanWith = &identity
	}

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
		model := remoteapi.Model{Label: label, Name: choice.Display(), Detail: choice.Connection.Name, Account: choice.Connection.ID, Efforts: []remoteapi.Effort{}, ServiceTiers: choice.ServiceTiers, DefaultServiceTier: choice.DefaultServiceTier}
		for _, effort := range choice.Efforts {
			model.Efforts = append(model.Efforts, remoteapi.Effort{Value: effort, Label: agent.EffortLabel(effort)})
		}
		models = append(models, model)
	}
	slices.SortFunc(models, func(left, right remoteapi.Model) int {
		return strings.Compare(left.Label, right.Label)
	})
	accounts := []remoteapi.Account{}
	for _, connection := range server.connections {
		if connection.Enabled {
			accounts = append(accounts, remoteapi.Account{ID: connection.ID, Name: connection.Name, Provider: connection.Provider, Kind: agent.ProviderName(connection.Provider), Problem: server.modelProblems[connection.ID]})
		}
	}
	return map[string]any{
		"userPreferences":     server.instructions,
		"preferencesEditable": true,
		"models":              models,
		"accounts":            accounts,
		"model":               server.labelLocked(server.cfg),
		"effort":              server.cfg.Effort,
		"tier":                server.cfg.ServiceTier,
		"window":              agent.ModelContextWindow(server.cfg),
		"approvalMode":        server.approvalMode,
		"autoMode":            agent.AutoModeOffered(server.cfg),
		"loading":             server.loading,
		"computerUse":         false,
		"messageVersions":     true,
		"attachments":         true,
	}
}

func (server *headlessServer) summariesLocked() []remoteapi.Chat {
	out := []remoteapi.Chat{}
	for _, chat := range server.chats {
		out = append(out, remoteapi.Chat{ID: chat.ID, Title: chat.Title, Workspace: chat.Config.Workspace, Running: chat.run != nil, Items: len(chat.Items)})
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
		Tasks:   append([]agent.BackgroundTaskInfo{}, chat.tasks.Snapshot()...),
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
	state := chat.branching()
	state.IdentifyMessages()
	chat.applyBranch(state)
	item := chat.Items[index]
	out := remoteapi.Item{
		ID:       item.ID,
		Versions: remoteapi.ItemVersions(&state, index),
		Kind:     item.Kind,
		Text:     item.Text,
		Images:   len(item.Images),
		Tool:     item.Tool,
		Args:     item.Args,
		Diff:     item.Diff,
		Millis:   item.Millis,
		Failed:   item.Failed,
		State:    "done",
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

// changedLocked queues a chat's change (an item, or only its meta when
// index is -1) for the clients.
func (server *headlessServer) changedLocked(chat *serverChat, index int) {
	if server.pending == nil {
		server.pending = map[*serverChat]map[int]bool{}
	}
	if server.pending[chat] == nil {
		server.pending[chat] = map[int]bool{}
	}
	if index >= 0 {
		server.pending[chat][index] = true
	}
	if !server.flushing {
		server.flushing = true
		time.AfterFunc(100*time.Millisecond, server.flushChanges)
	}
	server.scheduleSaveLocked()
}

// flushChanges sends the queued changes as they stand now: one item event
// per changed item, however many pieces of text it got meanwhile.
func (server *headlessServer) flushChanges() {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.flushing = false
	pending := server.pending
	server.pending = nil
	for chat, indexes := range pending {
		if server.chatLocked(chat.ID) != chat {
			continue // deleted meanwhile
		}
		changed := make([]int, 0, len(indexes))
		for index := range indexes {
			changed = append(changed, index)
		}
		slices.Sort(changed)
		for _, index := range changed {
			if index < len(chat.Items) {
				server.broadcastLocked("item", map[string]any{"chat": chat.ID, "index": index, "item": server.itemLocked(chat, index)})
			}
		}
		server.broadcastLocked("meta", server.metaLocked(chat))
	}
}

func (server *headlessServer) addLocked(chat *serverChat, item agent.ChatItem) int {
	if item.ID == "" {
		item.ID = core.Identifier()[:16]
	}
	chat.Items = append(chat.Items, item)
	return len(chat.Items) - 1
}

func (server *headlessServer) runLocked(chat *serverChat, m agent.Message) {
	server.runWithIDLocked(chat, m, "")
}

// chatTasksLocked are the chat's background tasks, made on first use: their
// end wakes the agent (joining its turn, or starting one), any change
// reaches the clients.
func (server *headlessServer) chatTasksLocked(chat *serverChat) *agent.BackgroundTasks {
	if chat.tasks == nil {
		chat.tasks = &agent.BackgroundTasks{
			OnFinish: func(_ agent.BackgroundTaskInfo, message agent.Message) {
				server.mu.Lock()
				defer server.mu.Unlock()
				if server.chatLocked(chat.ID) != chat || server.closing || server.ctx.Err() != nil {
					return
				}
				if chat.run != nil {
					chat.steer = append(chat.steer, message)
					server.changedLocked(chat, -1)
					return
				}
				server.runLocked(chat, message)
			},
			OnChange: func() {
				server.mu.Lock()
				defer server.mu.Unlock()
				if server.chatLocked(chat.ID) == chat {
					server.broadcastLocked("meta", server.metaLocked(chat))
				}
			},
		}
	}
	return chat.tasks
}

// runConfigLocked is the chat's configuration for a turn, with the server's
// secrets, preferences and tools.
func (server *headlessServer) runConfigLocked(chat *serverChat, capabilities chan struct{}) agent.Config {
	cfg := chat.Config
	cfg.APIKey = server.keys[cfg.ConnectionID]
	cfg.UserInstructions = server.instructions
	cfg.ApprovalMode = agent.ApprovalFor(server.approvalMode, cfg)
	cfg.ClassicCompaction = server.classic
	cfg.Todo = chat.Todo
	cfg.Tasks = server.chatTasksLocked(chat)
	cfg.Cache = chat.Cache
	if chat.grants == nil {
		chat.grants = &agent.PermissionGrants{}
	}
	cfg.Grants = chat.grants
	if chat.review == nil {
		chat.review = &agent.ReviewSession{}
	}
	cfg.Review = chat.review
	cfg.ContextKey = chat.ID + chat.Version
	cfg.Browser = server.browserTools(chat)
	cfg.Terminal = server.terminalTools(chat)
	if server.bridge != nil {
		cfg.CapabilitiesChanged = capabilities
		cfg.Phone = server.bridge.phoneTools
		cfg.Packages = server.bridge.packageTools
		cfg.ToolAllowed = func(name string) bool {
			switch {
			case name == "phone_use" || name == "adb_shell":
				// Android publishes grant/connection changes; a slow bridge must
				// not silently remove tools from a model request. In Approval
				// mode the phone is not offered: each tap would wait for the user.
				return server.phoneAvailable.Load() && cfg.ApprovalMode != "approval"
			case strings.HasPrefix(name, "browser_"):
				return true // WebView is built in; only ADB needs an optional grant.
			case name == "request_permissions":
				return false // Phone mode does not expose filesystem grant expansion.
			default:
				return true
			}
		}
	}
	cfg.Recall = func(ctx context.Context) ([]agent.ChatItem, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		return append([]agent.ChatItem{}, chat.Items...), ctx.Err()
	}
	return cfg
}

func (server *headlessServer) runWithIDLocked(chat *serverChat, m agent.Message, id string) {
	chat.History = append(chat.History, m)
	item := agent.ChatItem{ID: id, Kind: "user", Text: m.Content, Images: m.Images}
	if m.Internal { // a background task's end: a note, not the user's words
		item = agent.ChatItem{ID: id, Kind: "note", Text: agent.WakeSummary(m)}
	}
	index := server.addLocked(chat, item)
	ctx, cancel := context.WithCancel(server.ctx)
	run := &serverRun{cancel: cancel, capabilities: make(chan struct{}, 1), calls: map[string]int{}, states: map[int]string{}, index: -1, label: "Waiting for the model…", mark: "wait"}
	chat.run = run
	cfg := server.runConfigLocked(chat, run.capabilities)
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
	// A handoff to apply first: the one chosen, or the recommended one for a
	// turn that did not come from the prompt (a queued message, a wake-up).
	handoff := chat.handoff
	chat.handoff = nil
	if handoff == nil && agent.HandoffNeeded(cfg, history[:len(history)-1]) {
		plan, previous := server.handoffPlanLocked(chat, cfg, history[:len(history)-1])
		handoff = &pendingHandoff{choice: plan.Recommended, plan: plan, previous: previous}
	}
	go func() {
		defer cancel()
		emit := func(kind string, value any) { server.emit(chat, run, kind, value) }
		var err error
		if handoff != nil {
			prompt := history[len(history)-1]
			var result agent.HandoffResult
			result, err = server.backend.ApplyHandoff(ctx, handoff.previous, cfg, history[:len(history)-1], handoff.choice, handoff.plan, emit)
			if err == nil {
				history = append(result.History, prompt)
				cfg.HandoffSummary = result.Summary
				cfg.HandoffApplied = true
				emit("history", append([]agent.Message(nil), history...))
				emit("context_note", result.Note)
			}
		}
		if err == nil {
			identity := agent.IdentityOf(cfg)
			server.mu.Lock()
			if chat.run == run {
				chat.Config.RanWith = &identity
			}
			server.mu.Unlock()
			err = server.backend.Run(ctx, cfg, history, inboxValue, emit)
		}
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
		if m.Internal {
			index = server.addLocked(chat, agent.ChatItem{Kind: "note", Text: agent.WakeSummary(m)})
		} else {
			index = server.addLocked(chat, agent.ChatItem{Kind: "user", Text: m.Content, Images: m.Images})
		}
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
	case "session_synced": // the session has seen everything shown so far
		if chat.Config.Session != "" {
			chat.Config.SessionMark = len(chat.Items)
		}
	case "notice":
		run.label = value.(string)
	}
	server.changedLocked(chat, index)
}

func (server *headlessServer) saveSettingsLocked() error {
	return saveServerJSON(filepath.Join(server.dir, "config.json"), serverSettings{
		Config:            server.cfg,
		Projects:          server.projects,
		Connections:       server.connections,
		Keys:              server.keys,
		UserInstructions:  server.instructions,
		ApprovalMode:      server.approvalMode,
		ClassicCompaction: server.classic,
	})
}
