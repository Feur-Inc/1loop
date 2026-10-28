package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

func mobileClaudeSignedIn() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return false
	}
	var credentials struct {
		OAuth struct {
			Access  string `json:"accessToken"`
			Refresh string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &credentials) != nil {
		return false
	}
	return credentials.OAuth.Access != "" || credentials.OAuth.Refresh != ""
}

func (server *headlessServer) claudeLoginState(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.claudeLogin != nil {
		core.WriteJSON(w, 200, server.claudeLogin)
		return
	}
	state := "idle"
	if mobileClaudeSignedIn() {
		state = "connected"
	}
	core.WriteJSON(w, 200, map[string]string{"state": state})
}

func (server *headlessServer) beginClaudeLogin(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Code   string `json:"code"`
		Cancel bool   `json:"cancel"`
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
	ctx, cancel := context.WithTimeout(server.ctx, 5*time.Minute)
	login := &mobileLogin{State: "pending", cancel: cancel}
	process, err := agent.StartClaudeSignIn(ctx, server.dir, "/usr/local/libexec/1loop-open-url", func(link string) {
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
		if err != nil || !mobileClaudeSignedIn() {
			login.State, login.Problem = "failed", "Claude Code sign-in did not complete. Try again."
		}
		server.mu.Unlock()
		if login.State == "connected" {
			server.loadModels()
		}
	}()
	core.WriteJSON(w, 200, login)
}
