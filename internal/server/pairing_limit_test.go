// SPDX-License-Identifier: MPL-2.0

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"1loop/internal/remoteapi"
)

// One client cannot hold every pending pairing: past pairingsPerAddress its
// new request replaces its own oldest one, and the other devices' stay (#532).
func TestPairingLimitedPerAddress(t *testing.T) {
	server, _, _ := testServer(t)
	at := time.Now()
	server.pairing.Now = func() time.Time { return at }
	routes := server.routes()
	pair := func(address string) int {
		at = at.Add(4 * time.Second) // past the shared policy's interval between a client's requests
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/pair", strings.NewReader(`{"device":"Phone"}`))
		req.RemoteAddr = address
		routes.ServeHTTP(recorder, req)
		return recorder.Code
	}
	pending := func(address string) int {
		n := 0
		for _, pair := range server.pairing.Waiting() {
			if pair.Client == address {
				n++
			}
		}
		return n
	}
	if code := pair("192.0.2.8:4000"); code != 200 {
		t.Fatalf("the phone: %d", code)
	}
	for i := 0; i < 20; i++ { // a device flooding requests
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
	// Faster than the policy allows, a client is told to wait.
	at = at.Add(-4 * time.Second)
	if code := pair("192.0.2.9:4000"); code != 429 {
		t.Fatalf("a second request at once: %d", code)
	}
}

// The headless server and the desktop share the pairing policy: wrong codes
// typed by devices that are not paired lock pairing until the console
// unlocks it.
func TestPairingLockAndUnlock(t *testing.T) {
	server, _, _ := testServer(t)
	at := time.Now()
	server.pairing.Now = func() time.Time { return at }
	routes := server.routes()
	call := func(path, body string) (int, string) {
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return recorder.Code, recorder.Body.String()
	}
	for failures := 0; failures < remoteapi.MaxFailedCodes; {
		at = at.Add(4 * time.Second)
		code, reply := call("/api/pair", `{"device":"Guess"}`)
		if code != 200 {
			t.Fatalf("pair after %d wrong codes: %d %s", failures, code, reply)
		}
		id := reply[strings.Index(reply, `"pairing":"`)+11:]
		id = id[:strings.Index(id, `"`)]
		for try := 0; try < remoteapi.TriesPerPairing && failures < remoteapi.MaxFailedCodes; try++ {
			call("/api/pair/confirm", `{"pairing":"`+id+`","code":"x"}`)
			failures++
		}
	}
	at = at.Add(4 * time.Second)
	if code, _ := call("/api/pair", `{"device":"Phone"}`); code != 429 {
		t.Fatalf("pairing while locked: %d", code)
	}
	var out strings.Builder
	if !server.command([]string{"unlock"}, &out) {
		t.Fatalf("unlock: %s", out.String())
	}
	if code, _ := call("/api/pair", `{"device":"Phone"}`); code != 200 {
		t.Fatalf("pairing after unlock: %d", code)
	}
}
