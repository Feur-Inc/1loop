package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"1loop/internal/core"
	"1loop/internal/platform"
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
	path, err := exec.LookPath("claude")
	if err != nil {
		core.Fail(w, 409, "Install Claude Code first")
		return
	}
	ctx, cancel := context.WithTimeout(server.ctx, 5*time.Minute)
	command := exec.CommandContext(ctx, path, "auth", "login", "--claudeai")
	command.Dir = server.dir
	command.Env = append(os.Environ(), "BROWSER=/usr/local/libexec/1loop-open-url")
	platform.PrepareProcess(command)
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		core.Fail(w, 500, "Unable to start sign-in")
		return
	}
	output, err := command.StdoutPipe()
	if err != nil {
		cancel()
		core.Fail(w, 500, "Unable to start sign-in")
		return
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		cancel()
		core.Fail(w, 500, err.Error())
		return
	}
	login := &mobileLogin{State: "pending", cancel: cancel, input: input}
	server.claudeLogin = login
	go func() {
		defer cancel()
		defer input.Close()
		links := regexp.MustCompile(`https://[^\s\x1b]+`)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			for _, candidate := range links.FindAllString(scanner.Text(), -1) {
				parsed, err := url.Parse(candidate)
				if err != nil || !claudeAuthorizationURL(parsed) {
					continue
				}
				server.mu.Lock()
				if server.claudeLogin == login {
					login.URL = candidate
				}
				server.mu.Unlock()
			}
		}
		err := command.Wait()
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

func claudeAuthorizationURL(link *url.URL) bool {
	if link.Scheme != "https" || link.User != nil || link.Port() != "" {
		return false
	}
	if link.Hostname() == "claude.com" {
		return link.Path == "/cai/oauth/authorize"
	}
	return link.Path == "/oauth/authorize" && (link.Hostname() == "claude.ai" || link.Hostname() == "platform.claude.com" || link.Hostname() == "console.anthropic.com")
}
