// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPairingWithoutConsole: the first device pairs with the setup link, the
// next ones are approved from a paired device (issue #331).
func TestPairingWithoutConsole(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	server.webDir, server.setupPort = t.TempDir(), 47101
	server.mu.Lock()
	server.offerSetupLocked()
	setup := server.setup
	server.mu.Unlock()
	if setup == "" {
		t.Fatal("no setup link while no device is paired")
	}
	routes := server.routes()
	call := func(method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		out := map[string]any{}
		json.Unmarshal(recorder.Body.Bytes(), &out)
		return recorder.Code, out
	}
	pair := func(device, setup string) (id, code string, approved bool) {
		t.Helper()
		status, reply := call("POST", "/api/pair", "", map[string]string{"device": device, "setup": setup})
		if status != http.StatusOK {
			t.Fatalf("pair %s: %d %v", device, status, reply)
		}
		return reply["pairing"].(string), reply["code"].(string), reply["approved"] == true
	}
	confirm := func(id, code string) (int, string) {
		t.Helper()
		status, reply := call("POST", "/api/pair/confirm", "", map[string]string{"pairing": id, "code": code})
		token, _ := reply["token"].(string)
		return status, token
	}

	// A wrong secret is an ordinary pairing; the right one is approved at once.
	if _, _, approved := pair("Guess", "nope"); approved {
		t.Fatal("approved with a wrong setup secret")
	}
	id, code, approved := pair("1loop web", setup)
	if !approved {
		t.Fatal("the setup link did not approve the pairing")
	}
	status, browser := confirm(id, code)
	if status != http.StatusOK || browser == "" {
		t.Fatalf("confirm with the setup link: %d", status)
	}
	if _, _, approved := pair("Again", setup); approved {
		t.Fatal("the setup link worked twice")
	}

	// The next device waits; a paired device sees it and approves it.
	id, code, approved = pair("Phone", "")
	if approved {
		t.Fatal("a pairing without the setup link was approved")
	}
	if status, _ := confirm(id, code); status != http.StatusConflict {
		t.Fatalf("confirmed before approval: %d", status)
	}
	if status, _ := call("GET", "/api/pairings", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("pairings without a token: %d", status)
	}
	_, listed := call("GET", "/api/pairings", browser, nil)
	found := false
	for _, pending := range listed["pending"].([]any) {
		found = found || pending.(map[string]any)["id"] == id
	}
	if !found {
		t.Fatalf("the pairing is not listed: %v", listed)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	if status, _ := call("POST", "/api/pairings/"+id, browser, map[string]any{"code": wrong, "approve": true}); status != http.StatusForbidden {
		t.Fatalf("approved with the wrong code: %d", status)
	}
	if status, _ := call("POST", "/api/pairings/"+id, browser, map[string]any{"code": code, "approve": true}); status != http.StatusOK {
		t.Fatalf("approve: %d", status)
	}
	if status, phone := confirm(id, code); status != http.StatusOK || phone == "" {
		t.Fatalf("confirm after approval: %d", status)
	}

	// Refused: the device cannot finish.
	id, code, _ = pair("Stranger", "")
	if status, _ := call("POST", "/api/pairings/"+id, browser, map[string]any{"approve": false}); status != http.StatusOK {
		t.Fatalf("refuse: %d", status)
	}
	if status, _ := confirm(id, code); status != http.StatusForbidden {
		t.Fatalf("confirmed after a refusal: %d", status)
	}
}
