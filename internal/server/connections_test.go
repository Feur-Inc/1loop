// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"1loop/internal/core"
	"1loop/internal/extensions"
	"1loop/internal/remoteapi"
)

// pairedServer is a headless server with one paired device, and a call
// with its token (or none).
func pairedServer(t *testing.T) (*headlessServer, func(method, path string, body any, paired bool) (int, string)) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	token := core.Identifier()
	server.devices = append(server.devices, remoteapi.PairedDevice{Name: "Browser", TokenHash: remoteapi.TokenHash(token), Added: time.Now()})
	routes := server.routes()
	return server, func(method, path string, body any, paired bool) (int, string) {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		if paired {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		return recorder.Code, recorder.Body.String()
	}
}

// A paired device adds an OpenAI-compatible account: its key stays on the
// server, which reads the models with it and offers them in the picker.
func TestPairedConnections(t *testing.T) {
	const key = "sk-test-kept-on-the-server"
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"test-model-a"},{"id":"test-model-b"}]}`))
	}))
	defer service.Close()
	server, call := pairedServer(t)

	if code, _ := call("GET", "/api/connections", nil, false); code != http.StatusUnauthorized {
		t.Fatalf("unpaired list: %d", code)
	}
	if code, body := call("POST", "/api/connections", map[string]any{"connection": map[string]any{"name": "ChatGPT", "provider": "openai"}}, true); code != http.StatusBadRequest {
		t.Fatalf("a ChatGPT sign-in from another device: %d %s", code, body)
	}
	draft := map[string]any{"name": "Test service", "provider": "compatible", "baseURL": service.URL + "/v1", "enabled": true, "allModels": true}
	code, body := call("POST", "/api/connections/models", map[string]any{"connection": draft, "apiKey": key}, true)
	if code != 200 || !strings.Contains(body, "test-model-a") {
		t.Fatalf("draft models: %d %s", code, body)
	}
	code, body = call("POST", "/api/connections", map[string]any{"connection": draft, "apiKey": key}, true)
	if code != 200 || strings.Contains(body, key) {
		t.Fatalf("save: %d %s", code, body)
	}
	var saved struct{ Connection struct{ ID string } }
	json.Unmarshal([]byte(body), &saved)
	if saved.Connection.ID == "" {
		t.Fatalf("saved without an ID: %s", body)
	}

	code, body = call("GET", "/api/connections", nil, true)
	if code != 200 || strings.Contains(body, key) || !strings.Contains(body, `"hasKey":true`) {
		t.Fatalf("list: %d %s", code, body)
	}
	// The stored key reads the catalog, of the saved account and of its draft.
	if code, body := call("GET", "/api/connections/"+saved.Connection.ID+"/models", nil, true); code != 200 || !strings.Contains(body, "test-model-b") {
		t.Fatalf("models: %d %s", code, body)
	}
	draft["id"] = saved.Connection.ID
	if code, body := call("POST", "/api/connections/models", map[string]any{"connection": draft}, true); code != 200 || !strings.Contains(body, "test-model-b") {
		t.Fatalf("draft models with the stored key: %d %s", code, body)
	}

	// The picker: the server's state lists the account's models.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, state := call("GET", "/api/state", nil, true)
		if strings.Contains(state, "test-model-a") && strings.Contains(state, saved.Connection.ID) {
			if strings.Contains(state, key) {
				t.Fatal("the state shows the key")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("models not offered: %s", state)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Switched off, then removed.
	draft["enabled"] = false
	if code, body := call("POST", "/api/connections", map[string]any{"connection": draft}, true); code != 200 {
		t.Fatalf("disable: %d %s", code, body)
	}
	server.mu.Lock()
	kept, enabled := server.keys[saved.Connection.ID], server.connections[len(server.connections)-1].Enabled
	server.mu.Unlock()
	if kept != key || enabled {
		t.Fatalf("after disabling: key kept %v, enabled %v", kept == key, enabled)
	}
	if code, body := call("POST", "/api/connections", map[string]any{"connection": draft, "remove": true}, true); code != 200 {
		t.Fatalf("remove: %d %s", code, body)
	}
	if _, body := call("GET", "/api/connections", nil, true); strings.Contains(body, saved.Connection.ID) {
		t.Fatalf("still listed: %s", body)
	}
}

// MCP servers: their environment and header values are hidden in the
// answers, and a hidden value saved back keeps the stored one.
func TestPairedMCPHidesValues(t *testing.T) {
	const secret = "ghp_test-secret-token"
	_, call := pairedServer(t)
	server := map[string]any{"name": "docs", "type": "stdio", "command": "true", "env": map[string]string{"TOKEN": secret, "EMPTY": ""}, "enabled": false}
	code, body := call("POST", "/api/extensions/mcp", map[string]any{"server": server}, true)
	if code != 200 || strings.Contains(body, secret) || !strings.Contains(body, extensions.Hidden) {
		t.Fatalf("add: %d %s", code, body)
	}
	if code, body := call("GET", "/api/extensions", nil, true); code != 200 || strings.Contains(body, secret) || !strings.Contains(body, `"docs"`) {
		t.Fatalf("list: %d %s", code, body)
	}
	server["env"] = map[string]string{"TOKEN": extensions.Hidden, "OTHER": "x"}
	if code, body := call("POST", "/api/extensions/mcp", map[string]any{"server": server, "previous": "docs"}, true); code != 200 {
		t.Fatalf("save back: %d %s", code, body)
	}
	registry, err := extensions.Load()
	if err != nil || len(registry.MCP) != 1 {
		t.Fatalf("registry: %v %v", registry.MCP, err)
	}
	if env := registry.MCP[0].Env; env["TOKEN"] != secret || env["OTHER"] != "x" {
		t.Fatalf("stored environment: %v", env)
	}
}
