package server

// The headless server's HTTP API: the same routes as the desktop's phone
// remote (internal/ui/phone_server.go), served over HTTPS to paired devices only.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/extensions"
	"1loop/internal/githubconnector"
	"1loop/internal/gitrepo"
	"1loop/internal/remoteapi"
)

func (server *headlessServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		core.WriteJSON(w, 200, map[string]any{"name": server.name, "protocol": 1, "headless": true, "secure": true, "consolePairing": !server.local})
	})
	if !server.local {
		mux.HandleFunc("POST /api/pair", server.pair)
		mux.HandleFunc("POST /api/pair/confirm", server.confirmPair)
	} else {
		mux.HandleFunc("POST /api/local/capabilities", server.localOnly(func(w http.ResponseWriter, r *http.Request) {
			var state struct{ Phone bool }
			if core.ReadJSON(r, &state) != nil {
				core.Fail(w, 400, "Invalid capability state")
				return
			}
			server.phoneAvailable.Store(state.Phone)
			server.mu.Lock()
			for _, chat := range server.chats {
				if chat.run != nil {
					select {
					case chat.run.capabilities <- struct{}{}:
					default:
					}
				}
			}
			server.mu.Unlock()
			core.WriteJSON(w, 200, map[string]bool{"ok": true})
		}))
		mux.HandleFunc("GET /api/local/busy", server.localOnly(server.localBusy))
		mux.HandleFunc("GET /api/local/connections", server.localOnly(server.localConnections))
		mux.HandleFunc("POST /api/local/connections", server.localOnly(server.saveLocalConnection))
		mux.HandleFunc("POST /api/local/connections/models", server.localOnly(server.draftLocalConnectionModels))
		mux.HandleFunc("GET /api/local/connections/{id}/models", server.localOnly(server.localConnectionModels))
		mux.HandleFunc("POST /api/local/connections/{id}/login", server.localOnly(server.localConnectionLogin))
		mux.HandleFunc("POST /api/local/connections/{id}/disconnect", server.localOnly(server.localConnectionDisconnect))
		mux.HandleFunc("POST /api/local/connections/refresh", server.localOnly(server.refreshLocalModels))
		mux.HandleFunc("GET /api/local/connections/{id}/web", server.localOnly(server.localWebTunnel))
		mux.HandleFunc("POST /api/local/connections/{id}/web", server.localOnly(server.localWebTunnel))
		mux.HandleFunc("POST /api/local/chats/{id}/chatgpt-web", server.localOnly(server.localWebPage))
		mux.HandleFunc("GET /api/local/claude/login", server.localOnly(server.claudeLoginState))
		mux.HandleFunc("POST /api/local/claude/login", server.localOnly(server.beginClaudeLogin))
		mux.HandleFunc("POST /api/local/sync", server.localOnly(server.localSync))
		mux.HandleFunc("GET /api/local/sharing", server.localOnly(server.sharingState))
		mux.HandleFunc("POST /api/local/sharing", server.localOnly(server.setSharing))
		mux.HandleFunc("POST /api/local/pairings/{id}", server.localOnly(server.answerPairing))
		mux.HandleFunc("POST /api/local/devices/{index}/revoke", server.localOnly(server.revokeDevice))
		mux.HandleFunc("POST /api/local/shutdown", server.localOnly(func(w http.ResponseWriter, r *http.Request) {
			core.WriteJSON(w, 200, map[string]bool{"ok": true})
			server.shutdown()
		}))
	}
	endpoints := map[string]http.HandlerFunc{
		"GET /api/projects":                            server.projectFolders,
		"POST /api/projects":                           server.addProject,
		"POST /api/projects/{action}":                  server.changeProject,
		"GET /api/state":                               server.state,
		"GET /api/limits":                              server.limits,
		"GET /api/chats/{id}":                          server.chat,
		"POST /api/chats/{id}/delete":                  server.deleteChat,
		"POST /api/chats/{id}/tasks/{task}/stop":       server.stopTask,
		"POST /api/browser/clear":                      server.clearBrowser,
		"GET /api/events":                              server.events,
		"POST /api/chats/{id}/message":                 server.editMessage,
		"POST /api/chats/{id}/version":                 server.selectVersion,
		"POST /api/attachments":                        server.uploadAttachment,
		"GET /api/update":                              server.updateStatus,
		"POST /api/update":                             server.installUpdate,
		"GET /api/files":                               server.projectFiles(remoteapi.ListFiles),
		"GET /api/files/content":                       server.projectFiles(remoteapi.FileContent),
		"GET /api/files/download":                      server.projectFiles(remoteapi.DownloadFile),
		"POST /api/files/upload":                       server.projectFiles(remoteapi.UploadFile),
		"POST /api/send":                               server.send,
		"POST /api/stop":                               server.stop,
		"POST /api/approve":                            server.approve,
		"POST /api/answer":                             server.answer,
		"POST /api/preferences":                        server.setPreferences,
		"POST /api/memory":                             server.setMemory,
		"POST /api/model":                              server.setModel,
		"POST /api/auto-resume":                        server.setAutoResume,
		"POST /api/model/compaction":                   server.setModelCompaction,
		"POST /api/approval-mode":                      server.setApprovalMode,
		"GET /api/chats/{id}/panel":                    server.panelTabs,
		"GET /api/chats/{id}/panel/{key}":              server.panelTab,
		"GET /api/chats/{id}/panel/{key}/screen":       server.panelScreen,
		"GET /api/chats/{id}/panel/{key}/stream":       server.panelStream,
		"POST /api/chats/{id}/panel/{key}/input":       server.panelInput,
		"POST /api/chats/{id}/panel":                   server.editPanel,
		"GET /api/hooks":                               remoteapi.GetHooks,
		"POST /api/hooks":                              remoteapi.SaveHooks,
		"GET /api/chats/{id}/items/{index}/images/{n}": server.itemImage,
	}
	syncEngine := server.syncEngine()
	syncManifest, syncExport, syncImport := syncEngine.Routes()
	endpoints["GET /api/sync/base"] = syncEngine.BaseRoute
	endpoints["POST /api/sync/base"] = syncEngine.BaseRoute
	endpoints["GET /api/sync/manifest"] = syncManifest
	endpoints["POST /api/sync/export"] = syncExport
	endpoints["POST /api/sync/import"] = syncImport
	// Skills, plugins and MCP servers (internal/extensions); a change is
	// told to the clients.
	for path, handler := range extensions.Handlers(func() {
		server.mu.Lock()
		server.broadcastLocked("extensions", map[string]bool{"changed": true})
		server.mu.Unlock()
	}) {
		endpoints[path] = handler
	}
	for path, handler := range githubconnector.Handlers() {
		endpoints[path] = handler
	}
	for path, handler := range gitrepo.Handlers(gitTarget{server}, githubconnector.Default.Authorizations) {
		endpoints[path] = handler
	}
	for path, handler := range endpoints {
		mux.HandleFunc(path, server.authorized(handler))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/attachments", "/api/files/upload", "/api/sync/import":
			// Files: their handlers set their own limits.
		case "/api/sync/export":
			r.Body = http.MaxBytesReader(w, r.Body, 64<<20) // the list of a large project's files
		default:
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (server *headlessServer) state(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	core.WriteJSON(w, 200, map[string]any{
		"name":              server.name,
		"projects":          server.projects,
		"worktrees":         server.worktrees,
		"workspace":         server.cfg.Workspace,
		"projectManagement": true,
		"projectActions":    true,
		"chats":             server.summariesLocked(),
		"settings":          server.settingsLocked(),
	})
}

func (server *headlessServer) chat(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.chatLocked(r.PathValue("id"))
	if chat == nil {
		core.Fail(w, 404, "Chat not found")
		return
	}
	first, end := remoteapi.Page(r, len(chat.Items))
	items := make([]remoteapi.Item, 0, end-first)
	for i := first; i < end; i++ {
		items = append(items, server.itemLocked(chat, i))
	}
	core.WriteJSON(w, 200, map[string]any{
		"chat":  remoteapi.Chat{ID: chat.ID, Title: chat.Title, Workspace: chat.Config.Workspace, Running: chat.run != nil, Items: len(chat.Items)},
		"items": items,
		"first": first,
		"total": len(chat.Items),
		"meta":  server.metaLocked(chat),
	})
}

func (server *headlessServer) deleteChat(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	chat := server.chatLocked(r.PathValue("id"))
	if chat == nil {
		server.mu.Unlock()
		core.Fail(w, 404, "Chat not found")
		return
	}
	previous := server.chats
	kept := make([]*serverChat, 0, len(previous)-1)
	for _, candidate := range previous {
		if candidate != chat {
			kept = append(kept, candidate)
		}
	}
	server.chats = kept
	if err := server.saveLocked(); err != nil {
		server.chats = previous
		server.mu.Unlock()
		core.Fail(w, 500, err.Error())
		return
	}
	if chat.run != nil {
		chat.run.cancel()
		chat.run = nil
	}
	chat.queue, chat.steer = nil, nil
	chat.tasks.StopAll()
	tabs := chat.tabs
	chat.tabs = nil
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	server.mu.Unlock()
	for _, tab := range tabs {
		tab.close()
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// clearBrowser erases what the agent's browser keeps: cookies and logins,
// cache, site data, history. On the phone the browser is Android's
// WebView: the app clears it, over its bridge.
func (server *headlessServer) clearBrowser(w http.ResponseWriter, r *http.Request) {
	if server.bridge != nil { // the phone's WebView, for a device paired with it
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var reply map[string]any
		if err := server.bridge.call(ctx, "browser_clear", nil, &reply); err != nil {
			core.Fail(w, 500, "Some data could not be cleared: "+err.Error())
			return
		}
		core.WriteJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if err := browser.ClearData(server.engine); err != nil {
		core.Fail(w, 500, "Some data could not be cleared: "+err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// itemImage serves a picture of an item: what the model saw.
func (server *headlessServer) itemImage(w http.ResponseWriter, r *http.Request) {
	remoteapi.ItemImage(w, r, func(index int) ([]core.ImagePart, bool) {
		server.mu.Lock()
		defer server.mu.Unlock()
		chat := server.chatLocked(r.PathValue("id"))
		if chat == nil || index < 0 || index >= len(chat.Items) {
			return nil, false
		}
		return chat.Items[index].Images, true
	})
}

// stopTask is a background task's own Stop.
func (server *headlessServer) stopTask(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	chat := server.chatLocked(r.PathValue("id"))
	server.mu.Unlock()
	if chat == nil || !chat.tasks.Stop(r.PathValue("task")) {
		core.Fail(w, 404, "This task is not running")
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		core.Fail(w, 500, "Streaming unsupported")
		return
	}
	channel := make(chan serverEvent, 256)
	server.mu.Lock()
	server.subs[channel] = true
	server.mu.Unlock()
	defer func() { server.mu.Lock(); delete(server.subs, channel); server.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, ": 1loop\n\n")
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-server.ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
		case event, ok := <-channel:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.kind, event.data); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}

func (server *headlessServer) send(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Chat, Text, Mode, Workspace string
		Attachments                 []agent.FileAttachment
		Images                      []core.ImagePart
		Handoff                     *agent.HandoffChoice // the answer to a handoff the server asked for
	}
	if core.ReadJSON(r, &args) != nil || strings.TrimSpace(args.Text) == "" && len(args.Attachments) == 0 && len(args.Images) == 0 {
		core.Fail(w, 400, "Nothing to send")
		return
	}
	server.mu.Lock()
	chat := server.chatLocked(args.Chat)
	workspace := args.Workspace
	if chat != nil {
		if workspace != "" && workspace != chat.Config.Workspace {
			server.mu.Unlock()
			core.Fail(w, 400, "This chat belongs to another project")
			return
		}
		workspace = chat.Config.Workspace
	}
	if args.Chat != "" && chat == nil {
		server.mu.Unlock()
		core.Fail(w, 404, "Chat not found")
		return
	}
	workspace, err := server.workspaceLocked(workspace)
	server.mu.Unlock()
	if err != nil {
		core.Fail(w, 403, err.Error())
		return
	}
	m, err := remoteapi.AttachedMessage(workspace, args.Text, args.Attachments)
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	m.Images = append(m.Images, args.Images...)
	server.mu.Lock()
	defer server.mu.Unlock()
	if chat == nil {
		if server.cfg.Model == "" {
			core.Fail(w, 409, "Configure a provider and model on the server first")
			return
		}
		titleText := args.Text
		if strings.TrimSpace(titleText) == "" {
			if len(args.Attachments) > 0 {
				titleText = args.Attachments[0].Name
			} else {
				titleText = "Image"
			}
		}
		title := []rune(strings.Join(strings.Fields(titleText), " "))
		if len(title) > 50 {
			title = title[:50]
		}
		cfg := server.cfg
		cfg.Workspace = workspace
		cfg.Session, cfg.RanWith = "", nil
		chat = server.panelHomes["home:"+workspace]
		if chat == nil {
			chat = &serverChat{Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
		}
		delete(server.panelHomes, "home:"+workspace)
		chat.ID, chat.Title, chat.Config = core.Identifier()[:16], string(title), cfg
		server.chats = append([]*serverChat{chat}, server.chats...)
	}

	if chat.handoff == nil && !(chat.run != nil && args.Mode == "steer") {
		// Another provider, account or model than the chat's last turn: the
		// client first asks the user how the new one receives the chat.
		if cfg := server.runConfigLocked(chat, nil); agent.HandoffNeeded(cfg, chat.History) {
			plan, previous := server.handoffPlanLocked(chat, cfg, chat.History)
			if args.Handoff == nil || args.Handoff.Target != nil && *args.Handoff.Target != agent.IdentityOf(cfg) {
				core.WriteJSON(w, 409, map[string]any{"error": "Choose how " + plan.To + " receives this conversation", "handoff": plan})
				return
			}
			if problem := plan.Problem(*args.Handoff); problem != "" {
				core.Fail(w, 400, problem)
				return
			}
			chat.handoff = &pendingHandoff{choice: *args.Handoff, plan: plan, previous: previous}
		}
	}

	if chat.run != nil {
		if args.Mode == "steer" {
			chat.steer = append(chat.steer, m)
		} else {
			chat.queue = append(chat.queue, m)
		}
		server.changedLocked(chat, -1)
	} else {
		server.runLocked(chat, m)
	}
	core.WriteJSON(w, 200, map[string]string{"chat": chat.ID})
}

func (server *headlessServer) stop(w http.ResponseWriter, r *http.Request) {
	var args struct{ Chat string }
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if chat := server.chatLocked(args.Chat); chat != nil && chat.run != nil {
		chat.run.cancel()
		chat.queue = nil
		chat.steer = nil
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) answer(w http.ResponseWriter, r *http.Request) {
	var args struct {
		ID      string
		Answers map[string]string
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	if !server.backend.Answer(args.ID, args.Answers) {
		core.Fail(w, 404, "These questions were already answered")
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) approve(w http.ResponseWriter, r *http.Request) {
	var args struct {
		ID    string
		Allow bool
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.backend.Approve(args.ID, args.Allow)
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) setAutoResume(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Policy agent.AutoResume `json:"policy"`
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	if err := args.Policy.Validate(); err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, step := range args.Policy.Steps {
		if step.Action != "switch" {
			continue
		}
		found := false
		for _, choice := range server.choices {
			if choice.Connection.ID == step.ConnectionID && choice.ID == step.Model && choice.Connection.Enabled {
				found = true
				break
			}
		}
		if !found {
			core.Fail(w, 400, "Resume model is unavailable")
			return
		}
	}
	server.autoResume = &args.Policy
	if err := server.saveSettingsLocked(); err != nil {
		core.Fail(w, 500, "Unable to save resume settings")
		return
	}
	server.broadcastLocked("settings", server.settingsLocked())
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) setModelCompaction(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Label string `json:"label"`
		Limit int    `json:"limit"`
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	choice, ok := server.choices[args.Label]
	if !ok {
		core.Fail(w, 400, "Unknown model")
		return
	}
	for i := range server.connections {
		connection := &server.connections[i]
		if connection.ID != choice.Connection.ID {
			continue
		}
		if err := connection.SetCompactLimit(choice.ID, args.Limit); err != nil {
			core.Fail(w, 400, err.Error())
			return
		}
		if err := server.saveSettingsLocked(); err != nil {
			core.Fail(w, 500, "Unable to save context settings")
			return
		}
		server.broadcastLocked("settings", server.settingsLocked())
		core.WriteJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	core.Fail(w, 404, "Connection not found")
}

func (server *headlessServer) setModel(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Chat, Label, Effort string
		Tier                *string
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	target := &server.cfg
	chat := server.chatLocked(args.Chat)
	if args.Chat != "" {
		if chat == nil {
			core.Fail(w, 404, "Chat not found")
			return
		}
		if chat.run != nil {
			core.Fail(w, 409, "Stop this chat before changing its model")
			return
		}
		target = &chat.Config
	}
	next := *target
	if args.Label != "" {
		choice, ok := server.choices[args.Label]
		if !ok {
			core.Fail(w, 400, "Unknown model")
			return
		}
		next = server.configFor(choice, next)
	}
	if args.Effort != "" {
		choice, ok := server.choices[server.labelLocked(next)]
		if !ok || !agent.Contains(choice.Efforts, args.Effort) {
			core.Fail(w, 400, "Unsupported reasoning level")
			return
		}
		next.Effort = args.Effort
	}
	if args.Tier != nil {
		choice, ok := server.choices[server.labelLocked(next)]
		if !ok || *args.Tier != "" && agent.TierFor(choice, *args.Tier) != *args.Tier {
			core.Fail(w, 400, "Unsupported speed")
			return
		}
		next.ServiceTier = *args.Tier
	}
	*target = next
	if args.Chat == "" {
		if err := server.saveSettingsLocked(); err != nil {
			core.Fail(w, 500, "Unable to save model selection")
			return
		}
	}
	if chat != nil {
		server.changedLocked(chat, -1)
	}
	server.broadcastLocked("settings", server.settingsLocked())
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) setApprovalMode(w http.ResponseWriter, r *http.Request) {
	var args struct{ Mode string }
	if core.ReadJSON(r, &args) != nil || !agent.Contains([]string{"approval", "yolo", "auto"}, args.Mode) {
		core.Fail(w, 400, "Unknown approval mode")
		return
	}
	server.mu.Lock()
	var closed []*serverTab
	if args.Mode != server.approvalMode {
		server.terminalRevision++
	}
	if server.bridge != nil && (server.approvalMode == "yolo") != (args.Mode == "yolo") {
		for _, chat := range server.panelContextsLocked() {
			if chat.run != nil {
				chat.run.cancel()
			}
			for i := len(chat.tabs) - 1; i >= 0; i-- {
				if chat.tabs[i].terminal != nil {
					closed = append(closed, server.detachTabLocked(chat, i))
				}
			}
		}
		server.scheduleSaveLocked()
	}
	server.approvalMode = args.Mode
	err := server.saveSettingsLocked()
	if err == nil {
		server.broadcastLocked("settings", server.settingsLocked())
	}
	server.mu.Unlock()
	for _, tab := range closed {
		tab.close()
	}
	if err != nil {
		core.Fail(w, 500, "Unable to save permissions")
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) editPanel(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Action, Kind, Key, URL string
		Root                   bool // the phone's Debian terminal (Settings → Linux)
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	chat := server.panelChatLocked(r.PathValue("id"))
	server.mu.Unlock()
	if chat == nil {
		core.Fail(w, 404, "Chat not found")
		return
	}
	var result agent.PanelToolResult
	var err error
	switch {
	case args.Action == "open" && args.Kind == "browser":
		if server.bridge != nil && !server.bridge.available(r.Context(), "browser") {
			core.Fail(w, 409, "Local browser is unavailable")
			return
		}
		raw, _ := json.Marshal(map[string]string{"action": "open", "tab_id": "", "url": args.URL})
		result, err = server.browserTools(chat)(r.Context(), "browser_tabs", raw)
	case args.Action == "open" && args.Kind == "terminal" && args.Root:
		var key string
		key, err = server.openDebianTerminal(r, chat)
		if err == nil {
			core.WriteJSON(w, 200, map[string]string{"key": key})
			return
		}
	case args.Action == "open" && args.Kind == "terminal":
		raw, _ := json.Marshal(map[string]string{"action": "open", "session_id": "", "cwd": ""})
		result, err = server.terminalTools(chat)(r.Context(), "terminal_tabs", raw)
	case args.Action == "close":
		var closed *serverTab
		server.mu.Lock()
		for index, tab := range chat.tabs {
			if tab.key() == args.Key {
				closed = server.detachTabLocked(chat, index)
				break
			}
		}
		server.scheduleSaveLocked()
		server.mu.Unlock()
		if closed != nil {
			closed.close()
		}
	default:
		core.Fail(w, 400, "Choose browser or terminal")
		return
	}
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]string{"result": result.Text})
}

// projectFiles serves the file browser in a registered project.
func (server *headlessServer) projectFiles(handler func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		workspace, err := server.workspaceLocked(r.URL.Query().Get("workspace"))
		server.mu.Unlock()
		if err != nil {
			core.Fail(w, 403, err.Error())
			return
		}
		handler(w, r, workspace)
	}
}

func (server *headlessServer) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	workspace, err := server.workspaceLocked(r.URL.Query().Get("workspace"))
	server.mu.Unlock()
	if err != nil {
		core.Fail(w, 403, err.Error())
		return
	}
	remoteapi.UploadAttachment(w, r, workspace)
}
