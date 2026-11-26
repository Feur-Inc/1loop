// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"1loop/internal/core"
)

const openAIResource = "https://chatgpt.com/backend-api/codex"

// Public OAuth client used by the subscription login protocol in OpenCode.
const openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const openAIClientVersion = "1.0.0"
const openAIIssuer = "https://auth.openai.com"

type openAITokens struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ID        string `json:"id_token"`
	Scope     string `json:"scope"`
	Type      string `json:"token_type"`
	ExpiresIn int64  `json:"expires_in"`
}

type openAIAccount struct {
	ClientID string       `json:"client_id"`
	Subject  string       `json:"subject"`
	Email    string       `json:"email"`
	Expires  time.Time    `json:"expires_at"`
	Tokens   openAITokens `json:"tokens"`
}

type openAIAuth struct {
	Mu      sync.Mutex    `json:"-"`
	Account openAIAccount `json:"account"`
	path    string
	relay   *openAIPending // a relayed sign-in waiting for its code (Mu)
}

func newOpenAIAuth(profiles ...string) (*openAIAuth, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	name := "openai.json"
	if len(profiles) > 0 && profiles[0] != "" {
		if !regexp.MustCompile(`^[a-f0-9]+$`).MatchString(profiles[0]) {
			return nil, fmt.Errorf("Invalid account profile")
		}
		name = "openai-" + profiles[0] + ".json"
	}
	auth := &openAIAuth{path: filepath.Join(dir, "1loop", name)}
	data, err := os.ReadFile(auth.path)
	if err == nil {
		if err = json.Unmarshal(data, auth); err != nil {
			return nil, fmt.Errorf("Unable to read OpenAI credentials: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return auth, nil
}

func (auth *openAIAuth) save() error {
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(auth.path), 0700); err != nil {
		return err
	}
	// Rename publishes the complete token set together, including a rotated refresh token.
	file, err := os.CreateTemp(filepath.Dir(auth.path), ".openai-*")
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), auth.path)
}

func tokenExchange(ctx context.Context, form url.Values) (openAITokens, error) {
	var openAITokensValue openAITokens
	req, err := http.NewRequestWithContext(ctx, "POST", openAITokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return openAITokensValue, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return openAITokensValue, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return openAITokensValue, fmt.Errorf("OpenAI sign-in: HTTP %d; reconnect your account", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&openAITokensValue)
	if openAITokensValue.Type == "" {
		openAITokensValue.Type = "Bearer"
	}
	if openAITokensValue.ExpiresIn == 0 {
		openAITokensValue.ExpiresIn = 3600
	}
	if err == nil && (openAITokensValue.Access == "" || !strings.EqualFold(openAITokensValue.Type, "Bearer") || openAITokensValue.ExpiresIn <= 0) {
		err = fmt.Errorf("Incomplete OpenAI tokens")
	}
	return openAITokensValue, err
}

func (auth *openAIAuth) credential(ctx context.Context) (string, error) {
	auth.Mu.Lock()
	defer auth.Mu.Unlock()
	openAIAccount := auth.Account
	if openAIAccount.ClientID != openAIClientID {
		return "", fmt.Errorf("Reconnect this ChatGPT account to use the subscription login")
	}
	if openAIAccount.Tokens.Access == "" {
		return "", fmt.Errorf("Connect your OpenAI account in Settings")
	}
	if time.Now().Add(time.Minute).After(openAIAccount.Expires) {
		if openAIAccount.Tokens.Refresh == "" {
			return "", fmt.Errorf("OpenAI session expired; reconnect your account")
		}
		openAITokens, err := tokenExchange(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"client_id":     {openAIAccount.ClientID},
			"refresh_token": {openAIAccount.Tokens.Refresh},
		})
		if err != nil {
			return "", err
		}
		if openAITokens.Refresh == "" {
			return "", fmt.Errorf("Incomplete OpenAI token renewal; reconnect your account")
		}
		if openAITokens.ID == "" {
			openAITokens.ID = openAIAccount.Tokens.ID
		}
		if openAITokens.Scope == "" {
			openAITokens.Scope = openAIAccount.Tokens.Scope
		}
		openAIAccount.Tokens = openAITokens
		openAIAccount.Expires = time.Now().Add(time.Duration(openAITokens.ExpiresIn) * time.Second)
		auth.Account = openAIAccount
		if err = auth.save(); err != nil {
			return "", err
		}
	}

	return auth.Account.Tokens.Access, nil
}

// openAICallback is where OpenAI sends the browser back: the public client
// accepts only this address, so a relayed sign-in names it too.
const openAICallback = "http://localhost:1455/auth/callback"

// openAITokenURL is OpenAI's token endpoint; a test points it at its own.
var openAITokenURL = openAIIssuer + "/oauth/token"

// openAIPending is a sign-in between its link and the code that comes back:
// the PKCE verifier stays here, and the account it may replace.
type openAIPending struct {
	state, verifier string
	original        openAIAccount
	expires         time.Time
}

// authorize starts a sign-in: its link and what checks the code returned.
func (auth *openAIAuth) authorize() (string, *openAIPending) {
	pending := &openAIPending{state: core.Identifier(), verifier: core.Identifier(), expires: time.Now().Add(5 * time.Minute)}
	digest := sha256.Sum256([]byte(pending.verifier))
	auth.Mu.Lock()
	pending.original = auth.Account
	auth.Mu.Unlock()
	q := url.Values{
		"client_id":                  {openAIClientID},
		"response_type":              {"code"},
		"redirect_uri":               {openAICallback},
		"scope":                      {"openid profile email offline_access"},
		"state":                      {pending.state},
		"code_challenge_method":      {"S256"},
		"code_challenge":             {base64.RawURLEncoding.EncodeToString(digest[:])},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"1loop"},
	}
	return openAIIssuer + "/oauth/authorize?" + q.Encode(), pending
}

// finish trades the code for tokens, checks the identity and saves the
// account: the one path of every sign-in, wherever its browser ran.
func (auth *openAIAuth) finish(ctx context.Context, pending *openAIPending, code string) error {
	if code == "" {
		return fmt.Errorf("Missing authorization code")
	}
	openAITokens, err := tokenExchange(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAIClientID},
		"code":          {code},
		"code_verifier": {pending.verifier},
		"redirect_uri":  {openAICallback},
	})
	if err != nil {
		return err
	}
	sub, email, err := validateOpenAIID(ctx, openAITokens.ID, openAIClientID, "")
	if err != nil {
		return err
	}
	original := pending.original
	if original.ClientID == openAIClientID && original.Subject != "" && sub != original.Subject {
		return fmt.Errorf("The account does not match the selected session")
	}
	auth.Mu.Lock()
	defer auth.Mu.Unlock()
	previous := auth.Account
	auth.Account = openAIAccount{
		ClientID: openAIClientID,
		Subject:  sub,
		Email:    email,
		Tokens:   openAITokens,
		Expires:  time.Now().Add(time.Duration(openAITokens.ExpiresIn) * time.Second),
	}
	if err = auth.save(); err != nil {
		auth.Account = previous
	}
	return err
}

// begin starts only a loopback callback. The user opens the authorization link.
func (auth *openAIAuth) begin(ctx context.Context) (string, <-chan error, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return "", nil, err
	}
	link, pending := auth.authorize()

	result := make(chan error, 1)
	var once sync.Once
	finish := func(err error) { once.Do(func() { result <- err }) }
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" || request.URL.Query().Get("state") != pending.state {
			http.Error(writer, "Invalid OAuth state", 400)
			return
		}
		once.Do(func() {
			returned := request.URL.Query()
			loginErr := fmt.Errorf("OpenAI sign-in denied")
			if returned.Get("error") == "" {
				loginErr = auth.finish(ctx, pending, returned.Get("code"))
			}
			if loginErr != nil {
				http.Error(writer, "Sign-in failed. Check 1loop for details.", 400)
			} else {
				writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
				fmt.Fprint(writer, "Sign-in successful. You can return to 1loop.")
			}
			result <- loginErr
		})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			finish(err)
		}
	}()
	done := make(chan error, 1)
	go func() {
		select {
		case err := <-result:
			done <- err
		case <-ctx.Done():
			finish(ctx.Err())
			done <- ctx.Err()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return link, done, nil
}

// openAIKeys are OpenAI's signing keys, fetched once and again when a token
// names a key they lack. A test replaces them.
var (
	openAIKeysOnce sync.Once
	openAIKeys     oidc.KeySet
)

// validateOpenAIID checks an ID token from OpenAI's sign-in (signature,
// issuer, audience, expiry and nonce) and gives its subject and email.
func validateOpenAIID(ctx context.Context, token, clientID, nonce string) (string, string, error) {
	openAIKeysOnce.Do(func() {
		if openAIKeys == nil {
			fetch := oidc.ClientContext(context.Background(), &http.Client{Timeout: 20 * time.Second})
			openAIKeys = oidc.NewRemoteKeySet(fetch, openAIIssuer+"/.well-known/jwks.json")
		}
	})
	verifier := oidc.NewVerifier(openAIIssuer, openAIKeys, &oidc.Config{ClientID: clientID})
	idToken, err := verifier.Verify(ctx, token)
	if err != nil {
		return "", "", fmt.Errorf("Invalid or expired OpenAI identity: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", err
	}
	if idToken.Subject == "" || (nonce != "" && idToken.Nonce != nonce) {
		return "", "", fmt.Errorf("Invalid or expired OpenAI identity")
	}
	return idToken.Subject, claims.Email, nil
}

func (backend *App) GetOpenAI(profile string) (*openAIAuth, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if profile == "" && backend.openAI != nil {
		return backend.openAI, nil
	}
	if backend.openAIProfiles == nil {
		backend.openAIProfiles = map[string]*openAIAuth{}
	}
	if auth := backend.openAIProfiles[profile]; auth != nil {
		return auth, nil
	}
	auth, err := newOpenAIAuth(profile)
	if err != nil {
		return nil, err
	}
	backend.openAIProfiles[profile] = auth
	if profile == "" {
		backend.openAI = auth
	}
	return auth, nil
}

func (backend *App) openAIConfig(ctx context.Context, cfg Config) (Config, error) {
	auth, err := backend.GetOpenAI(cfg.AuthProfile)
	if err != nil {
		return cfg, err
	}
	cfg.APIKey, err = auth.credential(ctx)
	if err != nil {
		return cfg, err
	}
	auth.Mu.Lock()
	cfg.AccountID, cfg.Residency = openAIAccountMetadata(auth.Account.Tokens)
	auth.Mu.Unlock()
	return cfg, nil
}

func (backend *App) BeginOpenAI(ctx context.Context, profiles ...string) (string, <-chan error, error) {
	profile := ""
	if len(profiles) > 0 {
		profile = profiles[0]
	}
	auth, err := backend.GetOpenAI(profile)
	if err != nil {
		return "", nil, err
	}
	return auth.begin(ctx)
}

// BeginOpenAIRelay starts a sign-in whose browser runs on another device
// (a paired desktop): it gives the link to open there and its state. The
// device catches OpenAI's redirect on its own localhost:1455 and hands the
// code to CompleteOpenAI; the verifier and the tokens stay here. A new one
// replaces the profile's previous one, and it lasts five minutes.
func (backend *App) BeginOpenAIRelay(profile string) (link, state string, err error) {
	auth, err := backend.GetOpenAI(profile)
	if err != nil {
		return "", "", err
	}
	link, pending := auth.authorize()
	auth.Mu.Lock()
	auth.relay = pending
	auth.Mu.Unlock()
	return link, pending.state, nil
}

// CompleteOpenAI finishes the profile's relayed sign-in with the code
// OpenAI returned to the other device, if state is the one it began with.
// The sign-in is used up either way: a code is good once.
func (backend *App) CompleteOpenAI(ctx context.Context, profile, state, code string) error {
	auth, err := backend.GetOpenAI(profile)
	if err != nil {
		return err
	}
	auth.Mu.Lock()
	pending := auth.relay
	if pending == nil || time.Now().After(pending.expires) {
		auth.relay = nil
		auth.Mu.Unlock()
		return fmt.Errorf("This sign-in has expired; start it again")
	}
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(pending.state)) != 1 {
		auth.Mu.Unlock()
		return fmt.Errorf("Invalid OAuth state")
	}
	auth.relay = nil
	auth.Mu.Unlock()
	return auth.finish(ctx, pending, code)
}

func (backend *App) DisconnectOpenAI(ctx context.Context, profile string) (string, error) {
	auth, err := backend.GetOpenAI(profile)
	if err != nil {
		return "", err
	}
	auth.Mu.Lock()
	defer auth.Mu.Unlock()
	var warning string
	if auth.Account.Tokens.Refresh != "" {
		err = revokeOpenAI(ctx, auth.Account)
		if err != nil {
			warning = "Disconnected locally, but remote revocation was not confirmed. You can disconnect 1loop in ChatGPT Settings. " + err.Error()
		}
	}
	auth.Account.Tokens = openAITokens{}
	auth.relay = nil
	return warning, auth.save()
}

func revokeOpenAI(ctx context.Context, account openAIAccount) error {
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	discovery, err := http.NewRequestWithContext(ctx, "GET", openAIIssuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(discovery)
	if err != nil {
		return err
	}
	var metadata struct {
		Issuer     string `json:"issuer"`
		Revocation string `json:"revocation_endpoint"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata)
	response.Body.Close()
	if err != nil {
		return err
	}
	target, err := url.Parse(metadata.Revocation)
	if response.StatusCode != 200 || metadata.Issuer != openAIIssuer || err != nil || target.Scheme != "https" || target.Host != "auth.openai.com" || target.User != nil {
		return fmt.Errorf("Invalid OpenAI revocation endpoint")
	}
	form := url.Values{"token": {account.Tokens.Refresh}, "token_type_hint": {"refresh_token"}, "client_id": {account.ClientID}}
	req, err := http.NewRequestWithContext(ctx, "POST", target.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(req)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Revocation: HTTP %d", response.StatusCode)
	}
	return nil
}

func openAIAccountMetadata(tokens openAITokens) (accountID, residency string) {
	for _, token := range []string{tokens.ID, tokens.Access} {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims struct {
			AccountID string `json:"chatgpt_account_id"`
			Residency string `json:"chatgpt_compute_residency"`
			Auth      struct {
				AccountID string `json:"chatgpt_account_id"`
				Residency string `json:"chatgpt_compute_residency"`
			} `json:"https://api.openai.com/auth"`
			Organizations []struct {
				ID string `json:"id"`
			} `json:"organizations"`
		}
		if json.Unmarshal(raw, &claims) != nil {
			continue
		}
		if accountID == "" {
			accountID = claims.AccountID
			if accountID == "" {
				accountID = claims.Auth.AccountID
			}
			if accountID == "" && len(claims.Organizations) > 0 {
				accountID = claims.Organizations[0].ID
			}
		}
		value := claims.Residency
		if value == "" {
			value = claims.Auth.Residency
		}
		if value != "" && value != "no_constraint" {
			residency = value
		}
	}
	return
}

// StubOpenAISignIn sends the sign-ins' code exchanges to tokenURL and
// checks their ID tokens with keys, until restore: for the tests of the
// packages that relay a sign-in. Nothing else calls it.
func StubOpenAISignIn(tokenURL string, keys oidc.KeySet) (restore func()) {
	openAIKeysOnce.Do(func() {})
	savedURL, savedKeys := openAITokenURL, openAIKeys
	openAITokenURL, openAIKeys = tokenURL, keys
	return func() { openAITokenURL, openAIKeys = savedURL, savedKeys }
}
