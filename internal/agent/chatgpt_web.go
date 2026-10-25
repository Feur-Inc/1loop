package agent

import (
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"os"
	"path/filepath"
	"strings"

	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/mcptunnel"
)

// WebSettings contains no credentials. The tunnel key stays in the file
// chosen by the user, outside the saved conversations and the repository.
type WebSettings struct {
	TunnelID   string `json:"tunnelId,omitempty"`
	KeyFile    string `json:"keyFile,omitempty"`
	ProjectURL string `json:"projectURL,omitempty"`
}

func (backend *App) EnableChatGPTWeb(ctx context.Context) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.webContext = ctx
	backend.webAccounts = make(map[string]*ChatGPTWeb)
	backend.webSlots = make(chan struct{}, 3)
}

// WebAccount is desktop-only. Each connection owns its browser process and
// profile; Browser Use is never given a reference to this engine.
func (backend *App) WebAccount(cfg Config) (*ChatGPTWeb, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.webContext == nil {
		return nil, fmt.Errorf("ChatGPT (Web) is available on the local desktop only")
	}
	profile := cfg.AuthProfile
	if profile == "" {
		return nil, fmt.Errorf("This ChatGPT (Web) connection has no browser profile")
	}
	if account := backend.webAccounts[profile]; account != nil {
		return account, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	// The first connection can reuse the profile used during the initial setup.
	name := "chatgpt-browser"
	if profile != "default" {
		name += "-" + profile
	}
	if filepath.Base(profile) != profile || strings.ContainsAny(profile, `/\`) {
		return nil, fmt.Errorf("Invalid ChatGPT browser profile")
	}
	account := NewChatGPTWeb(backend)
	account.parent = backend.webContext
	account.pages = make(map[string]*browser.Page)
	account.engine = browser.NewBrowserEngine(account.parent)
	account.engine.Profile = filepath.Join(dir, "1loop", name)
	backend.webAccounts[profile] = account
	return account, nil
}

func (bridge *ChatGPTWeb) Page(key, address string) *browser.Page {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if tab := bridge.pages[key]; tab != nil && tab.Life.Err() == nil {
		return tab
	}
	if address == "" {
		address = "https://chatgpt.com/"
	}
	tab := browser.NewBrowserPage(browser.NewBrowserSession(bridge.engine), browser.Info{URL: address, Width: 1100, Height: 800, Adaptive: key == "setup"})
	bridge.pages[key] = tab
	go tab.Start()
	return tab
}

func (bridge *ChatGPTWeb) StartTunnel(settings WebSettings) error {
	if !strings.HasPrefix(settings.TunnelID, "tunnel_") {
		return fmt.Errorf("Enter the tunnel ID from OpenAI Platform")
	}
	info, err := os.Stat(settings.KeyFile)
	if err != nil {
		return fmt.Errorf("Read tunnel key file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("The tunnel key must be a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("The tunnel key file must be private (chmod 600)")
	}
	key, err := os.ReadFile(settings.KeyFile)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(key))) == 0 {
		return fmt.Errorf("The tunnel key file is empty")
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.stopTunnel != nil && bridge.tunnelSettings == settings {
		return nil
	}
	if bridge.stopTunnel != nil {
		bridge.stopTunnel()
	}
	ctx, cancel := context.WithCancel(bridge.parent)
	bridge.stopTunnel, bridge.tunnelSettings = cancel, settings
	bridge.tunnelError, bridge.tunnelReady = nil, false
	tunnel := &mcptunnel.Tunnel{ID: settings.TunnelID, Key: strings.TrimSpace(string(key)), Handle: bridge.HandleMCP, Instance: "1loop-" + core.Identifier(),
		Status: func(err error) {
			bridge.mu.Lock()
			defer bridge.mu.Unlock()
			if ctx.Err() == nil {
				bridge.tunnelError, bridge.tunnelReady = err, err == nil
			}
		},
	}
	go func() { _ = tunnel.Run(ctx) }()
	return nil
}

func (bridge *ChatGPTWeb) TunnelStatus() string {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.tunnelError != nil {
		return bridge.tunnelError.Error()
	}
	if bridge.tunnelReady {
		return "Tunnel connected. Keep 1loop running while using the connector."
	}
	if bridge.stopTunnel == nil {
		return "Tunnel not started."
	}
	return "Connecting to the tunnel…"
}

func (bridge *ChatGPTWeb) Close() {
	bridge.mu.Lock()
	if bridge.stopTunnel != nil {
		bridge.stopTunnel()
		bridge.stopTunnel = nil
	}
	bridge.engine.Cancel()
	bridge.mu.Unlock()
	bridge.engine.WaitClosed()
}

func (bridge *ChatGPTWeb) Models(ctx context.Context, conn Connection) ([]ModelChoice, error) {
	tab := bridge.Page("setup", "https://chatgpt.com/")
	if err := webModelMenu(ctx, tab, true); err != nil {
		return nil, err
	}
	defer func() { _ = tab.Run(ctx, chromedp.KeyEvent(kb.Escape)) }()
	var rows []struct {
		Name     string `json:"name"`
		Selected bool   `json:"selected"`
	}
	if err := tab.Run(ctx, evaluateWeb(`Array.from(document.querySelectorAll('[role="menuitemradio"]')).filter(e=>!e.closest('[aria-hidden="true"]')&&e.getAttribute('aria-disabled')!=='true').map(e=>({name:e.innerText.trim().split('\n')[0],selected:e.getAttribute('aria-checked')==='true'}))`, &rows)); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("ChatGPT did not expose any available models")
	}
	originalModel := ""
	var originalEffort string
	_ = tab.Run(ctx, evaluateWeb(`document.querySelector('[data-selected-reasoning-effort]')?.dataset.selectedReasoningEffort||''`, &originalEffort))
	for _, row := range rows {
		if row.Selected {
			originalModel = row.Name
		}
	}
	defer func() {
		if originalModel != "" {
			_ = webSelectModel(ctx, tab, originalModel)
			if originalEffort != "" {
				_ = webSelectEffort(ctx, tab, originalEffort)
			}
		}
	}()
	var choices []ModelChoice
	for _, row := range rows {
		if err := webSelectModel(ctx, tab, row.Name); err != nil {
			return nil, err
		}
		efforts, current, err := webModelEfforts(ctx, tab)
		if err != nil {
			return nil, err
		}
		choices = append(choices, ModelChoice{Connection: conn, ID: row.Name, Name: row.Name, Efforts: sortedEfforts(efforts), DefaultEffort: current, ReasoningMode: "effort", Source: "ChatGPT browser model picker"})
	}
	return choices, nil
}

func (backend *App) CloseChatGPTWeb() {
	backend.mu.Lock()
	var accounts []*ChatGPTWeb
	for _, account := range backend.webAccounts {
		accounts = append(accounts, account)
	}
	backend.mu.Unlock()
	for _, account := range accounts {
		account.Close()
	}
}
