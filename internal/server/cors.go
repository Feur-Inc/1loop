// SPDX-License-Identifier: MPL-2.0

package server

// Another 1loop server's browser client may link this server (#382): its
// page, served from that other server, calls this API from its own
// origin. The API is reached with the paired device's bearer token, never
// a cookie, so a page of another site gets nothing it could not get with
// the token anyway; a pairing asked from another page names that page, so
// the device that approves it sees where it comes from.

import (
	"net/http"
	"net/url"
	"strings"
)

// crossOrigin answers for the API to a page of another HTTPS origin: the
// headers that let it read the reply, and the preflight on its own. It
// says whether the request is answered.
func crossOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := foreignOrigin(r)
	if origin == "" || !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Add("Vary", "Origin")
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
		header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		header.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// foreignOrigin is the HTTPS origin of the page that sent the request when
// it is not this server's own, "" otherwise.
func foreignOrigin(r *http.Request) string {
	origin := r.Header.Get("Origin")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || strings.EqualFold(parsed.Host, r.Host) {
		return ""
	}
	return origin
}
