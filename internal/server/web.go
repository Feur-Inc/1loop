// SPDX-License-Identifier: MPL-2.0

package server

// The browser client (cmd/1loop-web, issue #267): the desktop's remote mode
// built for GOOS=js, served at /app/ from the same origin as the API, so its
// requests need no CORS and the token travels as with any other client.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// webClient serves the files of dir at /app/. A file's gzipped copy
// (name.gz), when there is one, goes to the browsers that accept it: the
// .wasm is tens of megabytes otherwise.
func webClient(dir string) http.Handler {
	files := http.StripPrefix("/app/", http.FileServer(http.Dir(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/app/")
		if name == "" {
			name = "index.html"
		}
		switch filepath.Ext(name) {
		case ".wasm":
			w.Header().Set("Content-Type", "application/wasm")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		w.Header().Set("Cache-Control", "no-cache") // revalidated: a new build replaces it
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && !strings.Contains(name, "..") {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)+".gz")); err == nil {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Vary", "Accept-Encoding")
				r.URL.Path = "/app/" + name + ".gz"
			}
		}
		files.ServeHTTP(w, r)
	})
}
