// SPDX-License-Identifier: MPL-2.0

package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"1loop/internal/agent"
)

// A paired desktop signs a ChatGPT account in on the server: the server
// gives the link, the desktop hands back the code OpenAI returned to it,
// and the tokens stay on the server.
func TestPairedChatGPTSignIn(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("code") != "good-code" {
			http.Error(w, "bad grant", http.StatusBadRequest)
			return
		}
		encode := func(v any) string {
			data, _ := json.Marshal(v)
			return base64.RawURLEncoding.EncodeToString(data)
		}
		signed := encode(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + encode(map[string]any{
			"iss": "https://auth.openai.com", "sub": "user-1", "aud": "app_EMoamEEZ73f0CkXaXp7hrann",
			"email": "a@example.com", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()})
		hash := sha256.Sum256([]byte(signed))
		signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		// No refresh token: signing out then revokes nothing at OpenAI.
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access-kept-on-the-server",
			"id_token": signed + "." + base64.RawURLEncoding.EncodeToString(signature), "token_type": "Bearer", "expires_in": 3600})
	}))
	defer openAI.Close()
	defer agent.StubOpenAISignIn(openAI.URL, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}})()
	_, call := pairedServer(t)

	if _, body := call("GET", "/api/connections", nil, true); !strings.Contains(body, `"chatGPTSignIn":true`) {
		t.Fatalf("sign-in not offered: %s", body)
	}
	code, body := call("POST", "/api/connections", map[string]any{"connection": map[string]any{"name": "ChatGPT", "provider": "openai", "enabled": true, "allModels": true}}, true)
	var saved struct{ Connection struct{ ID string } }
	json.Unmarshal([]byte(body), &saved)
	if code != 200 || saved.Connection.ID == "" {
		t.Fatalf("save: %d %s", code, body)
	}
	other := map[string]any{"name": "Other", "provider": "compatible", "baseURL": "http://127.0.0.1:1/v1", "enabled": false}
	_, body = call("POST", "/api/connections", map[string]any{"connection": other}, true)
	var compatible struct{ Connection struct{ ID string } }
	json.Unmarshal([]byte(body), &compatible)
	login := "/api/connections/" + saved.Connection.ID + "/login"

	if code, _ := call("POST", login, nil, false); code != http.StatusUnauthorized {
		t.Fatalf("unpaired begin: %d", code)
	}
	if code, _ := call("POST", login+"/complete", map[string]string{"state": "x", "code": "good-code"}, false); code != http.StatusUnauthorized {
		t.Fatalf("unpaired complete: %d", code)
	}
	if code, body := call("POST", "/api/connections/"+compatible.Connection.ID+"/login", nil, true); code != http.StatusBadRequest {
		t.Fatalf("another provider's sign-in: %d %s", code, body)
	}
	code, body = call("POST", login, nil, true)
	var begun struct{ URL, State string }
	json.Unmarshal([]byte(body), &begun)
	link, _ := url.Parse(begun.URL)
	if code != 200 || begun.State == "" || link.Query().Get("state") != begun.State || link.Query().Get("redirect_uri") != "http://localhost:1455/auth/callback" {
		t.Fatalf("begin: %d %s", code, body)
	}
	if code, body := call("POST", login+"/complete", map[string]string{"state": "wrong", "code": "good-code"}, true); code != http.StatusBadRequest {
		t.Fatalf("wrong state: %d %s", code, body)
	}
	code, body = call("POST", login+"/complete", map[string]string{"state": begun.State, "code": "good-code"}, true)
	if code != 200 || !strings.Contains(body, "a@example.com") || strings.Contains(body, "access-kept") {
		t.Fatalf("complete: %d %s", code, body)
	}
	_, body = call("GET", "/api/connections", nil, true)
	if !strings.Contains(body, `"signedIn":true`) || !strings.Contains(body, "a@example.com") || strings.Contains(body, "access-kept") {
		t.Fatalf("list after sign-in: %s", body)
	}
	if code, body := call("POST", "/api/connections/"+saved.Connection.ID+"/disconnect", nil, true); code != 200 {
		t.Fatalf("disconnect: %d %s", code, body)
	}
	if _, body := call("GET", "/api/connections", nil, true); !strings.Contains(body, `"signedIn":false`) {
		t.Fatalf("list after signing out: %s", body)
	}
}
