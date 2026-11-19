// SPDX-License-Identifier: MPL-2.0

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// One client cannot hold every pending pairing: past pairingsPerAddress its
// new request replaces its own oldest one, and the other devices' stay (#532).
func TestPairingLimitedPerAddress(t *testing.T) {
	server, _, _ := testServer(t)
	routes := server.routes()
	pair := func(address string) int {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/pair", strings.NewReader(`{"device":"Phone"}`))
		req.RemoteAddr = address
		routes.ServeHTTP(recorder, req)
		return recorder.Code
	}
	pending := func(address string) int {
		server.mu.Lock()
		defer server.mu.Unlock()
		n := 0
		for _, pair := range server.pairs {
			if pair.Address == address {
				n++
			}
		}
		return n
	}
	if code := pair("192.0.2.8:4000"); code != 200 {
		t.Fatalf("the phone: %d", code)
	}
	for i := 0; i < maxPendingPairings; i++ { // a device flooding requests
		if code := pair("192.0.2.7:4000"); code != 200 {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	if pending("192.0.2.7") != pairingsPerAddress || pending("192.0.2.8") != 1 {
		t.Fatalf("pending: flooder %d, phone %d", pending("192.0.2.7"), pending("192.0.2.8"))
	}
	if code := pair("192.0.2.9:4000"); code != 200 {
		t.Fatalf("another device after the flood: %d", code)
	}
}
