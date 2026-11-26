// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// lendServer is a server without Chromium, with a paired device whose
// token lends browsers.
func lendServer(t *testing.T) (*headlessServer, string, string) {
	t.Helper()
	t.Setenv("ONELOOP_BROWSER", "none")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	token := core.Identifier()
	server.devices = append(server.devices, remoteapi.PairedDevice{Name: "Laptop", TokenHash: remoteapi.TokenHash(token)})
	web := httptest.NewServer(server.routes())
	t.Cleanup(web.Close)
	return server, web.URL, token
}

// fakeBrowser answers every DevTools command with its method, and tells
// which ones it got.
func fakeBrowser(t *testing.T) (func(context.Context) (string, func(), error), chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	received := make(chan string, 16)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := ws.Upgrade(conn); err != nil {
					return
				}
				for {
					data, err := wsutil.ReadClientText(conn)
					if err != nil {
						return
					}
					var command struct {
						ID     int64
						Method string
					}
					json.Unmarshal(data, &command)
					received <- command.Method
					answer, _ := json.Marshal(map[string]any{"id": command.ID, "result": map[string]string{"method": command.Method}})
					wsutil.WriteServerText(conn, answer)
				}
			}()
		}
	}()
	return func(context.Context) (string, func(), error) {
		return "ws://" + listener.Addr().String() + "/devtools/browser/fake", func() {}, nil
	}, received
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func lentState(t *testing.T, address, token string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", address+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state map[string]any
	json.NewDecoder(response.Body).Decode(&state)
	return state
}

// Frames go both ways between the engine's socket and the client's
// browser; what would reach the client's files is answered with an error
// there; when the client leaves, the engine's socket closes.
func TestLentBrowserRelay(t *testing.T) {
	server, address, token := lendServer(t)
	launch, received := fakeBrowser(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	used := make(chan bool, 8)
	ended := make(chan error, 1)
	go func() {
		ended <- browser.Lending{URL: address, Token: token, Launch: launch, OnUse: func(on bool) { used <- on }}.Run(ctx)
	}()
	waitUntil(t, "the client to attach", func() bool { return server.lenders.current() != nil })
	if state := lentState(t, address, token); state["browser"] != true || state["ownBrowser"] != false || state["lentBrowser"].(map[string]any)["device"] != "Laptop" {
		t.Fatalf("state with a lent browser: %v", state)
	}

	engineAddress, err := server.lenders.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if on := <-used; !on {
		t.Fatal("the client did not say its browser is used")
	}
	conn, _, _, err := ws.Dial(ctx, engineAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	wsutil.WriteClientText(conn, []byte(`{"id":1,"method":"Runtime.evaluate","params":{"expression":"1"}}`))
	reply, err := wsutil.ReadServerText(conn)
	if err != nil || !strings.Contains(string(reply), `"id":1`) || !strings.Contains(string(reply), "Runtime.evaluate") {
		t.Fatalf("reply: %s %v", reply, err)
	}
	if method := <-received; method != "Runtime.evaluate" {
		t.Fatalf("the browser got %q", method)
	}
	if state := lentState(t, address, token); state["lentBrowser"].(map[string]any)["inUse"] != true {
		t.Fatalf("state while used: %v", state)
	}

	for _, command := range []string{
		`{"id":2,"method":"Page.navigate","params":{"url":"file:///etc/passwd"}}`,
		`{"id":2,"method":"DOM.setFileInputFiles","params":{"files":["/etc/passwd"]}}`,
		`{"id":2,"method":"Browser.setDownloadBehavior","params":{"behavior":"allow","downloadPath":"/tmp"}}`,
	} {
		wsutil.WriteClientText(conn, []byte(command))
		reply, err = wsutil.ReadServerText(conn)
		if err != nil || !strings.Contains(string(reply), `"error"`) || !strings.Contains(string(reply), `"id":2`) {
			t.Fatalf("%s: %s %v", command, reply, err)
		}
	}
	select {
	case method := <-received:
		t.Fatalf("a refused command reached the browser: %s", method)
	default:
	}

	cancel() // the client goes away
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := wsutil.ReadServerText(conn); err == nil {
		t.Fatal("the engine's socket stayed open")
	}
	<-ended
	waitUntil(t, "the lender to leave", func() bool { return server.lenders.current() == nil })
	if state := lentState(t, address, token); state["browser"] != false || state["lentBrowser"] != nil {
		t.Fatalf("state after the client left: %v", state)
	}
	if _, err := server.lenders.Connect(context.Background()); err == nil || err.Error() != browser.NoBrowserProblem {
		t.Fatalf("connect without a lender: %v", err)
	}
}

// Only a paired device lends its browser: not an unknown token, nor the
// phone app's local one. A server from before lending says so.
func TestLentBrowserNeedsPairedDevice(t *testing.T) {
	server, address, token := lendServer(t)
	local := core.Identifier()
	server.mu.Lock()
	server.localHash = remoteapi.TokenHash(local)
	server.mu.Unlock()
	launch, _ := fakeBrowser(t)
	for _, bad := range []string{"", "wrong", local} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := browser.Lending{URL: address, Token: bad, Launch: launch}.Run(ctx)
		cancel()
		var status ws.StatusError
		if !errors.As(err, &status) || int(status) != http.StatusUnauthorized {
			t.Fatalf("token %q: %v", bad, err)
		}
	}
	if server.lenders.current() != nil {
		t.Fatal("a refused client lends its browser")
	}

	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	if err := (browser.Lending{URL: old.URL, Token: token, Launch: launch}).Run(context.Background()); !errors.Is(err, browser.ErrLendUnsupported) {
		t.Fatalf("old server: %v", err)
	}
}

// The browser tools run in the lent browser, with a local Chromium's
// results, and fail saying why when its client leaves in the middle of a
// call; with no client, they give the same error as before.
func TestLentBrowserRunsTools(t *testing.T) {
	if _, err := exec.LookPath("chromium"); err != nil {
		if _, err := exec.LookPath("google-chrome"); err != nil {
			t.Skip("no Chromium to lend here")
		}
	}
	server, address, token := lendServer(t)
	chat := &serverChat{ID: "c1", Title: "t", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	server.mu.Lock()
	server.chats = []*serverChat{chat}
	server.mu.Unlock()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Lent</title><button id=b onclick="document.title='clicked'">Press me</button>`)
	}))
	defer site.Close()
	tools := server.browserTools(chat)
	run := func(name, args string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		result, err := tools(ctx, name, json.RawMessage(args))
		return result.Text, err
	}
	if _, err := run("browser_tabs", `{"action":"open","url":"`+site.URL+`"}`); err == nil || err.Error() != browser.NoBrowserProblem {
		t.Fatalf("without a lender: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- browser.Lending{URL: address, Token: token}.Run(ctx) }()
	waitUntil(t, "the client to attach", func() bool { return server.lenders.current() != nil })

	if text, err := run("browser_tabs", `{"action":"open","url":"`+site.URL+`"}`); err != nil || !strings.Contains(text, "Lent") {
		t.Fatalf("open: %s %v", text, err)
	}
	if text, err := run("browser_read", `{"action":"snapshot"}`); err != nil || !strings.Contains(text, "Press me") {
		t.Fatalf("snapshot: %s %v", text, err)
	}
	if _, err := run("browser_interact", `{"action":"click","selector":"#b"}`); err != nil {
		t.Fatal(err)
	}
	if text, err := run("browser_evaluate", `{"expression":"document.title + ' ' + (1+1)"}`); err != nil || text != `"clicked 2"` {
		t.Fatalf("evaluate: %s %v", text, err)
	}
	if _, err := run("browser_navigate", `{"action":"navigate","url":"/etc/passwd"}`); err == nil {
		t.Fatal("a lent browser opened a local file")
	}

	go func() { time.Sleep(time.Second); cancel() }() // the client leaves during the call
	_, err := run("browser_evaluate", `{"expression":"new Promise(() => {})"}`)
	if err == nil || !strings.Contains(err.Error(), "went away during this call") || !strings.Contains(err.Error(), "Laptop") {
		t.Fatalf("call whose lender left: %v", err)
	}
	<-ended
	waitUntil(t, "the lender to leave", func() bool { return server.lenders.current() == nil })
	if _, err := run("browser_read", `{"action":"snapshot"}`); err == nil || err.Error() != browser.NoBrowserProblem {
		t.Fatalf("after the lender left: %v", err)
	}

	// Another client lends its browser: the tabs open again in it.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go browser.Lending{URL: address, Token: token}.Run(ctx)
	waitUntil(t, "the client to attach again", func() bool { return server.lenders.current() != nil })
	if text, err := run("browser_read", `{"action":"snapshot"}`); err != nil || !strings.Contains(text, "Press me") {
		t.Fatalf("snapshot in the next lent browser: %s %v", text, err)
	}
}

// An idle lending socket stays open: the pong that answers the client's
// ping long after the last message gets a deadline of its own.
func TestLentBrowserIdlePing(t *testing.T) {
	server, address, token := lendServer(t)
	saved := lendWrite
	lendWrite = 200 * time.Millisecond
	t.Cleanup(func() { lendWrite = saved })
	dialer := ws.Dialer{Header: ws.HandshakeHeaderHTTP(http.Header{"Authorization": {"Bearer " + token}})}
	conn, _, _, err := dialer.Dial(context.Background(), "ws"+strings.TrimPrefix(address, "http")+browser.LendPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waitUntil(t, "the client to attach", func() bool { return server.lenders.current() != nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.lenders.Connect(ctx) // a write from the server: its deadline passes below
	data, err := wsutil.ReadServerText(conn)
	if err != nil || !strings.Contains(string(data), `"open":true`) {
		t.Fatalf("open: %s %v", data, err)
	}
	time.Sleep(3 * lendWrite)
	if err := wsutil.WriteClientMessage(conn, ws.OpPing, nil); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	frame, err := ws.ReadFrame(conn)
	if err != nil || frame.Header.OpCode != ws.OpPong {
		t.Fatalf("answer to a ping on an idle socket: %v %v", frame.Header.OpCode, err)
	}
	time.Sleep(100 * time.Millisecond)
	if server.lenders.current() == nil {
		t.Fatal("the client was dropped after an idle minute")
	}
}

// A lent browser that could not start fails the call saying why; the next
// call tries again, in a new one.
func TestLentBrowserStartsAgain(t *testing.T) {
	if _, err := exec.LookPath("chromium"); err != nil {
		if _, err := exec.LookPath("google-chrome"); err != nil {
			t.Skip("no Chromium to lend here")
		}
	}
	server, address, token := lendServer(t)
	chat := &serverChat{ID: "c1", Title: "t", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	server.mu.Lock()
	server.chats = []*serverChat{chat}
	server.mu.Unlock()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Again</title>`)
	}))
	defer site.Close()
	failed := false
	launch := func(ctx context.Context) (string, func(), error) {
		if !failed {
			failed = true
			return "", nil, errors.New("the profile is locked")
		}
		return browser.LaunchLent(ctx)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go browser.Lending{URL: address, Token: token, Launch: launch}.Run(ctx)
	waitUntil(t, "the client to attach", func() bool { return server.lenders.current() != nil })
	tools := server.browserTools(chat)
	run := func(name, args string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		result, err := tools(ctx, name, json.RawMessage(args))
		return result.Text, err
	}
	if _, err := run("browser_tabs", `{"action":"open","url":"`+site.URL+`"}`); err == nil || !strings.Contains(err.Error(), "the profile is locked") {
		t.Fatalf("first call: %v", err)
	}
	if text, err := run("browser_evaluate", `{"expression":"document.title"}`); err != nil || text != `"Again"` {
		t.Fatalf("evaluate: %s %v", text, err)
	}
}
