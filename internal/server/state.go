// SPDX-License-Identifier: MPL-2.0

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
	"1loop/internal/sshmachine"
)

type serverChat struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Config   agent.Config        `json:"config"`
	History  []agent.Message     `json:"history"`
	Items    []agent.ChatItem    `json:"items"`
	Version  string              `json:"version,omitempty"`
	Versions []agent.ChatVersion `json:"versions,omitempty"`
	Todo     *agent.TodoState    `json:"todo"`
	Cache    *agent.ContextCache `json:"contextCache,omitempty"`
	// ContextUsage is Claude Code's last count of the window by category.
	ContextUsage *agent.ContextUsage     `json:"contextUsage,omitempty"`
	grants       *agent.PermissionGrants // what request_permissions granted, until the server stops
	review       *agent.ReviewSession    // the Auto mode reviewer's conversation, until the server stops
	tasks        *agent.BackgroundTasks  // shell commands run in the background, until the server stops
	Browsers     []browser.Info          `json:"browsers,omitempty"`
	// Subchat links a subchat to the chat that started it (subchats.go).
	Subchat *agent.SubchatLink `json:"subchat,omitempty"`
	// ReportTo: the chats whose message caused the running turn, told when
	// it ends (chats.go). Saved, so a turn cut off by a restart shows so.
	ReportTo     []string `json:"reportTo,omitempty"`
	hops         int      // messages between chats since the user last wrote (agent.TurnHops)
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
	tool         string // executing now, from tool_exec to its tool_result
	approval     *remoteapi.Approval
	question     *remoteapi.Question
}

type serverSettings struct {
	Projects          []string           `json:"projects"`
	Config            agent.Config       `json:"config"`
	Connections       []agent.Connection `json:"connections"`
	Keys              map[string]string  `json:"keys"`
	UserInstructions  string             `json:"userInstructions"`
	ApprovalMode      string             `json:"approvalMode"`
	ClassicCompaction bool               `json:"classicCompaction"`
	MemoryOff         bool               `json:"memoryOff,omitempty"`
	WrapUpOff         bool               `json:"wrapUpOff,omitempty"`
	AutoResume        *agent.AutoResume  `json:"autoResume,omitempty"`
	// Worktrees of the projects, by folder: their chats belong to the project.
	Worktrees map[string]string `json:"worktrees,omitempty"`
	// SSH machines (machines.go), and the projects that are folders of one.
	SSHMachines     []serverMachine   `json:"sshMachines,omitempty"`
	ProjectMachines map[string]string `json:"projectMachines,omitempty"`
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
	worktrees                  map[string]string              // worktree folder → project (worktrees.go)
	machines                   []serverMachine                // SSH machines (machines.go)
	projectMachines            map[string]string              // project folder there → machine ID
	connected                  map[string]*sshmachine.Machine // machines used since the start, by ID
	trust                      *hostTrust
	cfg                        agent.Config
	connections                []agent.Connection
	keys                       map[string]string
	instructions, approvalMode string
	classic                    bool
	memoryOff                  bool                   // the agent's memory tool turned off
	wrapUpOff                  bool                   // the next-step card (wrap_up tool) turned off
	autoResume                 *agent.AutoResume      // what a rate limit does to a run, for every chat
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
	webLeases                  map[string]*webLease // ChatGPT (Web) pages the phone's panel shows, by chat
	claudeLogin                *mobileLogin
	loading                    bool
	local                      bool
	bridge                     *mobileBridge
	phoneAvailable             atomic.Bool
	saveTimer                  *time.Timer
	saveLock                   sync.Mutex // one state write at a time, the newest last
	saveCount, saveDone        int        // states taken (under mu), the newest written (under saveLock)
	// Local phone mode: the phone app's token hash, apart from the paired
	// network devices, and the network listener while the phone is shared.
	localHash   string
	sharing     *http.Server
	sharingPort int
	fingerprint string
	handler     http.Handler
	tlsConfig   *tls.Config
	webDir      string // the browser client, served at /app/ (web.go)
	// While no device is paired: the secret of the one-time setup link,
	// which pairs the first device without the console (security.go).
	setup     string
	setupPort int
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
		trust:        &hostTrust{out: os.Stderr},
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
		server.worktrees = settings.Worktrees
		server.machines = settings.SSHMachines
		server.projectMachines = settings.ProjectMachines
		server.cfg = settings.Config
		server.connections = settings.Connections
		if settings.Keys != nil {
			server.keys = settings.Keys
		}
		server.instructions = settings.UserInstructions
		server.classic = settings.ClassicCompaction
		server.memoryOff = settings.MemoryOff
		server.wrapUpOff = settings.WrapUpOff
		server.autoResume = settings.AutoResume
		if settings.ApprovalMode != "" {
			server.approvalMode = settings.ApprovalMode
		}
	} else if customConfig || !os.IsNotExist(err) {
		return nil, err
	}
	if server.projects == nil {
		server.projects = []string{root}
	}
	if !slices.Contains(server.projects, server.cfg.Workspace) && server.worktrees[server.cfg.Workspace] == "" {
		server.cfg.Workspace = ""
		if len(server.projects) > 0 {
			server.cfg.Workspace = server.projects[0]
		}
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
		if !chat.Config.InScratch() && !slices.Contains(server.projects, chat.Config.Workspace) && server.worktrees[chat.Config.Workspace] == "" {
			server.projects = append(server.projects, chat.Config.Workspace)
		}
		if chat.Todo == nil {
			chat.Todo = &agent.TodoState{}
		}
		if chat.Cache == nil {
			chat.Cache = &agent.ContextCache{}
		}
		chat.Versions = agent.ExpandVersions(chat.Versions, chat.History, chat.Items)
		chat.Subchat.Restored(len(chat.ReportTo) > 0)
		chat.ReportTo = nil
	}
	return server, nil
}

func (server *headlessServer) saveLocked() error {
	return server.stateLocked()()
}

// stateLocked takes the state to save, under the lock, and returns what
// writes it: encoding and writing every chat can take seconds, during which
// the phone's requests and the agents' events must not wait for the lock.
func (server *headlessServer) stateLocked() (write func() error) {
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
	// Messages are never edited in place: copying the lists is enough.
	chats := make([]*serverChat, len(server.chats))
	for i, chat := range server.chats {
		chats[i] = &serverChat{ID: chat.ID, Title: chat.Title, Config: chat.Config, History: append([]agent.Message{}, chat.History...),
			Items: append([]agent.ChatItem{}, chat.Items...), Version: chat.Version, Versions: agent.CopyVersions(chat.Versions),
			Todo: chat.Todo.Copy(), Cache: chat.Cache.Copy(), ContextUsage: chat.ContextUsage, Browsers: append([]browser.Info{}, chat.Browsers...),
			Subchat: chat.Subchat.Copy(), ReportTo: append([]string(nil), chat.ReportTo...)} // its parent: forgetting it detached every subchat on restart (#356)
	}
	devices := append([]remoteapi.PairedDevice{}, server.devices...)
	server.saveCount++
	count, path := server.saveCount, filepath.Join(server.dir, "state.json")
	return func() error {
		server.saveLock.Lock()
		defer server.saveLock.Unlock()
		if count < server.saveDone {
			return nil // a newer state is already on disk
		}
		server.saveDone = count
		for _, chat := range chats {
			chat.Versions = agent.CompactVersions(chat.Versions, chat.History, chat.Items)
		}
		return saveServerJSON(path, struct {
			Chats   []*serverChat            `json:"chats"`
			Devices []remoteapi.PairedDevice `json:"devices"`
		}{chats, devices})
	}
}

func (server *headlessServer) scheduleSaveLocked() {
	if server.saveTimer != nil {
		server.saveTimer.Stop()
	}
	server.saveTimer = time.AfterFunc(400*time.Millisecond, func() {
		server.mu.Lock()
		write := server.stateLocked()
		server.mu.Unlock()
		if err := write(); err != nil {
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
	server.backend.CloseChatGPTWeb()
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
	normalize := func(cfg *agent.Config) {
		for _, choice := range choices {
			if cfg.Model == "default" && choice.ID == cfg.Model && choice.Connection.ID == cfg.ConnectionID {
				cfg.DefaultPreset = len(choice.Connection.DefaultModels) > 0
				if cfg.DefaultPreset && !agent.Contains(choice.Efforts, cfg.Effort) {
					cfg.Effort = choice.DefaultEffort
				}
			}
		}
	}
	normalize(&server.cfg)
	for _, chat := range server.chats {
		normalize(&chat.Config)
	}
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
	cfg.DefaultPreset = choice.IsDefaultPreset()
	cfg.Effort = previous.Effort
	cfg.ServiceTier = agent.TierFor(choice, previous.ServiceTier)
	cfg.ReasoningMode = choice.ReasoningMode
	cfg.InheritSession(previous)
	cfg.WebConversation = previous.WebConversation
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
		model := remoteapi.Model{ID: choice.ID, Label: label, Name: choice.Display(), Detail: choice.Connection.Name, Account: choice.Connection.ID, Family: agent.FamilyOf(choice).Name, Efforts: []remoteapi.Effort{}, DefaultEffort: choice.DefaultEffort, ServiceTiers: choice.ServiceTiers, DefaultServiceTier: choice.DefaultServiceTier}
		if connection, ok := server.connectionLocked(choice.Connection.ID); ok {
			model.CompactAt = connection.ModelOptions[choice.ID].CompactAt
			cfg := connection.Config("")
			cfg.Model = choice.ID
			model.CompactDefault, _ = agent.CompactDefault(cfg)
			model.CompactHelp = agent.CompactHelp(cfg)
			model.CompactSupported = connection.Provider != "chatgptweb" && !choice.IsDefaultPreset()
		}
		for _, effort := range choice.Efforts {
			model.Efforts = append(model.Efforts, remoteapi.Effort{Value: effort, Label: agent.EffortLabel(effort), About: choice.DefaultAbout(effort)})
		}
		models = append(models, model)
	}
	slices.SortFunc(models, func(left, right remoteapi.Model) int {
		if left.Account != right.Account {
			return strings.Compare(left.Account, right.Account)
		}
		if order := agent.CompareModels(server.choices[left.Label], server.choices[right.Label]); order != 0 {
			return order
		}
		return strings.Compare(left.Label, right.Label)
	})
	accounts := []remoteapi.Account{}
	for _, connection := range server.connections {
		if connection.Enabled {
			accounts = append(accounts, remoteapi.Account{ID: connection.ID, Name: connection.Name, Provider: connection.Provider, Kind: agent.ProviderName(connection.Provider), Problem: server.modelProblems[connection.ID], DefaultModels: connection.DefaultModels})
		}
	}
	return map[string]any{
		"defaultPresets":      true,
		"autoResume":          server.autoResume,
		"userPreferences":     server.instructions,
		"preferencesEditable": true,
		"memory":              !server.memoryOff,
		"wrapUp":              !server.wrapUpOff,
		"models":              models,
		"accounts":            accounts,
		"model":               server.labelLocked(server.cfg),
		"effort":              server.cfg.Effort,
		"tier":                server.cfg.ServiceTier,
		"window":              agent.ModelContextWindow(server.resolveModelLocked(server.cfg)),
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
		status, blocked := agent.WrapUpStatus(chat.Items)
		summary := remoteapi.Chat{ID: chat.ID, Title: chat.Title, Workspace: chat.Config.Workspace, Scratch: chat.Config.InScratch(), Machine: server.machineLabelLocked(chat.Config.MachineID), Running: chat.run != nil, Items: len(chat.Items), Status: status, Blocked: blocked}
		if chat.Subchat != nil {
			summary.Parent = chat.Subchat.Parent
			if server.subchatWorktreeLocked(chat) != "" {
				summary.Branch = chat.Subchat.Branch
			}
		}
		out = append(out, summary)
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
		Window:  agent.ModelContextWindow(server.resolveModelLocked(chat.Config)),
		Todo:    []remoteapi.Todo{},
		Tasks:   append([]agent.BackgroundTaskInfo{}, chat.tasks.Snapshot()...),
	}
	meta.Provider = chat.Config.Provider
	for _, m := range chat.queue {
		meta.Queue = append(meta.Queue, m.Typed())
	}
	for _, m := range chat.steer {
		meta.Steer = append(meta.Steer, m.Typed())
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
		meta.Tool = chat.run.tool
		if chat.run.used > 0 {
			meta.Used = chat.run.used
		}
	}
	if parent := server.parentLocked(chat); parent != nil {
		meta.Parent, meta.ParentTitle = parent.ID, parent.Title
	}
	for _, sub := range server.subchatsLocked(chat) {
		meta.Subchats = append(meta.Subchats, server.subchatInfoLocked(sub))
	}
	meta.ContextStatus = agent.TaskContextText(chat.Config, meta.Used, meta.Window, server.classic, meta.Mark)
	meta.ContextUsage = agent.ChatContextUsage(chat.Config, chat.History, chat.ContextUsage, server.classic).Summary()
	return meta
}

func (server *headlessServer) itemLocked(chat *serverChat, index int) remoteapi.Item {
	state := chat.branching()
	if chat.Items[index].ID == "" { // the whole chat: per item, a page cost its square
		state.IdentifyMessages()
		chat.applyBranch(state)
	}
	item := chat.Items[index]
	out := remoteapi.Item{
		ID:       item.ID,
		Versions: remoteapi.ItemVersions(&state, index),
		Kind:     item.Kind,
		Text:     agent.ToolText(item),
		Images:   agent.ToolImageCount(item),
		Files:    remoteapi.FileNames(item.Files),
		Tool:     item.Tool,
		Args:     item.Args,
		Diff:     item.Diff,
		Millis:   item.Millis,
		Failed:   item.Failed,
		From:     item.From,
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
				server.wakeLocked(chat, message)
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
	if connection, ok := server.connectionLocked(cfg.ConnectionID); ok {
		cfg = agent.ResolveDefault(cfg, connection, server.choices)
		cfg.CompactAt = connection.ModelOptions[cfg.Model].CompactAt
	}
	cfg.APIKey = server.keys[cfg.ConnectionID]
	if machine := server.machineLocked(cfg.MachineID); machine != nil {
		cfg.Machine = machine // else the run refuses: its machine was removed
	}
	cfg.UserInstructions = server.instructions
	cfg.WrapUpOff = server.wrapUpOff
	if !server.memoryOff {
		cfg.Preferences = server.updatePreferences
	}
	cfg.Settings = server.settingsAccess()
	cfg.Subchats = server.subchatAccess(chat)
	cfg.Chats = server.chatAccess(chat)
	cfg.ApprovalMode = agent.ApprovalFor(server.approvalMode, cfg)
	if cfg.InScratch() { // change_directory; the phone has no folder picker for request_directory yet
		cfg.Move = &agent.WorkspaceMove{}
		if server.local {
			cfg.Move.Within = server.projectBase()
		}
		cfg.RecentProjects = append([]string(nil), server.projects...)
	}
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
	if connection, ok := server.connectionLocked(cfg.ConnectionID); ok && cfg.Provider == "chatgptweb" {
		cfg.WebSettings = connection.WebSettings // the tunnel as last saved, not as when the model was chosen
	}
	cfg.Browser = server.browserTools(chat)
	cfg.Terminal = server.terminalTools(chat)
	if server.bridge != nil {
		cfg.CapabilitiesChanged = capabilities
		cfg.Phone = server.bridge.phoneTools
		cfg.Packages = server.bridge.packageTools
		cfg.ToolAllowed = func(name string) bool {
			switch {
			case agent.IsAPKTool(name):
				return true // the installer, not ADB: no Shizuku, and one approval per call even in Approval mode
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
	cfg.AutoResume = server.autoResume
	cfg.ResumeModel = func(ctx context.Context, step agent.ResumeStep) (agent.Config, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if ctx.Err() != nil {
			return agent.Config{}, ctx.Err()
		}
		for _, choice := range server.choices {
			if choice.Connection.ID == step.ConnectionID && choice.ID == step.Model && choice.Connection.Enabled {
				return agent.ResumeConfig(server.runConfigLocked(chat, capabilities), choice, server.keys[choice.Connection.ID], step.Effort), nil
			}
		}
		return agent.Config{}, fmt.Errorf("the selected model is no longer available")
	}
	cfg.ResumeSwitched = func(ctx context.Context, next agent.Config) error {
		server.mu.Lock()
		defer server.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		identity := agent.IdentityOf(next)
		chat.Config = agent.ResumeStoredConfig(next)
		chat.Config.RanWith = &identity
		server.changedLocked(chat, -1)
		server.scheduleSaveLocked()
		return nil
	}
	return cfg
}

func (server *headlessServer) runWithIDLocked(chat *serverChat, m agent.Message, id string) {
	chat.History = append(chat.History, m)
	chat.hops = agent.TurnHops(chat.hops, false, m)
	// The user's words, another chat's under its name, or a note for
	// 1loop's own (a task's end, a report).
	index := server.addLocked(chat, agent.MessageItem(id, m))
	ctx, cancel := context.WithCancel(server.ctx)
	run := &serverRun{cancel: cancel, capabilities: make(chan struct{}, 1), calls: map[string]int{}, states: map[int]string{}, index: -1, label: "Waiting for the model…", mark: "wait"}
	chat.run = run
	if parent := server.parentLocked(chat); parent != nil { // its state in the parent's Tasks
		server.broadcastLocked("meta", server.metaLocked(parent))
	}
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
		ended := agent.SubchatDone
		if err != nil {
			server.addLocked(chat, agent.ChatItem{Kind: "error", Text: err.Error()})
			ended = agent.SubchatFailed
			if ctx.Err() == context.Canceled {
				ended = agent.SubchatStopped
			}
		}
		moved := server.finishMoveLocked(chat, cfg.Move)
		server.broadcastLocked("replace", map[string]string{"chat": chat.ID})
		server.broadcastLocked("meta", server.metaLocked(chat))
		server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
		server.scheduleSaveLocked()
		// The turn used the account: its limits are asked again the next
		// time someone looks at them, as on the desktop.
		if limits, ok := server.limitsCache[chat.Config.ConnectionID]; ok {
			limits.Checked = time.Time{}
			server.limitsCache[chat.Config.ConnectionID] = limits
		}
		if server.ctx.Err() != nil {
			return
		}
		if moved != "" { // the agent goes on in the new folder
			server.runLocked(chat, agent.Message{Role: "user", Internal: true, Content: moved})
			return
		}
		// A message can arrive after the agent's last look at its inbox and
		// before this: it starts the next turn rather than wait unseen.
		if len(chat.steer) > 0 && err == nil {
			next := chat.steer[0]
			chat.steer = chat.steer[1:]
			server.runLocked(chat, next)
		} else if len(chat.queue) > 0 {
			next := chat.queue[0]
			chat.queue = chat.queue[1:]
			server.runLocked(chat, next)
		}
		if chat.run == nil { // the last turn of the chain: it reports to who caused it
			server.turnEndedLocked(chat, ended)
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
	case "text": // the streaming answer rewritten as a whole (ChatGPT (Web))
		if run.index < 0 {
			run.index = server.addLocked(chat, agent.ChatItem{Kind: "assistant"})
		}
		index = run.index
		chat.Items[index].Text = value.(string)
	case "message_end":
		run.index = -1
	case "history":
		chat.History = value.([]agent.Message)
	case "injected":
		m := value.(agent.Message)
		chat.hops = agent.TurnHops(chat.hops, true, m)
		index = server.addLocked(chat, agent.MessageItem("", m))
	case "activity":
		status := value.(agent.Activity)
		run.mark = status.State
		run.label = strings.TrimSpace(status.State + " " + status.Tool + " " + status.Detail)
		if status.State == "call" {
			run.mark = agent.CallMark(status.Tool)
			run.label = agent.CallLabel(status.Tool, status.Detail) + "…"
		}
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
		run.tool = outcome.Name
		// The row's own words, as the desktop says them (agent.DescribeTool).
		verb, target, _ := agent.DescribeTool(agent.ChatItem{Tool: outcome.Name, Args: outcome.Args}, true)
		run.mark, run.label = agent.CallMark(outcome.Name), strings.TrimSpace(verb+" "+target)+"…"
	case "tool_output": // a running command's output so far
		outcome := value.(agent.ToolOutcome)
		if at, ok := run.calls[outcome.ID]; ok {
			index = at
			chat.Items[index].Text = outcome.Output
		}
	case "tool_result":
		outcome := value.(agent.ToolOutcome)
		if run.tool == outcome.Name {
			run.tool = ""
		}
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
	case "context_usage":
		usage := value.(agent.ContextUsage)
		chat.ContextUsage = &usage
	case "web_session": // the ChatGPT (Web) conversation the next turn continues
		web := value.(agent.WebConversation)
		chat.Config.WebConversation = &web
	case "session":
		chat.Config.SetClaudeSession(value.(string))
	case "session_synced": // the session has seen everything shown so far
		chat.Config.SyncClaudeSession(len(chat.Items))
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
		MemoryOff:         server.memoryOff,
		WrapUpOff:         server.wrapUpOff,
		AutoResume:        server.autoResume,
		Worktrees:         server.worktrees,
		SSHMachines:       server.machines,
		ProjectMachines:   server.projectMachines,
	})
}

func (server *headlessServer) resolveModelLocked(cfg agent.Config) agent.Config {
	if connection, ok := server.connectionLocked(cfg.ConnectionID); ok {
		return agent.ResolveDefault(cfg, connection, server.choices)
	}
	return cfg
}
