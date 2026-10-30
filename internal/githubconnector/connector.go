// Package githubconnector owns the user's explicit GitHub connection.
// Credentials stay on the execution target, outside model configuration.
package githubconnector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"1loop/internal/core"
	"1loop/internal/extensions"
)

const ServerName = "1loop-github-connector"

// ClientID can be supplied by the publisher at build time. It is public;
// device authorization never embeds an application secret in 1loop.
var ClientID = "Iv23limWVmHkejd9nSPt"

const AppURL = "https://github.com/apps/1loop-github-connector"

var (
	loginURL = "https://github.com/login"
	apiURL   = "https://api.github.com"
	mcpURL   = "https://api.githubcopilot.com/mcp/x/all"
)

type credentials struct {
	Connection   string    `json:"connection,omitempty"`
	ClientID     string    `json:"clientId"`
	Login        string    `json:"login,omitempty"`
	Token        string    `json:"token,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	Expires      time.Time `json:"expires,omitempty"`
	Enabled      bool      `json:"enabled"`
	// The attachment endpoint refuses the app's tokens (attachments.go).
	AttachmentToken string `json:"attachmentToken,omitempty"`
	AttachmentLogin string `json:"attachmentLogin,omitempty"`
}

// Status is safe to send to the settings UI. It contains no access token,
// refresh token, or private device code.
type Status struct {
	Configured bool      `json:"configured"`
	ClientID   string    `json:"clientId"`
	Connected  bool      `json:"connected"`
	Enabled    bool      `json:"enabled"`
	Login      string    `json:"login"`
	InstallURL string    `json:"installUrl"`
	URL        string    `json:"url,omitempty"`
	Code       string    `json:"code,omitempty"`
	Expires    time.Time `json:"expires,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Attachments: the saved personal access token's account, else whether
	// GitHub CLI can sign them.
	AttachmentLogin string `json:"attachmentLogin,omitempty"`
	GitHubCLI       bool   `json:"githubCli"`
}

type pending struct {
	cancel  context.CancelFunc
	url     string
	code    string
	expires time.Time
	err     string
}

type Manager struct {
	mu      sync.Mutex
	refresh sync.Mutex
	pending *pending
	changed chan struct{}
	client  *http.Client
	dir     func() string
}

var Default = &Manager{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, dir: func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop")
}}

func (manager *Manager) path() string { return filepath.Join(manager.dir(), "github-connector.json") }

func (manager *Manager) load() (credentials, error) {
	var saved credentials
	if manager.dir() == "" {
		return saved, fmt.Errorf("No private configuration folder is available for GitHub")
	}
	data, err := os.ReadFile(manager.path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return saved, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &saved); err != nil {
			return saved, fmt.Errorf("Could not read the GitHub connection")
		}
	}
	if saved.ClientID == "" {
		saved.ClientID = ClientID
		if saved.ClientID == "" {
			saved.ClientID = os.Getenv("ONELOOP_GITHUB_CLIENT_ID")
		}
	}
	return saved, nil
}

func (manager *Manager) save(saved credentials) error {
	if manager.dir() == "" {
		return fmt.Errorf("No private configuration folder is available for GitHub")
	}
	if err := os.MkdirAll(manager.dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	path := manager.path() + ".tmp"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Rename(path, manager.path())
}

func connected(saved credentials) bool {
	return saved.Token != "" && saved.Login != "" && (saved.Expires.IsZero() || time.Now().Before(saved.Expires) || saved.RefreshToken != "")
}

func (manager *Manager) Status() Status {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err != nil {
		return Status{Error: err.Error()}
	}
	status := Status{Configured: saved.ClientID != "", ClientID: saved.ClientID, Connected: connected(saved), Enabled: saved.Enabled, Login: saved.Login, InstallURL: "https://github.com/settings/installations", AttachmentLogin: saved.AttachmentLogin, GitHubCLI: cliInstalled()}
	if saved.ClientID == ClientID {
		status.InstallURL = AppURL + "/installations/new"
	}
	if pending := manager.pending; pending != nil {
		status.URL, status.Code, status.Expires, status.Error = pending.url, pending.code, pending.expires, pending.err
	}
	return status
}

func (manager *Manager) Available() bool {
	state := manager.Status()
	return state.Connected && state.Enabled
}

func connectionID(saved credentials) string {
	if saved.Connection != "" {
		return saved.Connection
	}
	sum := sha256.Sum256([]byte(saved.Token))
	return hex.EncodeToString(sum[:])
}

func (manager *Manager) ConnectionID() string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err != nil || !connected(saved) {
		return ""
	}
	return connectionID(saved)
}

func (manager *Manager) Changes() <-chan struct{} {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.changed == nil {
		manager.changed = make(chan struct{})
	}
	return manager.changed
}

func (manager *Manager) notifyLocked() {
	if manager.changed != nil {
		close(manager.changed)
	}
	manager.changed = make(chan struct{})
}

func (manager *Manager) Configure(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 || strings.ContainsAny(id, " \t\r\n/:?") {
		return fmt.Errorf("Enter the public client ID of 1loop's GitHub App")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err != nil {
		return err
	}
	if connected(saved) || manager.pending != nil {
		return fmt.Errorf("Disconnect or cancel sign-in before changing the GitHub App")
	}
	saved.ClientID = id
	return manager.save(saved)
}

func (manager *Manager) Enable(on bool) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err != nil {
		return err
	}
	if on && !connected(saved) {
		return fmt.Errorf("Connect GitHub before enabling its tools")
	}
	if !on && manager.pending != nil {
		manager.pending.cancel()
		manager.pending = nil
	}
	changed := saved.Enabled != on
	saved.Enabled = on
	err = manager.save(saved)
	if err == nil && changed {
		manager.notifyLocked()
	}
	return err
}

func (manager *Manager) Cancel() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.pending != nil {
		manager.pending.cancel()
		manager.pending = nil
	}
}

func (manager *Manager) Disconnect() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err != nil {
		return err
	}
	if manager.pending != nil {
		manager.pending.cancel()
		manager.pending = nil
	}
	err = manager.save(credentials{ClientID: saved.ClientID})
	if err == nil {
		manager.notifyLocked()
	}
	return err
}

type tokenReply struct {
	Token        string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

func (manager *Manager) post(ctx context.Context, endpoint string, values url.Values, reply any) error {
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := manager.client.Do(request)
	if err != nil {
		return fmt.Errorf("Could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub sign-in returned HTTP %d", response.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(reply) != nil {
		return fmt.Errorf("GitHub returned an invalid sign-in response")
	}
	return nil
}

func (manager *Manager) Start(ctx context.Context) error {
	manager.mu.Lock()
	saved, err := manager.load()
	if err != nil {
		manager.mu.Unlock()
		return err
	}
	if saved.ClientID == "" || connected(saved) || manager.pending != nil {
		manager.mu.Unlock()
		return fmt.Errorf("Configure a GitHub App first, or cancel the existing connection attempt")
	}
	operation, cancel := context.WithCancel(context.Background())
	attempt := &pending{cancel: cancel}
	manager.pending = attempt
	manager.mu.Unlock()
	var reply struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URL        string `json:"verification_uri"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
		Error      string `json:"error"`
	}
	err = manager.post(ctx, loginURL+"/device/code", url.Values{"client_id": {saved.ClientID}}, &reply)
	if err == nil && (reply.Error != "" || reply.DeviceCode == "" || reply.UserCode == "" || reply.URL != "https://github.com/login/device" || reply.ExpiresIn <= 0) {
		err = fmt.Errorf("GitHub device sign-in is unavailable. Check the app's client ID and enable Device flow in its settings")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.pending != attempt {
		cancel()
		return context.Canceled
	}
	if err != nil {
		cancel()
		manager.pending = nil
		return err
	}
	attempt.url, attempt.code, attempt.expires = reply.URL, reply.UserCode, time.Now().Add(time.Duration(reply.ExpiresIn)*time.Second)
	go manager.poll(operation, attempt, saved.ClientID, reply.DeviceCode, max(5, reply.Interval))
	return nil
}

func (manager *Manager) poll(ctx context.Context, attempt *pending, clientID, device string, seconds int) {
	ctx, cancel := context.WithDeadline(ctx, attempt.expires)
	defer cancel()
	for {
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			manager.finish(attempt, credentials{}, fmt.Errorf("GitHub sign-in expired or was cancelled"))
			return
		case <-timer.C:
		}
		var reply tokenReply
		err := manager.post(ctx, loginURL+"/oauth/access_token", url.Values{"client_id": {clientID}, "device_code": {device}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &reply)
		if err == nil && reply.Error == "authorization_pending" {
			continue
		}
		if err == nil && reply.Error == "slow_down" {
			seconds += 5
			continue
		}
		if err == nil && (reply.Error != "" || reply.Token == "") {
			err = fmt.Errorf("GitHub sign-in was declined or expired. Start a new connection attempt")
		}
		var login string
		if err == nil {
			login, err = manager.user(ctx, reply.Token)
		}
		saved := credentials{Connection: core.Identifier(), ClientID: clientID, Login: login, Token: reply.Token, RefreshToken: reply.RefreshToken, Enabled: true}
		if reply.ExpiresIn > 0 {
			saved.Expires = time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)
		}
		manager.finish(attempt, saved, err)
		return
	}
}

func (manager *Manager) finish(attempt *pending, saved credentials, err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.pending != attempt {
		return
	}
	attempt.cancel()
	if err == nil {
		if current, loadErr := manager.load(); loadErr == nil {
			saved.AttachmentToken, saved.AttachmentLogin = current.AttachmentToken, current.AttachmentLogin
		}
		err = manager.save(saved)
	}
	if err != nil {
		attempt.url, attempt.code, attempt.err = "", "", err.Error()
		return
	}
	manager.pending = nil
	manager.notifyLocked()
}

func (manager *Manager) user(ctx context.Context, token string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", apiURL+"/user", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := manager.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Could not verify the GitHub account")
	}
	defer response.Body.Close()
	var user struct{ Login string }
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user) != nil || user.Login == "" {
		return "", fmt.Errorf("GitHub did not authorize this account. Connect again")
	}
	return user.Login, nil
}

// Server supplies a short-lived descriptor to the existing MCP client.
// Neither the UI nor the model receives its Authorization header.
func (manager *Manager) Server(ctx context.Context) (extensions.MCPServer, error) {
	return manager.server(ctx, "")
}

// Refresh retries a rejected access token without discarding a valid refresh token.
func (manager *Manager) Refresh(ctx context.Context, authorization string) (extensions.MCPServer, error) {
	return manager.server(ctx, authorization)
}

func (manager *Manager) server(ctx context.Context, rejected string) (extensions.MCPServer, error) {
	manager.refresh.Lock()
	defer manager.refresh.Unlock()
	if err := ctx.Err(); err != nil {
		return extensions.MCPServer{}, err
	}
	manager.mu.Lock()
	saved, err := manager.load()
	manager.mu.Unlock()
	if err != nil {
		return extensions.MCPServer{}, err
	}
	if !saved.Enabled || !connected(saved) {
		return extensions.MCPServer{}, fmt.Errorf("The GitHub connector is disconnected or disabled")
	}
	if (!saved.Expires.IsZero() && time.Until(saved.Expires) < time.Minute) || (rejected != "" && rejected == "Bearer "+saved.Token) {
		var reply tokenReply
		if saved.RefreshToken == "" {
			manager.RejectAuthorization("Bearer " + saved.Token)
			return extensions.MCPServer{}, fmt.Errorf("GitHub authorization expired. Connect again")
		}
		// Rotation must finish and be saved even if the requesting turn stops.
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		err := manager.post(refreshCtx, loginURL+"/oauth/access_token", url.Values{"client_id": {saved.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {saved.RefreshToken}}, &reply)
		cancel()
		if err != nil {
			return extensions.MCPServer{}, err
		}
		if reply.Error != "" || reply.Token == "" {
			manager.RejectAuthorization("Bearer " + saved.Token)
			return extensions.MCPServer{}, fmt.Errorf("GitHub authorization expired or was revoked. Reconnect GitHub")
		}
		manager.mu.Lock()
		current, readErr := manager.load()
		if readErr != nil || current.Token != saved.Token {
			manager.mu.Unlock()
			return extensions.MCPServer{}, fmt.Errorf("The GitHub connection changed during refresh")
		}
		saved.Connection, saved.Enabled = connectionID(saved), current.Enabled
		saved.Token, saved.RefreshToken = reply.Token, reply.RefreshToken
		saved.Expires = time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)
		err = manager.save(saved)
		manager.mu.Unlock()
		if err != nil {
			return extensions.MCPServer{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return extensions.MCPServer{}, err
	}
	if !saved.Enabled {
		return extensions.MCPServer{}, fmt.Errorf("The GitHub connector is disabled")
	}
	return extensions.MCPServer{Name: ServerName, Type: "http", URL: mcpURL, Headers: map[string]string{"Authorization": "Bearer " + saved.Token}, Enabled: true}, nil
}

// RejectAuthorization forgets only the credential that GitHub rejected,
// without disconnecting a newer sign-in that completed in the meantime.
func (manager *Manager) RejectAuthorization(authorization string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	saved, err := manager.load()
	if err == nil && saved.Token != "" && authorization == "Bearer "+saved.Token {
		if manager.save(credentials{ClientID: saved.ClientID, AttachmentToken: saved.AttachmentToken, AttachmentLogin: saved.AttachmentLogin}) == nil {
			manager.notifyLocked()
		}
	}
}
