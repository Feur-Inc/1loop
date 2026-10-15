//go:build server

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (server *headlessServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"name": server.name, "protocol": 1, "headless": true, "secure": true, "consolePairing": true})
	})
	mux.HandleFunc("POST /api/pair", server.pair)
	mux.HandleFunc("POST /api/pair/confirm", server.confirmPair)
	endpoints := map[string]http.HandlerFunc{"GET /api/state": server.state, "GET /api/chats/{id}": server.chat, "GET /api/events": server.events, "POST /api/send": server.send, "POST /api/stop": server.stop, "POST /api/approve": server.approve, "POST /api/model": server.setModel, "POST /api/approval-mode": server.setApprovalMode, "GET /api/chats/{id}/panel": server.panelTabs, "GET /api/chats/{id}/panel/{key}": server.panelTab, "GET /api/chats/{id}/panel/{key}/screen": server.panelScreen, "GET /api/chats/{id}/panel/{key}/stream": server.panelStream, "POST /api/chats/{id}/panel/{key}/input": server.panelInput, "POST /api/chats/{id}/panel": server.editPanel}
	for path, handler := range endpoints {
		mux.HandleFunc(path, server.authorized(handler))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func (server *headlessServer) state(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	writeJSON(w, 200, map[string]any{"name": server.name, "projects": []string{server.root}, "workspace": server.root, "chats": server.summariesLocked(), "settings": server.settingsLocked()})
}
func (server *headlessServer) chat(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.chatLocked(r.PathValue("id"))
	if chat == nil {
		fail(w, 404, "Chat not found")
		return
	}
	items := make([]remoteItem, len(chat.Items))
	for i := range items {
		items[i] = server.itemLocked(chat, i)
	}
	writeJSON(w, 200, map[string]any{"chat": remoteChat{ID: chat.ID, Title: chat.Title, Workspace: server.root, Running: chat.run != nil, Items: len(items)}, "items": items, "meta": server.metaLocked(chat)})
}
func (server *headlessServer) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "Streaming unsupported")
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
		Images                      []ImagePart
	}
	if readJSON(r, &args) != nil || strings.TrimSpace(args.Text) == "" {
		fail(w, 400, "Nothing to send")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if args.Workspace != "" && args.Workspace != server.root {
		fail(w, 403, "Choose the workspace configured on this server")
		return
	}
	chat := server.chatLocked(args.Chat)
	if args.Chat != "" && chat == nil {
		fail(w, 404, "Chat not found")
		return
	}
	if chat == nil {
		if server.cfg.Model == "" {
			fail(w, 409, "Configure a provider and model on the server first")
			return
		}
		title := []rune(strings.Join(strings.Fields(args.Text), " "))
		if len(title) > 50 {
			title = title[:50]
		}
		cfg := server.cfg
		cfg.Session = ""
		chat = &serverChat{ID: identifier()[:16], Title: string(title), Config: cfg, Todo: &todoState{}, Cache: &contextCache{}}
		server.chats = append([]*serverChat{chat}, server.chats...)
	}
	m := message{Role: "user", Content: args.Text, Images: args.Images}
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
	writeJSON(w, 200, map[string]string{"chat": chat.ID})
}
func (server *headlessServer) stop(w http.ResponseWriter, r *http.Request) {
	var args struct{ Chat string }
	if readJSON(r, &args) != nil {
		fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if chat := server.chatLocked(args.Chat); chat != nil && chat.run != nil {
		chat.run.cancel()
		chat.queue = nil
		chat.steer = nil
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (server *headlessServer) approve(w http.ResponseWriter, r *http.Request) {
	var args struct {
		ID    string
		Allow bool
	}
	if readJSON(r, &args) != nil {
		fail(w, 400, "Invalid request")
		return
	}
	server.backend.approve(args.ID, args.Allow)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (server *headlessServer) setModel(w http.ResponseWriter, r *http.Request) {
	var args struct{ Chat, Label, Effort string }
	if readJSON(r, &args) != nil {
		fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	target := &server.cfg
	chat := server.chatLocked(args.Chat)
	if args.Chat != "" {
		if chat == nil {
			fail(w, 404, "Chat not found")
			return
		}
		if chat.run != nil {
			fail(w, 409, "Stop this chat before changing its model")
			return
		}
		target = &chat.Config
	}
	next := *target
	if args.Label != "" {
		choice, ok := server.choices[args.Label]
		if !ok {
			fail(w, 400, "Unknown model")
			return
		}
		next = server.configFor(choice, next)
	}
	if args.Effort != "" {
		choice, ok := server.choices[server.labelLocked(next)]
		if !ok || !contains(choice.Efforts, args.Effort) {
			fail(w, 400, "Unsupported reasoning level")
			return
		}
		next.Effort = args.Effort
	}
	*target = next
	if args.Chat == "" {
		if err := server.saveSettingsLocked(); err != nil {
			fail(w, 500, "Unable to save model selection")
			return
		}
	}
	if chat != nil {
		server.changedLocked(chat, -1)
	}
	server.broadcastLocked("settings", server.settingsLocked())
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (server *headlessServer) setApprovalMode(w http.ResponseWriter, r *http.Request) {
	var args struct{ Mode string }
	if readJSON(r, &args) != nil || !contains([]string{"approval", "yolo", "auto"}, args.Mode) {
		fail(w, 400, "Unknown approval mode")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	server.approvalMode = args.Mode
	if err := server.saveSettingsLocked(); err != nil {
		fail(w, 500, "Unable to save permissions")
		return
	}
	server.broadcastLocked("settings", server.settingsLocked())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) editPanel(w http.ResponseWriter, r *http.Request) {
	var args struct{ Action, Kind, Key, URL string }
	if readJSON(r, &args) != nil {
		fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	chat := server.chatLocked(r.PathValue("id"))
	server.mu.Unlock()
	if chat == nil {
		fail(w, 404, "Chat not found")
		return
	}
	var result panelToolResult
	var err error
	switch {
	case args.Action == "open" && args.Kind == "browser":
		raw, _ := json.Marshal(map[string]string{"action": "open", "tab_id": "", "url": args.URL})
		result, err = server.browserTools(chat)(r.Context(), "browser_tabs", raw)
	case args.Action == "open" && args.Kind == "terminal":
		raw, _ := json.Marshal(map[string]string{"action": "open", "session_id": "", "cwd": ""})
		result, err = server.terminalTools(chat)(r.Context(), "terminal_tabs", raw)
	case args.Action == "close":
		server.mu.Lock()
		for index, tab := range chat.tabs {
			if tab.key() == args.Key {
				tab.close()
				chat.tabs = append(chat.tabs[:index], chat.tabs[index+1:]...)
				break
			}
		}
		server.scheduleSaveLocked()
		server.mu.Unlock()
	default:
		fail(w, 400, "Choose browser or terminal")
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"result": result.Text})
}
