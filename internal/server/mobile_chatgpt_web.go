package server

// ChatGPT (Web) in the phone's local agent: the account's tunnel key, its
// tunnel during setup, and the chat's ChatGPT page the panel shows.

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// webKeyFile holds a connection's tunnel key, private, beside the server's
// state: the settings name it, never hold it.
func (server *headlessServer) webKeyFile(id string) string {
	return filepath.Join(server.dir, "chatgpt-web-"+filepath.Base(id)+".key")
}

// webConnectionLocked keeps the phone's own key file, whatever path a
// client sends.
func (server *headlessServer) webConnectionLocked(connection *agent.Connection) {
	connection.BaseURL = ""
	settings := agent.WebSettings{}
	if connection.WebSettings != nil {
		settings = *connection.WebSettings
	}
	settings.TunnelID = strings.TrimSpace(settings.TunnelID)
	settings.KeyFile = server.webKeyFile(connection.ID)
	connection.WebSettings = &settings
}

// localWebTunnel reports the tunnel, and starts it (POST) for setup: the
// plugin is added in ChatGPT while it runs. It stops ten minutes later
// unless a chat uses it.
func (server *headlessServer) localWebTunnel(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	connection, found := server.connectionLocked(r.PathValue("id"))
	server.mu.Unlock()
	if !found || connection.Provider != "chatgptweb" {
		core.Fail(w, 404, "Choose a saved ChatGPT (Web) connection")
		return
	}
	bridge, err := server.backend.WebAccount(connection.Config(""))
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	if r.Method == http.MethodPost {
		release := bridge.Retain()
		if err := bridge.StartTunnel(*connection.WebSettings); err != nil {
			release()
			core.Fail(w, 400, err.Error())
			return
		}
		time.AfterFunc(10*time.Minute, release)
	}
	core.WriteJSON(w, 200, map[string]string{"tunnel": bridge.TunnelStatus()})
}

// webLease keeps one chat's ChatGPT page open while the phone shows the
// chat, as the desktop's panel does: the app renews it every few seconds,
// and an app that stops renewing (another chat, closed, killed) lets the
// page close. It is a view: after a while without a turn the page still
// closes (agent.webIdle), until the chat is opened again or a turn starts.
type webLease struct {
	key     string
	bridge  *agent.ChatGPTWeb
	release func()
	until   time.Time
}

const webLeaseTime = 30 * time.Second

func (server *headlessServer) localWebPage(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Open bool `json:"open"` // the chat was just opened: a paused page opens again
	}
	_ = core.ReadJSON(r, &args)
	server.mu.Lock()
	chat := server.chatLocked(r.PathValue("id"))
	if chat == nil || chat.Config.Provider != "chatgptweb" {
		server.mu.Unlock()
		core.Fail(w, 404, "This chat does not use ChatGPT (Web)")
		return
	}
	cfg := chat.Config
	if connection, ok := server.connectionLocked(cfg.ConnectionID); ok {
		cfg.WebSettings = connection.WebSettings
	}
	key := chat.ID + chat.Version // the run's page (Config.ContextKey)
	server.mu.Unlock()
	bridge, err := server.backend.WebAccount(cfg)
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	address := "https://chatgpt.com/"
	if cfg.WebSettings != nil && cfg.WebSettings.ProjectURL != "" {
		address = cfg.WebSettings.ProjectURL
	}
	if cfg.WebConversation != nil && cfg.WebConversation.URL != "" {
		address = cfg.WebConversation.URL
	}
	server.mu.Lock()
	if server.webLeases == nil {
		server.webLeases = map[string]*webLease{}
	}
	lease, replaced := server.webLeases[chat.ID], (*webLease)(nil)
	if lease != nil && (lease.key != key || lease.bridge != bridge) {
		replaced, lease = lease, nil
	}
	if lease == nil {
		lease = &webLease{key: key, bridge: bridge, release: bridge.RetainView(key)}
		server.webLeases[chat.ID] = lease
		var expire func()
		expire = func() {
			server.mu.Lock()
			if server.webLeases[chat.ID] != lease {
				server.mu.Unlock()
				return
			}
			if left := time.Until(lease.until); left > 0 { // renewed meanwhile
				server.mu.Unlock()
				time.AfterFunc(left, expire)
				return
			}
			delete(server.webLeases, chat.ID)
			server.mu.Unlock()
			lease.release()
		}
		time.AfterFunc(webLeaseTime, expire)
	}
	lease.until = time.Now().Add(webLeaseTime)
	server.mu.Unlock()
	if replaced != nil {
		replaced.release() // after the new one holds the account: its browser keeps running
	}
	if bridge.Paused() && !args.Open {
		core.WriteJSON(w, 200, map[string]any{"paused": true})
		return
	}
	page := bridge.Page(key, address)
	core.WriteJSON(w, 200, map[string]any{"page": page.Snapshot(), "profile": "chatgpt-" + cfg.AuthProfile})
}
