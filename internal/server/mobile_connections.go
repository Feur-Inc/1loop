package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

type mobileLogin struct {
	State   string `json:"state"`
	URL     string `json:"url,omitempty"`
	Problem string `json:"problem,omitempty"`
	cancel  context.CancelFunc
	input   io.WriteCloser
}

func (server *headlessServer) connectionLocked(id string) (agent.Connection, bool) {
	for _, connection := range server.connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return agent.Connection{}, false
}

func (server *headlessServer) localConnections(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	rows := []map[string]any{}
	for _, connection := range server.connections {
		row := map[string]any{"connection": connection, "hasKey": server.keys[connection.ID] != "", "problem": server.modelProblems[connection.ID]}
		if login := server.logins[connection.ID]; login != nil {
			row["login"] = login
		}
		if connection.Provider == "openai" {
			if auth, err := server.backend.GetOpenAI(connection.AuthProfile); err == nil {
				auth.Mu.Lock()
				row["signedIn"] = auth.Account.Tokens.Access != ""
				row["email"] = auth.Account.Email
				auth.Mu.Unlock()
			}
		}
		if connection.Provider == "claudecode" {
			row["signedIn"] = mobileClaudeSignedIn()
			if server.claudeLogin != nil {
				row["login"] = server.claudeLogin
			}
		}
		if connection.Provider == "chatgptweb" {
			_, err := os.Stat(server.webKeyFile(connection.ID))
			row["hasKey"] = err == nil
		}
		rows = append(rows, row)
	}
	_, installed := exec.LookPath("claude")
	_, ready := os.Stat(filepath.Join(server.dir, "claude.ready"))
	core.WriteJSON(w, 200, map[string]any{"connections": rows, "claudeInstalled": installed == nil && ready == nil})
}

func (server *headlessServer) saveLocalConnection(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Connection agent.Connection `json:"connection"`
		APIKey     *string          `json:"apiKey"`
		Remove     bool             `json:"remove"`
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid connection")
		return
	}
	connection := args.Connection
	server.mu.Lock()
	defer server.mu.Unlock()
	existing, found := server.connectionLocked(connection.ID)
	if args.Remove {
		if !found {
			core.Fail(w, 404, "Connection not found")
			return
		}
		for _, chat := range server.chats {
			if chat.run != nil && chat.Config.ConnectionID == connection.ID {
				core.Fail(w, 409, "Stop chats using this connection before removing it")
				return
			}
		}
	} else {
		if connection.ID != "" && !found {
			core.Fail(w, 404, "Connection not found")
			return
		}
		if strings.TrimSpace(connection.Name) == "" || !agent.Contains([]string{"openai", "claudecode", "compatible", "openrouter", "vllm", "chatgptweb"}, connection.Provider) {
			core.Fail(w, 400, "Choose a connection name and supported provider")
			return
		}
		if !found {
			connection.ID = core.Identifier()
			connection.AuthProfile = connection.ID
		} else {
			connection.AuthProfile = existing.AuthProfile
		}
		if connection.Provider == "chatgptweb" {
			server.webConnectionLocked(&connection)
		}
	}
	// Save a candidate first, so a storage failure leaves the active settings intact.
	previous := server.connections
	previousKeys := server.keys
	next := make([]agent.Connection, 0, len(previous)+1)
	for _, item := range previous {
		if item.ID != connection.ID {
			next = append(next, item)
		} else if !args.Remove {
			next = append(next, connection)
		}
	}
	if !found && !args.Remove {
		next = append(next, connection)
	}
	keys := make(map[string]string, len(previousKeys)+1)
	for id, key := range previousKeys {
		keys[id] = key
	}
	if args.Remove {
		delete(keys, connection.ID)
	} else if found && connection.Provider != existing.Provider {
		delete(keys, connection.ID)
	}
	var tunnelKey *string // ChatGPT (Web)'s: in its own private file, never in the settings
	if !args.Remove && args.APIKey != nil {
		if connection.Provider == "chatgptweb" {
			tunnelKey = args.APIKey
		} else {
			keys[connection.ID] = *args.APIKey
		}
	}
	server.connections, server.keys = next, keys
	if err := server.saveSettingsLocked(); err != nil {
		server.connections, server.keys = previous, previousKeys
		core.Fail(w, 500, "Unable to save the connection")
		return
	}
	if tunnelKey != nil {
		if err := os.WriteFile(server.webKeyFile(connection.ID), []byte(strings.TrimSpace(*tunnelKey)+"\n"), 0600); err != nil {
			core.Fail(w, 500, "Unable to save the tunnel key")
			return
		}
	}
	if args.Remove && existing.Provider == "chatgptweb" {
		_ = os.Remove(server.webKeyFile(existing.ID))
	}
	if login := server.logins[connection.ID]; login != nil && login.cancel != nil {
		login.cancel()
	}
	delete(server.logins, connection.ID)
	delete(server.limitsCache, connection.ID)
	server.modelRevision++
	server.loading = true
	server.broadcastLocked("settings", server.settingsLocked())
	go server.loadModels()
	core.WriteJSON(w, 200, map[string]any{"connection": connection})
}

func (server *headlessServer) localConnectionModels(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	connection, found := server.connectionLocked(r.PathValue("id"))
	key := server.keys[connection.ID]
	server.mu.Unlock()
	if !found {
		core.Fail(w, 404, "Connection not found")
		return
	}
	server.writeConnectionModels(w, r, connection, key)
}

func (server *headlessServer) draftLocalConnectionModels(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Connection agent.Connection `json:"connection"`
		APIKey     *string          `json:"apiKey"`
	}
	if core.ReadJSON(r, &args) != nil || !agent.Contains([]string{"openai", "claudecode", "compatible", "openrouter", "vllm", "chatgptweb"}, args.Connection.Provider) {
		core.Fail(w, 400, "Choose a supported provider")
		return
	}
	server.mu.Lock()
	existing, found := server.connectionLocked(args.Connection.ID)
	key := ""
	if found {
		args.Connection.AuthProfile = existing.AuthProfile
		if args.Connection.Provider == existing.Provider {
			key = server.keys[existing.ID]
		}
		if args.Connection.Provider == "chatgptweb" {
			server.webConnectionLocked(&args.Connection)
		}
	} else if args.Connection.Provider == "openai" || args.Connection.Provider == "chatgptweb" {
		server.mu.Unlock()
		core.Fail(w, 409, "Save the ChatGPT connection and sign in first")
		return
	} else {
		args.Connection.AuthProfile = ""
	}
	server.mu.Unlock()
	if args.APIKey != nil {
		key = *args.APIKey
	}
	server.writeConnectionModels(w, r, args.Connection, key)
}

func (server *headlessServer) writeConnectionModels(w http.ResponseWriter, r *http.Request, connection agent.Connection, key string) {
	choices, err := server.backend.ConnectionModels(r.Context(), connection, key)
	if err != nil {
		core.Fail(w, 502, err.Error())
		return
	}
	models := []map[string]string{}
	for _, choice := range choices {
		models = append(models, map[string]string{"id": choice.ID, "name": choice.Name})
	}
	core.WriteJSON(w, 200, map[string]any{"models": models})
}

func (server *headlessServer) localConnectionLogin(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	connection, found := server.connectionLocked(r.PathValue("id"))
	if !found || connection.Provider != "openai" {
		core.Fail(w, 400, "Choose a ChatGPT connection")
		return
	}
	if current := server.logins[connection.ID]; current != nil && current.State == "pending" {
		core.WriteJSON(w, 200, current)
		return
	}
	ctx, cancel := context.WithTimeout(server.ctx, 5*time.Minute)
	link, done, err := server.backend.BeginOpenAI(ctx, connection.AuthProfile)
	if err != nil {
		cancel()
		core.Fail(w, 400, err.Error())
		return
	}
	login := &mobileLogin{State: "pending", URL: link, cancel: cancel}
	if server.logins == nil {
		server.logins = map[string]*mobileLogin{}
	}
	server.logins[connection.ID] = login
	go func() {
		defer cancel()
		err := <-done
		server.mu.Lock()
		if server.logins[connection.ID] != login {
			server.mu.Unlock()
			return
		}
		login.State, login.URL = "connected", ""
		if err != nil {
			login.State, login.Problem = "failed", err.Error()
		}
		server.mu.Unlock()
		if err == nil {
			server.loadModels()
		}
	}()
	core.WriteJSON(w, 200, login)
}

func (server *headlessServer) localConnectionDisconnect(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	connection, found := server.connectionLocked(r.PathValue("id"))
	if login := server.logins[connection.ID]; login != nil && login.cancel != nil {
		login.cancel()
	}
	delete(server.logins, connection.ID)
	delete(server.limitsCache, connection.ID)
	server.mu.Unlock()
	if !found || connection.Provider != "openai" {
		core.Fail(w, 400, "Choose a ChatGPT connection")
		return
	}
	warning, err := server.backend.DisconnectOpenAI(r.Context(), connection.AuthProfile)
	if err != nil {
		core.Fail(w, 500, err.Error())
		return
	}
	go server.loadModels()
	core.WriteJSON(w, 200, map[string]string{"warning": warning})
}

func (server *headlessServer) refreshLocalModels(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	server.loading = true
	server.broadcastLocked("settings", server.settingsLocked())
	server.mu.Unlock()
	go server.loadModels()
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) limits(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	connections := append([]agent.Connection(nil), server.connections...)
	keys := make(map[string]string, len(server.keys))
	cached := make(map[string]agent.AccountLimits, len(server.limitsCache))
	for id, key := range server.keys {
		keys[id] = key
	}
	for id, value := range server.limitsCache {
		cached[id] = value
	}
	server.mu.Unlock()
	results := make([]agent.AccountLimits, len(connections))
	var wait sync.WaitGroup
	for index, connection := range connections {
		if !connection.Enabled {
			continue
		}
		if value, ok := cached[connection.ID]; ok && time.Since(value.Checked) < time.Minute {
			results[index] = value
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := server.backend.ConnectionLimits(r.Context(), connection, keys[connection.ID])
			if err != nil {
				value = agent.AccountLimits{Note: "Limits unavailable"}
			}
			value.Checked = time.Now()
			results[index] = value
		}()
	}
	wait.Wait()
	out := []remoteapi.Limits{}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.limitsCache == nil {
		server.limitsCache = map[string]agent.AccountLimits{}
	}
	for index, connection := range connections {
		if !connection.Enabled {
			continue
		}
		server.limitsCache[connection.ID] = results[index]
		value := remoteapi.Limits{Account: connection.ID, Note: results[index].Note, Windows: []remoteapi.LimitWindow{}}
		for _, window := range results[index].Windows {
			reset := int64(0)
			if !window.ResetAt.IsZero() {
				reset = window.ResetAt.UnixMilli()
			}
			value.Windows = append(value.Windows, remoteapi.LimitWindow{Label: window.Label, Left: window.Remaining(), Reset: reset})
		}
		out = append(out, value)
	}
	core.WriteJSON(w, 200, map[string]any{"accounts": out})
}
