// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"1loop/internal/remoteapi"
)

// A phone's token is checked without waiting for the window's thread: no
// fyne app runs here, so a UI hop would never return (#518).
func TestRemoteAuthorizedWithoutUI(t *testing.T) {
	ui := &desktopUI{desktopApp: &desktopApp{ctx: context.Background()}}
	ui.setDevices([]remoteapi.PairedDevice{{Name: "Pixel", TokenHash: remoteapi.TokenHash("secret")}})
	server := &remoteServer{ui: ui}
	handler := remoteapi.Authorized(server, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	serve := func(token string) int {
		req := httptest.NewRequest("GET", "/api/state", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { handler(recorder, req); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the token check waited for the UI thread")
		}
		return recorder.Code
	}
	if code := serve("secret"); code != http.StatusNoContent {
		t.Fatalf("paired phone: %d", code)
	}
	if code := serve("other"); code != http.StatusUnauthorized {
		t.Fatalf("unknown token: %d", code)
	}
	ui.setDevices(nil) // forgotten in Settings
	if code := serve("secret"); code != http.StatusUnauthorized {
		t.Fatalf("forgotten phone: %d", code)
	}
}
