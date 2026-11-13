// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Another server's browser client may call the API from its page (#382):
// the preflight is answered and the reply readable from that origin, and a
// pairing it asks for names the page.
func TestCrossOriginAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	routes := server.routes()
	const page = "https://home.lan:47101"

	preflight := httptest.NewRequest("OPTIONS", "https://box.lan:47101/api/state", nil)
	preflight.Header.Set("Origin", page)
	preflight.Header.Set("Access-Control-Request-Method", "GET")
	preflight.Header.Set("Access-Control-Request-Headers", "authorization")
	recorder := httptest.NewRecorder()
	routes.ServeHTTP(recorder, preflight)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Access-Control-Allow-Origin") != page ||
		!strings.Contains(recorder.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight: %d %v", recorder.Code, recorder.Header())
	}

	pair := httptest.NewRequest("POST", "https://box.lan:47101/api/pair", bytes.NewReader([]byte(`{"device":"1loop web"}`)))
	pair.Header.Set("Origin", page)
	recorder = httptest.NewRecorder()
	routes.ServeHTTP(recorder, pair)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Access-Control-Allow-Origin") != page {
		t.Fatalf("pair: %d %v %s", recorder.Code, recorder.Header(), recorder.Body)
	}
	server.mu.Lock()
	pending := server.pendingPairsLocked()
	server.mu.Unlock()
	if len(pending) != 1 || pending[0]["device"] != "1loop web · "+page {
		t.Fatalf("pending %v", pending)
	}

	// Its own page, or a page that is not HTTPS, gets no such header.
	for _, origin := range []string{"https://box.lan:47101", "http://evil.lan", ""} {
		req := httptest.NewRequest("GET", "https://box.lan:47101/api/info", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q allowed: %q", origin, got)
		}
	}
}
