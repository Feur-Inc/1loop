// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// fakeOpenAI is OpenAI's token endpoint for one sign-in: it gives tokens
// for the code "good-code" when the verifier matches the link's challenge.
func fakeOpenAI(t *testing.T, subject string) (challenge *string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	challenge = new(string)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != *challenge ||
			r.Form.Get("redirect_uri") != openAICallback || r.Form.Get("client_id") != openAIClientID {
			http.Error(w, "bad grant", http.StatusBadRequest)
			return
		}
		id := signTestIDToken(t, key, map[string]any{
			"iss": openAIIssuer, "sub": subject, "aud": []string{openAIClientID},
			"email": "a@example.com", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		})
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "id_token": id, "token_type": "Bearer", "expires_in": 3600})
	}))
	t.Cleanup(service.Close)
	t.Cleanup(StubOpenAISignIn(service.URL, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}))
	return challenge
}

func linkQuery(t *testing.T, link string) url.Values {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query()
}

// A sign-in whose browser runs on another device: the link names the
// callback OpenAI accepts, and only its state completes it, once.
func TestOpenAIRelay(t *testing.T) {
	challenge := fakeOpenAI(t, "user-1")
	backend := &App{}
	ctx := context.Background()
	if err := backend.CompleteOpenAI(ctx, "abc", "anything", "good-code"); err == nil {
		t.Fatal("completed a sign-in that never began")
	}
	link, state, err := backend.BeginOpenAIRelay("abc")
	if err != nil {
		t.Fatal(err)
	}
	query := linkQuery(t, link)
	if query.Get("state") != state || query.Get("redirect_uri") != openAICallback || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("link: %s", link)
	}
	*challenge = query.Get("code_challenge")
	if err := backend.CompleteOpenAI(ctx, "abc", "wrong", "good-code"); err == nil {
		t.Fatal("wrong state accepted")
	}
	if err := backend.CompleteOpenAI(ctx, "abc", state, "good-code"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	auth, _ := backend.GetOpenAI("abc")
	if auth.Account.Tokens.Access != "access-1" || auth.Account.Email != "a@example.com" || auth.Account.Subject != "user-1" {
		t.Fatalf("account: %+v", auth.Account)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "1loop", "openai-abc.json")); err != nil {
		t.Fatalf("not saved: %v", err)
	}
	if err := backend.CompleteOpenAI(ctx, "abc", state, "good-code"); err == nil {
		t.Fatal("a sign-in completed twice")
	}

	// A bad code uses the sign-in up; an old one expires.
	_, state, _ = backend.BeginOpenAIRelay("abc")
	if err := backend.CompleteOpenAI(ctx, "abc", state, "bad-code"); err == nil {
		t.Fatal("bad code accepted")
	}
	_, state, _ = backend.BeginOpenAIRelay("abc")
	auth.Mu.Lock()
	auth.relay.expires = time.Now().Add(-time.Second)
	auth.Mu.Unlock()
	if err := backend.CompleteOpenAI(ctx, "abc", state, "good-code"); err == nil {
		t.Fatal("expired sign-in accepted")
	}
}

// Another account than the one the profile had is refused.
func TestOpenAIRelayOtherAccount(t *testing.T) {
	challenge := fakeOpenAI(t, "user-2")
	backend := &App{}
	auth, _ := backend.GetOpenAI("abc")
	auth.Account = openAIAccount{ClientID: openAIClientID, Subject: "user-1"}
	link, state, _ := backend.BeginOpenAIRelay("abc")
	*challenge = linkQuery(t, link).Get("code_challenge")
	if err := backend.CompleteOpenAI(context.Background(), "abc", state, "good-code"); err == nil || auth.Account.Tokens.Access != "" {
		t.Fatalf("other account: %v", err)
	}
}

// The sign-in on this computer goes through the same exchange.
func TestOpenAILocalCallback(t *testing.T) {
	if listener, err := net.Listen("tcp", "127.0.0.1:1455"); err != nil {
		t.Skip("port 1455 is busy")
	} else {
		listener.Close()
	}
	challenge := fakeOpenAI(t, "user-1")
	backend := &App{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	link, done, err := backend.BeginOpenAI(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	query := linkQuery(t, link)
	*challenge = query.Get("code_challenge")
	response, err := http.Get("http://127.0.0.1:1455/auth/callback?state=wrong&code=good-code")
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong state: %v %v", response, err)
	}
	response, err = http.Get("http://127.0.0.1:1455/auth/callback?" + url.Values{"state": {query.Get("state")}, "code": {"good-code"}}.Encode())
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("callback: %v %v", response, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	auth, _ := backend.GetOpenAI("abc")
	if auth.Account.Tokens.Access != "access-1" {
		t.Fatalf("account: %+v", auth.Account)
	}
}
