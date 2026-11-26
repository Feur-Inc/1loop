// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"net/http"
	"strings"
	"time"
)

// The limits and headers every phone API shares, the desktop's and a
// headless server's alike (#518).
const (
	MaxBody       = 2 << 20  // a request's JSON
	MaxExportBody = 64 << 20 // the list of a large project's files (/api/sync/export)
)

// Protect applies the shared limits and headers to a request before its
// route runs: its body is capped (file uploads set their own limits),
// responses are never sniffed, and the API's are never cached.
func Protect(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/attachments", "/api/files/upload", "/api/sync/import", "/api/loopy/file":
		// Files: their handlers set their own limits.
	case "/api/sync/export":
		r.Body = http.MaxBytesReader(w, r.Body, MaxExportBody)
	default:
		r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store") // an event stream says no-cache itself
	}
}

// Harden is next behind Protect.
func Harden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Protect(w, r)
		next.ServeHTTP(w, r)
	})
}

// NewServer is an http.Server for a phone API, with the shared timeouts
// and header limit; the caller adds TLS and logging.
func NewServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
}
