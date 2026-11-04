// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// claudeLoginConnectionLocked is the Claude Code connection a sign-in is
// for: the saved one named, or for a connection not saved yet the system
// login, when no other connection uses it (claude_accounts.go).
func (server *headlessServer) claudeLoginConnectionLocked(id string) (agent.Connection, error) {
	if id == "" {
		if agent.NewClaudeLogin(server.connections, "") == agent.ClaudeOwnLogin {
			return agent.Connection{}, fmt.Errorf("Save this connection first: it gets its own Claude login")
		}
		return agent.Connection{Provider: "claudecode"}, nil
	}
	connection, found := server.connectionLocked(id)
	if !found || connection.Provider != "claudecode" {
		return agent.Connection{}, fmt.Errorf("Connection not found")
	}
	return connection, nil
}

func (server *headlessServer) claudeLoginState(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	id := r.URL.Query().Get("connection")
	if server.claudeLogin != nil && server.claudeLogin.connection == id {
		core.WriteJSON(w, 200, server.claudeLogin)
		return
	}
	state := "idle"
	if connection, err := server.claudeLoginConnectionLocked(id); err == nil && agent.ClaudeSignedIn(connection) {
		state = "connected"
	}
	core.WriteJSON(w, 200, map[string]string{"state": state})
}

func (server *headlessServer) beginClaudeLogin(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Connection string `json:"connection"`
		Code       string `json:"code"`
		Cancel     bool   `json:"cancel"`
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid sign-in request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	current := server.claudeLogin
	if args.Cancel {
		if current != nil && current.cancel != nil {
			current.cancel()
		}
		server.claudeLogin = nil
		core.WriteJSON(w, 200, map[string]string{"state": "idle"})
		return
	}
	if current != nil && current.State == "pending" && current.connection != args.Connection {
		core.Fail(w, 409, "Another Claude sign-in is in progress")
		return
	}
	if current != nil && current.State == "pending" {
		if args.Code != "" {
			if len(args.Code) > 4096 || strings.ContainsAny(args.Code, "\r\n") {
				core.Fail(w, 400, "Invalid sign-in code")
				return
			}
			if _, err := current.input.Write([]byte(args.Code + "\n")); err != nil {
				core.Fail(w, 400, "The sign-in process is no longer waiting")
				return
			}
		}
		core.WriteJSON(w, 200, current)
		return
	}
	if args.Code != "" {
		core.Fail(w, 409, "Start sign-in before submitting its code")
		return
	}
	if _, err := agent.ClaudeExecutable(); err != nil {
		core.Fail(w, 409, "Install Claude Code first")
		return
	}
	connection, err := server.claudeLoginConnectionLocked(args.Connection)
	if err != nil {
		core.Fail(w, 409, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(server.ctx, 5*time.Minute)
	login := &mobileLogin{State: "pending", cancel: cancel, connection: args.Connection}
	process, err := agent.StartClaudeSignIn(ctx, connection, server.dir, "/usr/local/libexec/1loop-open-url", func(link string) {
		server.mu.Lock()
		if server.claudeLogin == login {
			login.URL = link
		}
		server.mu.Unlock()
	})
	if err != nil {
		cancel()
		core.Fail(w, 500, err.Error())
		return
	}
	login.input = process.Input
	server.claudeLogin = login
	go func() {
		defer cancel()
		err := <-process.Done
		server.mu.Lock()
		if server.claudeLogin != login {
			server.mu.Unlock()
			return
		}
		login.URL, login.State = "", "connected"
		if err != nil || !agent.ClaudeSignedIn(connection) {
			login.State, login.Problem = "failed", "Claude Code sign-in did not complete. Try again."
		}
		server.mu.Unlock()
		if login.State == "connected" {
			server.loadModels()
		}
	}()
	core.WriteJSON(w, 200, login)
}
