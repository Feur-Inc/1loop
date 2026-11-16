// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"fyne.io/fyne/v2"
)

// Forgetting a server unpairs this device there, then forgets it here;
// the other paired servers stay (#428).
func TestForgetServerUnpairsThisDevice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var mu sync.Mutex
	paired := map[string]string{"token-me": "aaaa", "token-phone": "bbbb"} // token → device ID
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		token := r.Header.Get("Authorization")[len("Bearer "):]
		if _, ok := paired[token]; !ok {
			http.Error(w, `{"error":"not paired"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/devices":
			devices := []serverDevice{}
			for each, id := range paired {
				devices = append(devices, serverDevice{ID: id, Name: id, Current: each == token})
			}
			json.NewEncoder(w).Encode(map[string]any{"devices": devices})
		case r.Method == "POST" && r.URL.Path == "/api/devices/aaaa/revoke":
			delete(paired, "token-me")
			w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	pin := sha256.Sum256(server.Certificate().Raw)
	me := serverAddress{URL: server.URL, Fingerprint: hex.EncodeToString(pin[:]), Token: "token-me"}
	other := serverAddress{URL: "https://other.lan:47101", Token: "o"}
	saveServerAddress(other)
	saveServerAddress(me)

	if err := unpairSelf(context.Background(), me); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	_, still := paired["token-me"]
	_, phone := paired["token-phone"]
	mu.Unlock()
	if still || !phone {
		t.Fatalf("after forgetting: this device paired %v, the phone paired %v", still, phone)
	}
	if err := unpairSelf(context.Background(), me); err == nil {
		t.Fatal("the server still answers this device")
	}

	ui := sidebarUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // the links dropServer starts end with the test
	ui.ctx = ctx
	fyne.DoAndWait(func() { ui.dropServer(me.URL) })
	if got := loadServerAddresses(); !slices.Equal(got, []serverAddress{other}) {
		t.Fatalf("paired servers %v, want only %v", got, other)
	}
}
