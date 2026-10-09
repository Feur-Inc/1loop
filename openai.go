package main

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
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
	mu      sync.Mutex
	Account openAIAccount `json:"account"`
	path    string
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
	a := &openAIAuth{path: filepath.Join(dir, "1loop", name)}
	data, err := os.ReadFile(a.path)
	if err == nil {
		if err = json.Unmarshal(data, a); err != nil {
			return nil, fmt.Errorf("Unable to read OpenAI credentials: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return a, nil
}
func (a *openAIAuth) save() error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return err
	}
	// Rename publishes the complete token set together, including a rotated refresh token.
	f, err := os.CreateTemp(filepath.Dir(a.path), ".openai-*")
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), a.path)
}
func tokenExchange(ctx context.Context, form url.Values) (openAITokens, error) {
	var t openAITokens
	req, err := http.NewRequestWithContext(ctx, "POST", openAIIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return t, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return t, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return t, fmt.Errorf("OpenAI sign-in: HTTP %d; reconnect your account", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t)
	if t.Type == "" {
		t.Type = "Bearer"
	}
	if t.ExpiresIn == 0 {
		t.ExpiresIn = 3600
	}
	if err == nil && (t.Access == "" || !strings.EqualFold(t.Type, "Bearer") || t.ExpiresIn <= 0) {
		err = fmt.Errorf("Incomplete OpenAI tokens")
	}
	return t, err
}
func (a *openAIAuth) credential(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.Account
	if c.ClientID != openAIClientID {
		return "", fmt.Errorf("Reconnect this ChatGPT account to use the subscription login")
	}
	if c.Tokens.Access == "" {
		return "", fmt.Errorf("Connect your OpenAI account in Settings")
	}
	if time.Now().Add(time.Minute).After(c.Expires) {
		if c.Tokens.Refresh == "" {
			return "", fmt.Errorf("OpenAI session expired; reconnect your account")
		}
		t, err := tokenExchange(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {c.Tokens.Refresh}})
		if err != nil {
			return "", err
		}
		if t.Refresh == "" {
			return "", fmt.Errorf("Incomplete OpenAI token renewal; reconnect your account")
		}
		if t.ID == "" {
			t.ID = c.Tokens.ID
		}
		if t.Scope == "" {
			t.Scope = c.Tokens.Scope
		}
		c.Tokens = t
		c.Expires = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
		a.Account = c
		if err = a.save(); err != nil {
			return "", err
		}
	}

	return a.Account.Tokens.Access, nil
}

// begin starts only a loopback callback. The user opens the authorization link.
func (a *openAIAuth) begin(ctx context.Context) (string, <-chan error, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return "", nil, err
	}
	state, verifier := identifier(), identifier()
	digest := sha256.Sum256([]byte(verifier))
	callback := "http://localhost:1455/auth/callback"
	a.mu.Lock()
	original := a.Account
	a.mu.Unlock()
	clientID := openAIClientID
	q := url.Values{"client_id": {clientID}, "response_type": {"code"}, "redirect_uri": {callback}, "scope": {"openid profile email offline_access"}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"}, "originator": {"1loop"}}

	result := make(chan error, 1)
	var once sync.Once
	finish := func(err error) { once.Do(func() { result <- err }) }
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid OAuth state", 400)
			return
		}
		var loginErr error
		once.Do(func() {
			returned := r.URL.Query()
			if returned.Get("error") != "" {
				loginErr = fmt.Errorf("OpenAI sign-in denied")
			} else {
				issued := clientID
				if loginErr == nil && returned.Get("code") == "" {
					loginErr = fmt.Errorf("Missing authorization code")
				}
				if loginErr == nil {
					t, e := tokenExchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {issued}, "code": {returned.Get("code")}, "code_verifier": {verifier}, "redirect_uri": {callback}})
					loginErr = e
					if e == nil {
						sub, email, e := validateOpenAIID(ctx, t.ID, issued, "")
						loginErr = e
						if e == nil && original.ClientID == issued && original.Subject != "" && sub != original.Subject {
							loginErr = fmt.Errorf("The account does not match the selected session")
						}

						if loginErr == nil {
							a.mu.Lock()
							previous := a.Account
							a.Account = openAIAccount{ClientID: issued, Subject: sub, Email: email, Tokens: t, Expires: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)}
							loginErr = a.save()
							if loginErr != nil {
								a.Account = previous
							}
							a.mu.Unlock()
						}
					}
				}
			}
			if loginErr != nil {
				http.Error(w, "Sign-in failed. Check 1loop for details.", 400)
			} else {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				fmt.Fprint(w, "Sign-in successful. You can return to 1loop.")
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
	return openAIIssuer + "/oauth/authorize?" + q.Encode(), done, nil
}
func validateOpenAIID(ctx context.Context, token, clientID, nonce string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("Invalid OpenAI ID token")
	}
	decode := func(s string, v any) error {
		b, e := base64.RawURLEncoding.DecodeString(s)
		if e != nil {
			return e
		}
		return json.Unmarshal(b, v)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decode(parts[0], &header); err != nil || header.Alg != "RS256" {
		return "", "", fmt.Errorf("Unsupported OpenAI signature")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", openAIIssuer+"/.well-known/jwks.json", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("OpenAI signing keys are unavailable")
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); err != nil {
		return "", "", err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	valid := false
	for _, key := range jwks.Keys {
		if key.Kid != header.Kid || key.Kty != "RSA" {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(key.N)
		e, e2 := base64.RawURLEncoding.DecodeString(key.E)
		exponent := new(big.Int).SetBytes(e)
		if e1 != nil || e2 != nil || !exponent.IsInt64() {
			continue
		}
		valid = rsa.VerifyPKCS1v15(&rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}, crypto.SHA256, hash[:], signature) == nil
		if valid {
			break
		}
	}
	if !valid {
		return "", "", fmt.Errorf("Invalid OpenAI account signature")
	}
	var claims struct {
		Iss   string          `json:"iss"`
		Sub   string          `json:"sub"`
		Email string          `json:"email"`
		Nonce string          `json:"nonce"`
		Exp   int64           `json:"exp"`
		Aud   json.RawMessage `json:"aud"`
	}
	if err = decode(parts[1], &claims); err != nil {
		return "", "", err
	}
	var audiences []string
	var single string
	if json.Unmarshal(claims.Aud, &single) == nil {
		audiences = []string{single}
	} else {
		_ = json.Unmarshal(claims.Aud, &audiences)
	}
	audienceOK := false
	for _, v := range audiences {
		if v == clientID {
			audienceOK = true
		}
	}
	if claims.Iss != openAIIssuer || claims.Sub == "" || (nonce != "" && claims.Nonce != nonce) || claims.Exp <= time.Now().Unix() || !audienceOK {
		return "", "", fmt.Errorf("Invalid or expired OpenAI identity")
	}
	return claims.Sub, claims.Email, nil
}

func (a *app) getOpenAI(profile string) (*openAIAuth, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if profile == "" && a.openAI != nil {
		return a.openAI, nil
	}
	if a.openAIProfiles == nil {
		a.openAIProfiles = map[string]*openAIAuth{}
	}
	if auth := a.openAIProfiles[profile]; auth != nil {
		return auth, nil
	}
	auth, err := newOpenAIAuth(profile)
	if err != nil {
		return nil, err
	}
	a.openAIProfiles[profile] = auth
	if profile == "" {
		a.openAI = auth
	}
	return auth, nil
}
func (a *app) openAIConfig(ctx context.Context, cfg config) (config, error) {
	auth, err := a.getOpenAI(cfg.AuthProfile)
	if err != nil {
		return cfg, err
	}
	cfg.APIKey, err = auth.credential(ctx)
	if err != nil {
		return cfg, err
	}
	auth.mu.Lock()
	cfg.AccountID, cfg.Residency = openAIAccountMetadata(auth.Account.Tokens)
	auth.mu.Unlock()
	return cfg, nil
}
func (a *app) beginOpenAI(ctx context.Context, profiles ...string) (string, <-chan error, error) {
	profile := ""
	if len(profiles) > 0 {
		profile = profiles[0]
	}
	auth, err := a.getOpenAI(profile)
	if err != nil {
		return "", nil, err
	}
	return auth.begin(ctx)
}
func (a *app) disconnectOpenAI(ctx context.Context, profile string) (string, error) {
	auth, err := a.getOpenAI(profile)
	if err != nil {
		return "", err
	}
	auth.mu.Lock()
	defer auth.mu.Unlock()
	var warning string
	if auth.Account.Tokens.Refresh != "" {
		err = revokeOpenAI(ctx, auth.Account)
		if err != nil {
			warning = "Disconnected locally, but remote revocation was not confirmed. You can disconnect 1loop in ChatGPT Settings. " + err.Error()
		}
	}
	auth.Account.Tokens = openAITokens{}
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
