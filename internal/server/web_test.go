// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// With -web-dir, / sends a browser to the web client at /app/.
func TestWebClientRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	server.webDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(server.webDir, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	routes := server.routes()
	for path, want := range map[string]int{"/": http.StatusFound, "/app/": http.StatusOK, "/nothing": http.StatusNotFound} {
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != want {
			t.Errorf("GET %s: %d, want %d", path, recorder.Code, want)
		}
		if location := recorder.Header().Get("Location"); path == "/" && location != "/app/" {
			t.Errorf("GET /: Location %q, want /app/", location)
		}
	}
}
