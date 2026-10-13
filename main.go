package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type config struct {
	ConnectionID     string         `json:"connectionId,omitempty"`
	AccountID        string         `json:"-"`
	Residency        string         `json:"-"`
	AuthProfile      string         `json:"authProfile,omitempty"`
	Effort           string         `json:"effort,omitempty"`
	ReasoningMode    string         `json:"reasoningMode,omitempty"`
	Routing          map[string]any `json:"routing,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	UserInstructions string         `json:"-"`
	ApprovalMode     string         `json:"-"`
	// ClassicCompaction turns the staged context policy off: one compaction
	// when the window is nearly full, tool outputs kept as they come.
	ClassicCompaction bool                                      `json:"-"`
	Cache             *contextCache                             `json:"-"`
	ContextKey        string                                    `json:"-"`
	Todo              *todoState                                `json:"-"`
	Terminal          panelToolHandler                          `json:"-"`
	Browser           panelToolHandler                          `json:"-"`
	Recall            func(context.Context) ([]chatItem, error) `json:"-"`
	Provider          string                                    `json:"provider"`
	BaseURL           string                                    `json:"baseURL"`
	APIKey            string                                    `json:"-"`
	Model             string                                    `json:"model"`
	Workspace         string                                    `json:"workspace"`
	Session           string                                    `json:"session,omitempty"` // Claude Code connection: the chat's session there
}

type emitter func(string, any)

type app struct {
	mu              sync.Mutex
	approvals       map[string]chan bool
	openAI          *openAIAuth
	openAIProfiles  map[string]*openAIAuth
	capabilityCache map[string]modelChoice
}

func identifier() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func main() {
	workspace := flag.String("workspace", ".", "Workspace")
	developer := flag.Bool("dev", false, "Write a detailed developer log (events, performance, stalls) to the terminal and to ~/.cache/1loop/dev.log")
	flag.Parse()
	devWanted = *developer || os.Getenv("ONELOOP_DEV") == "1"
	root, err := resolveWorkspace(*workspace)
	if err != nil {
		log.Fatal(err)
	}
	backend := &app{approvals: make(map[string]chan bool)}
	runDesktop(backend, root)
}

func resolveWorkspace(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("Choose a workspace")
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("The workspace must be a directory")
	}
	return root, nil
}

func (a *app) models(ctx context.Context, cfg config) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if cfg.Provider != "openai" {
		return apiRequest(ctx, cfg, "GET", "/models", nil)
	}
	cfg, err := a.openAIConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	result, err := apiRequest(ctx, cfg, "GET", "/models?client_version="+openAIClientVersion, nil)
	if err != nil {
		return nil, err
	}
	rows, _ := result["models"].([]any)
	data := make([]any, 0, len(rows))
	for _, row := range rows {
		model, ok := row.(map[string]any)
		if ok && model["visibility"] == "list" {
			model["id"] = model["slug"]
			model["displayName"] = model["display_name"]
			data = append(data, model)
		}
	}
	auth, err := a.getOpenAI(cfg.AuthProfile)
	if err != nil {
		return nil, err
	}
	auth.mu.Lock()
	email := auth.Account.Email
	auth.mu.Unlock()
	return map[string]any{"data": data, "account": map[string]any{"email": email}}, nil
}

func (a *app) confirm(ctx context.Context, emit emitter, title, preview string) bool {
	if ctx.Err() != nil {
		return false
	}
	if automatic, _ := ctx.Value(approvalContextKey{}).(bool); automatic {
		return true
	}
	id := identifier()
	ch := make(chan bool, 1)
	a.mu.Lock()
	a.approvals[id] = ch
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.approvals, id); a.mu.Unlock() }()
	emit("approval", map[string]any{"id": id, "title": title, "preview": preview})
	select {
	case allowed := <-ch:
		return allowed
	case <-ctx.Done():
		return false
	}
}

func (a *app) approve(id string, allow bool) {
	a.mu.Lock()
	ch := a.approvals[id]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- allow:
		default:
		}
	}
}
