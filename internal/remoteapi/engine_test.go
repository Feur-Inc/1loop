// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeEngine pairs with the shared book, as both engines do.
type fakeEngine struct {
	book   PairingBook
	tokens Tokens
	events *Events
}

func (engine *fakeEngine) Info() map[string]any    { return map[string]any{"name": "fake"} }
func (engine *fakeEngine) Allows(hash string) bool { return engine.tokens.Allows(hash) }
func (engine *fakeEngine) Events() *Events         { return engine.events }
func (engine *fakeEngine) Routes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{"GET /api/state": func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{}`)) }}
}
func (engine *fakeEngine) RequestPairing(r *http.Request, args PairRequest) (map[string]any, error) {
	pending, err := engine.book.Request(args.Device, ClientAddress(r))
	if err != nil {
		return nil, err
	}
	return map[string]any{"pairing": pending.ID, "code": pending.Code}, nil
}
func (engine *fakeEngine) ConfirmPairing(_ *http.Request, args PairConfirm) (map[string]any, error) {
	paired, err := engine.book.Confirm(args.Pairing, args.Code, args.Device)
	if err != nil {
		return nil, err
	}
	if paired.Device == "Full disk" {
		return nil, StatusError{Status: 500, Err: errors.New("Unable to save paired device")}
	}
	engine.tokens.Set([]PairedDevice{{Name: paired.Device, TokenHash: TokenHash("token")}})
	return map[string]any{"token": "token"}, nil
}

// The shared layer: info is public, pairing maps the book's errors to the
// statuses the phone app expects, and every other route needs a token.
func TestNewMux(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &fakeEngine{events: NewEvents(ctx)}
	engine.book.Approval = true
	routes := Harden(NewMux(engine, true))
	call := func(method, path, token string, body any) (int, map[string]any, http.Header) {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		out := map[string]any{}
		json.Unmarshal(recorder.Body.Bytes(), &out)
		return recorder.Code, out, recorder.Header()
	}
	if code, info, header := call("GET", "/api/info", "", nil); code != 200 || info["name"] != "fake" || header.Get("Cache-Control") != "no-store" {
		t.Fatalf("info: %d %v %v", code, info, header)
	}
	for _, path := range []string{"/api/state", "/api/events"} {
		if code, _, _ := call("GET", path, "", nil); code != 401 {
			t.Fatalf("%s without a token: %d", path, code)
		}
	}
	if code, _, _ := call("POST", "/api/pair", "", "not an object"); code != 400 {
		t.Fatalf("bad body: %d", code)
	}
	code, reply, _ := call("POST", "/api/pair", "", PairRequest{Device: "Pixel"})
	if code != 200 {
		t.Fatalf("pair: %d %v", code, reply)
	}
	id, shown := reply["pairing"].(string), reply["code"].(string)
	if code, _, _ := call("POST", "/api/pair", "", PairRequest{Device: "Pixel"}); code != 429 {
		t.Fatalf("pair again at once: %d", code)
	}
	if code, _, _ := call("POST", "/api/pair/confirm", "", PairConfirm{Pairing: id, Code: shown}); code != 409 {
		t.Fatalf("confirm before approval: %d", code)
	}
	if code, _, _ := call("POST", "/api/pair/confirm", "", PairConfirm{Pairing: id, Code: "nope"}); code != 403 {
		t.Fatalf("wrong code: %d", code)
	}
	engine.book.Approve(id, shown)
	if code, reply, _ := call("POST", "/api/pair/confirm", "", PairConfirm{Pairing: id, Code: shown}); code != 200 || reply["token"] != "token" {
		t.Fatalf("confirm: %d %v", code, reply)
	}
	if code, _, _ := call("GET", "/api/state", "token", nil); code != 200 {
		t.Fatalf("state with the token: %d", code)
	}
	engine.book.Clear()
	pending, _ := engine.book.Request("Full disk", "192.0.2.50")
	engine.book.Approve(pending.ID, pending.Code)
	if code, _, _ := call("POST", "/api/pair/confirm", "", PairConfirm{Pairing: pending.ID, Code: pending.Code}); code != 500 {
		t.Fatalf("a StatusError keeps its status: %d", code)
	}
}

// One event stream for both engines: it starts with a comment, carries
// broadcasts, and ends when the devices are disconnected.
func TestEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &fakeEngine{events: NewEvents(ctx)}
	engine.tokens.Set([]PairedDevice{{TokenHash: TokenHash("token")}})
	server := httptest.NewServer(NewMux(engine, false))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("stream headers: %v", resp.Header)
	}
	lines := bufio.NewReader(resp.Body)
	if first, _ := lines.ReadString('\n'); first != ": 1loop\n" {
		t.Fatalf("first line %q", first)
	}
	for deadline := time.Now().Add(5 * time.Second); !engine.events.Listening(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stream never subscribed")
		}
	}
	revoked, release := engine.events.Stream()
	defer release()
	engine.events.Broadcast("chats", map[string]int{"n": 1})
	var got []string
	for len(got) < 2 {
		line, err := lines.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
			got = append(got, line)
		}
	}
	if got[0] != "event: chats" || got[1] != `data: {"n":1}` {
		t.Fatalf("event: %v", got)
	}
	engine.events.Disconnect()
	select {
	case <-revoked:
	case <-time.After(5 * time.Second):
		t.Fatal("a browser stream outlived Disconnect")
	}
	if _, err := lines.ReadString('\n'); err == nil {
		if _, err := lines.ReadString('\n'); err == nil {
			t.Fatal("the event stream outlived Disconnect")
		}
	}
}
