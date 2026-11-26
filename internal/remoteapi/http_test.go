// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Both phone APIs cap bodies and set the same headers (#518).
func TestHarden(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	routes := Harden(read)
	post := func(path string, size int) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, httptest.NewRequest("POST", path, bytes.NewReader(make([]byte, size))))
		return recorder
	}
	for _, c := range []struct {
		path string
		size int
		want int
	}{
		{"/api/send", MaxBody, 200},
		{"/api/send", MaxBody + 1, 413},
		{"/api/sync/export", MaxBody + 1, 200},
		{"/api/sync/export", MaxExportBody + 1, 413},
		{"/api/files/upload", MaxBody + 1, 200}, // its handler sets its own limit
	} {
		if got := post(c.path, c.size).Code; got != c.want {
			t.Errorf("%s with %d bytes: %d, want %d", c.path, c.size, got, c.want)
		}
	}
	recorder := post("/api/state", 0)
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("API headers: %v", recorder.Header())
	}
	recorder = post("/app/index.html", 0)
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Cache-Control") != "" {
		t.Fatalf("a web page is cached as usual: %v", recorder.Header())
	}
	server := NewServer(routes)
	if server.IdleTimeout == 0 || server.MaxHeaderBytes == 0 || server.ReadHeaderTimeout == 0 {
		t.Fatalf("server limits: %+v", server)
	}
}
