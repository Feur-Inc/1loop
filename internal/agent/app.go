// SPDX-License-Identifier: MPL-2.0

// Package agent is 1loop's agent: the model connections, the conversation
// loop, its tools, approvals and context management.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"1loop/internal/browser"
	"1loop/internal/core"
)

type Config struct {
	DefaultPreset    bool `json:"defaultPreset,omitempty"`
	presetSelection  *Config
	selectionError   error
	ConnectionID     string                                            `json:"connectionId,omitempty"`
	AccountID        string                                            `json:"-"`
	Residency        string                                            `json:"-"`
	AuthProfile      string                                            `json:"authProfile,omitempty"`
	Effort           string                                            `json:"effort,omitempty"`
	ServiceTier      string                                            `json:"serviceTier,omitempty"` // a ChatGPT speed (see TierFor); "" for the plan's default
	ReasoningMode    string                                            `json:"reasoningMode,omitempty"`
	AutoResume       *AutoResume                                       `json:"-"` // the user's list, for every chat (Settings)
	ResumeModel      func(context.Context, ResumeStep) (Config, error) `json:"-"`
	ResumeSwitched   func(context.Context, Config) error               `json:"-"`
	Routing          map[string]any                                    `json:"routing,omitempty"`
	Parameters       map[string]any                                    `json:"parameters,omitempty"`
	UserInstructions string                                            `json:"-"`
	ApprovalMode     string                                            `json:"-"`
	// Preferences changes the saved user preferences under their owner's
	// lock: update receives the saved text and returns the new one. Nil
	// where the memory tool cannot change them.
	Preferences func(ctx context.Context, update func(string) (string, error)) error `json:"-"`
	// WrapUpOff: the next-step card is turned off in Settings, so the
	// wrap_up tool is not offered.
	WrapUpOff bool `json:"-"`
	// Settings reads and changes 1loop's own settings for the settings
	// tool. Nil where the tool is not offered.
	Settings *SettingsAccess `json:"-"`
	// Subchats starts and follows the chat's subchats (subchats.go), or
	// its parent in a subchat. Nil where the tool is not offered.
	Subchats *SubchatAccess `json:"-"`
	// Chats lists the other chats of this 1loop and sends them messages
	// (chats.go). Nil where the tool is not offered.
	Chats *ChatAccess `json:"-"`
	// ClassicCompaction turns the staged context policy off: one compaction
	// when the window is nearly full, tool outputs kept as they come.
	ClassicCompaction bool                                      `json:"-"`
	CompactAt         int                                       `json:"-"`
	Cache             *ContextCache                             `json:"-"`
	ContextKey        string                                    `json:"-"`
	Todo              *TodoState                                `json:"-"`
	Tasks             *BackgroundTasks                          `json:"-"` // shell commands run in the background
	Grants            *PermissionGrants                         `json:"-"`
	Review            *ReviewSession                            `json:"-"` // the Auto mode reviewer's conversation for the chat
	toolSet           []map[string]any                          // replaces the agent's tools (the reviewer's)
	Computer          PanelToolHandler                          `json:"-"`
	Terminal          PanelToolHandler                          `json:"-"`
	Browser           PanelToolHandler                          `json:"-"`
	Phone             PanelToolHandler                          `json:"-"`
	Packages          PanelToolHandler                          `json:"-"`
	ProjectTools      PanelToolHandler                          `json:"-"` // phone Claude Code uses 1loop's project-scoped tools
	Recall            func(context.Context) ([]ChatItem, error) `json:"-"`
	Provider          string                                    `json:"provider"`
	BaseURL           string                                    `json:"baseURL"`
	APIKey            string                                    `json:"-"`
	Model             string                                    `json:"model"`
	Workspace         string                                    `json:"workspace"`
	// Scratch is the folder 1loop made for a chat started without one
	// (scratch.go); the chat is in it while Workspace is the same.
	Scratch        string         `json:"scratch,omitempty"`
	Move           *WorkspaceMove `json:"-"`                 // where the chat asked to move, for this run
	RecentProjects []string       `json:"-"`                 // the user's project folders, for a scratch chat's prompt
	MachineID      string         `json:"machine,omitempty"` // the SSH machine the workspace is on, "" for this computer
	Machine        Machine        `json:"-"`                 // that machine, for a run (machine.go)
	// Env is the project's environment variables (#408), for the commands
	// the run starts. Never saved with the chat nor sent to the model.
	Env     map[string]string `json:"-"`
	Session string            `json:"session,omitempty"` // Claude Code connection: the chat's session there (see ClaudeSession)
	// ClaudeHome is the Claude Code connection's CLAUDE_CONFIG_DIR, "" for
	// the system Claude Code login (claude_accounts.go).
	ClaudeHome string `json:"claudeHome,omitempty"`
	// SessionMark is how many of the chat's items that session has seen:
	// what other models did after it is sent when Claude Code resumes.
	SessionMark int `json:"sessionMark,omitempty"`
	// OtherSessions keeps the sessions of the chat's other Claude Code
	// logins, so coming back to a login resumes its session (claude_accounts.go).
	OtherSessions map[string]LoginSession `json:"otherSessions,omitempty"`
	// RanWith is who the chat's last turn ran with: another provider,
	// account or model first goes through a handoff (handoff.go).
	RanWith *RunIdentity `json:"ranWith,omitempty"`
	// HandoffSummary is read by Claude Code resuming its session instead of
	// the whole gap, when the user had it summarized.
	HandoffSummary string `json:"-"`
	HandoffApplied bool   `json:"-"`
	// ToolAllowed is checked again at execution so revoking a capability
	// also blocks calls made from a model's earlier tool declarations.
	ToolAllowed         func(string) bool `json:"-"`
	CapabilitiesChanged <-chan struct{}   `json:"-"`
	WebConversation     *WebConversation  `json:"webConversation,omitempty"`
	// Usage keeps the run's model calls in the usage ledger; nil keeps none.
	Usage       *UsageTag    `json:"-"`
	WebSettings *WebSettings `json:"webSettings,omitempty"`
}

type emitter func(string, any)

type App struct {
	mu              sync.Mutex
	Approvals       map[string]chan bool
	openAI          *openAIAuth
	openAIProfiles  map[string]*openAIAuth
	capabilityCache map[string]ModelChoice
	claudeInfo      map[string]ClaudeInfo             // Claude Code: commands and account, by connection and folder
	answers         map[string]chan map[string]string // request_user_input, by request
	webContext      context.Context
	webAccounts     map[string]*ChatGPTWeb
	webSlots        chan struct{}
	webDir          string                                  // the phone's: beside the server's state
	webNative       func(profile string) browser.NativeHost // the phone's WebViews
}

func ResolveWorkspace(path string) (string, error) {
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

func (backend *App) models(ctx context.Context, cfg Config) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if cfg.Provider != "openai" {
		return apiRequest(ctx, cfg, "GET", "/models", nil)
	}
	cfg, err := backend.openAIConfig(ctx, cfg)
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
	auth, err := backend.GetOpenAI(cfg.AuthProfile)
	if err != nil {
		return nil, err
	}
	auth.Mu.Lock()
	email := auth.Account.Email
	auth.Mu.Unlock()
	return map[string]any{"data": data, "account": map[string]any{"email": email}}, nil
}

func (backend *App) confirm(ctx context.Context, emit emitter, title, preview string) bool {
	if ctx.Err() != nil {
		return false
	}
	policy, _ := ctx.Value(approvalContextKey{}).(approvalPolicy)
	if policy.yolo {
		return true
	}
	if policy.review != nil {
		allow, reason := policy.review(ctx, emit, title, preview)
		if allow {
			if reason != "" { // the reviewer's approval: kept in the chat
				emit("note", "Auto-approved · "+actionSummary(title, preview)+" · "+reason)
			}
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		if reason != "" {
			preview = "Auto-review asks you: " + reason + "\n\n" + preview
		}
	}
	return backend.askApproval(ctx, emit, title, preview)
}

// askApproval asks the user, whatever the approval mode.
func (backend *App) askApproval(ctx context.Context, emit emitter, title, preview string) bool {
	if ctx.Err() != nil {
		return false
	}
	id := core.Identifier()
	ch := make(chan bool, 1)
	backend.mu.Lock()
	backend.Approvals[id] = ch
	backend.mu.Unlock()
	defer func() { backend.mu.Lock(); delete(backend.Approvals, id); backend.mu.Unlock() }()
	emit("approval", map[string]any{"id": id, "title": title, "preview": preview})
	select {
	case allowed := <-ch:
		return allowed
	case <-ctx.Done():
		return false
	}
}

func (backend *App) Approve(id string, allow bool) {
	backend.mu.Lock()
	ch := backend.Approvals[id]
	backend.mu.Unlock()
	if ch != nil {
		select {
		case ch <- allow:
		default:
		}
	}
}
