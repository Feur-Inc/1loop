// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"1loop/internal/remoteapi"
)

// TestRevokePairedDevice: a paired device lists the others and unpairs one;
// the revoked token is refused on its next request and its event stream
// ends, and a device can unpair itself when it forgets the server (#428).
func TestRevokePairedDevice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	const desktop, phone = "desktop-token-0123456789", "phone-token-0123456789"
	server.mu.Lock()
	server.devices = []remoteapi.PairedDevice{
		{Name: "1loop desktop", TokenHash: remoteapi.TokenHash(desktop), Added: time.Now()},
		{Name: "Pixel", TokenHash: remoteapi.TokenHash(phone), Added: time.Now()},
	}
	server.mu.Unlock()
	routes := httptest.NewServer(server.routes())
	t.Cleanup(routes.Close)
	call := func(method, path, token string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, routes.URL+path, bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		out := map[string]any{}
		json.NewDecoder(response.Body).Decode(&out)
		return response.StatusCode, out
	}

	// The phone's event stream is open when it is revoked.
	req, _ := http.NewRequest("GET", routes.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+phone)
	stream, err := http.DefaultClient.Do(req)
	if err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("events: %v %v", err, stream)
	}
	defer stream.Body.Close()
	bufio.NewReader(stream.Body).ReadString('\n') // the stream's first comment

	status, listed := call("GET", "/api/devices", desktop)
	if status != http.StatusOK {
		t.Fatalf("devices: %d %v", status, listed)
	}
	devices, _ := listed["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("devices listed: %v", listed)
	}
	var phoneID, desktopID string
	for _, entry := range devices {
		device := entry.(map[string]any)
		switch device["name"] {
		case "Pixel":
			phoneID = device["id"].(string)
			if device["current"] == true {
				t.Fatal("the phone is marked as the device asking")
			}
		case "1loop desktop":
			desktopID = device["id"].(string)
			if device["current"] != true {
				t.Fatal("the desktop is not marked as the device asking")
			}
		}
		if _, leaked := device["tokenHash"]; leaked {
			t.Fatal("the list carries the token's hash")
		}
	}

	if status, _ := call("POST", "/api/devices/"+phoneID+"/revoke", desktop); status != http.StatusOK {
		t.Fatalf("revoke: %d", status)
	}
	if status, _ := call("GET", "/api/state", phone); status != http.StatusUnauthorized {
		t.Fatalf("the revoked phone still reaches the server: %d", status)
	}
	if status, _ := call("GET", "/api/devices", phone); status != http.StatusUnauthorized {
		t.Fatalf("the revoked phone still lists the devices: %d", status)
	}
	ended := make(chan struct{})
	go func() { io.Copy(io.Discard, stream.Body); close(ended) }()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the revoked phone's event stream stays open")
	}
	if status, _ := call("GET", "/api/state", desktop); status != http.StatusOK {
		t.Fatalf("the desktop lost its pairing: %d", status)
	}
	if status, _ := call("POST", "/api/devices/"+phoneID+"/revoke", desktop); status != http.StatusNotFound {
		t.Fatalf("revoked twice: %d", status)
	}

	// Forgetting the server unpairs the desktop itself.
	if status, _ := call("POST", "/api/devices/"+desktopID+"/revoke", desktop); status != http.StatusOK {
		t.Fatalf("self revoke: %d", status)
	}
	if status, _ := call("GET", "/api/state", desktop); status != http.StatusUnauthorized {
		t.Fatalf("the desktop still reaches the server: %d", status)
	}
	server.mu.Lock()
	left := len(server.devices)
	server.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d devices left", left)
	}
}
