// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// A server that can sign a ChatGPT account in offers the type; ChatGPT
// (Web) is never offered for a server.
func TestServerConnectionTypes(t *testing.T) {
	if types := serverConnectionTypes(true); !agent.Contains(types, "ChatGPT account") || agent.Contains(types, "ChatGPT (Web)") {
		t.Fatalf("types: %v", types)
	}
	if types := serverConnectionTypes(false); agent.Contains(types, "ChatGPT account") {
		t.Fatalf("an older server's types: %v", types)
	}
}

// freeCallback has the sign-in wait on a free port instead of 1455.
func freeCallback(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	saved := chatGPTCallback
	chatGPTCallback = address
	t.Cleanup(func() { chatGPTCallback = saved })
	return address
}

func relayRemote(t *testing.T, handler http.Handler) *remoteDesktop {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &remoteDesktop{ctx: context.Background(), name: "box", client: &remoteClient{http: server.Client(), address: serverAddress{URL: server.URL, Token: "t"}}}
}

// The browser comes back to this computer, which hands the code to the
// server; a wrong state is turned away and the sign-in keeps waiting.
func TestRelayChatGPTSignIn(t *testing.T) {
	callback := freeCallback(t)
	var completed map[string]string
	remote := relayRemote(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/connections/c1/login":
			json.NewEncoder(w).Encode(map[string]string{"url": "https://auth.openai.com/oauth/authorize?state=s1", "state": "s1"})
		case "/api/connections/c1/login/complete":
			json.NewDecoder(r.Body).Decode(&completed)
			json.NewEncoder(w).Encode(map[string]any{"signedIn": true, "email": "a@example.com"})
		default:
			http.NotFound(w, r)
		}
	}))
	saved := openURL
	defer func() { openURL = saved }()
	browser := make(chan []int, 1)
	openURL = func(target *url.URL) error {
		go func() {
			var codes []int
			for _, query := range []string{"state=wrong&code=c", "state=s1&code=the-code"} {
				response, err := http.Get("http://" + callback + "/auth/callback?" + query)
				if err != nil {
					codes = append(codes, 0)
					continue
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				codes = append(codes, response.StatusCode)
			}
			browser <- codes
		}()
		return nil
	}
	var shown string
	email, err := remote.relayChatGPTSignIn(context.Background(), "c1", func(link string) { shown = link })
	if err != nil || email != "a@example.com" {
		t.Fatalf("sign-in: %q %v", email, err)
	}
	if codes := <-browser; codes[0] != http.StatusBadRequest || codes[1] != 200 {
		t.Fatalf("browser pages: %v", codes)
	}
	if completed["state"] != "s1" || completed["code"] != "the-code" || !strings.HasPrefix(shown, "https://auth.openai.com/") {
		t.Fatalf("handed to the server: %v, shown %q", completed, shown)
	}
}

// An older server has no sign-in route; a link elsewhere than OpenAI's is
// not opened; a sign-in already waiting on this computer keeps the port.
func TestRelayChatGPTSignInRefused(t *testing.T) {
	saved := openURL
	defer func() { openURL = saved }()
	openURL = func(*url.URL) error { return nil }
	old := relayRemote(t, http.NotFoundHandler())
	if _, err := old.relayChatGPTSignIn(context.Background(), "c1", func(string) {}); !errors.Is(err, errOldServer) {
		t.Fatalf("older server: %v", err)
	}
	notFound := relayRemote(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Connection not found"})
	}))
	if _, err := notFound.relayChatGPTSignIn(context.Background(), "c1", func(string) {}); err == nil || errors.Is(err, errOldServer) {
		t.Fatalf("a removed connection: %v", err)
	}
	elsewhere := relayRemote(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"url": "https://evil.example/authorize", "state": "s1"})
	}))
	if _, err := elsewhere.relayChatGPTSignIn(context.Background(), "c1", func(string) {}); err == nil {
		t.Fatal("opened a link elsewhere")
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	savedCallback := chatGPTCallback
	chatGPTCallback = busy.Addr().String()
	defer func() { chatGPTCallback = savedCallback }()
	remote := relayRemote(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"url": "https://auth.openai.com/oauth/authorize?state=s1", "state": "s1"})
	}))
	if _, err := remote.relayChatGPTSignIn(context.Background(), "c1", func(string) {}); err == nil || !strings.Contains(err.Error(), "1455") {
		t.Fatalf("port in use: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	busy.Close()
	if _, err := remote.relayChatGPTSignIn(ctx, "c1", func(string) { cancel() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// hasLabel tells whether a label with this text is among the objects.
func hasLabel(object fyne.CanvasObject, text string) bool {
	switch object := object.(type) {
	case *widget.Label:
		return object.Text == text
	case *fyne.Container:
		for _, child := range object.Objects {
			if hasLabel(child, text) {
				return true
			}
		}
	}
	return false
}

// The Access card: who is signed in on the server and the sign-in, or,
// on an older server, that it needs an update.
func TestChatGPTSignInFields(t *testing.T) {
	ui := sidebarUI(t)
	ui.window = test.NewWindow(nil)
	t.Cleanup(ui.window.Close)
	draft := agent.Connection{ID: "c1", Name: "ChatGPT", Provider: "openai"}
	name := widget.NewEntry()
	remote := &remoteDesktop{parent: ui, ctx: context.Background(), chatGPTSignIn: true}

	fields, _ := remote.chatGPTSignInFields(&draft, serverConnection{Connection: draft, SignedIn: true, Email: "a@example.com"}, name, func(string) {}, nil)
	login, _ := findButton(fields, fyne.Position{}, "Continue with ChatGPT")
	signOut, _ := findButton(fields, fyne.Position{}, "Sign out")
	if account, _ := findFlow(fields, fyne.Position{}, "a@example.com"); account == nil || login == nil || !login.Visible() || signOut == nil || !signOut.Visible() {
		t.Fatal("a signed-in account: no email, sign-in or sign-out")
	}

	remote.chatGPTSignIn = false
	fields, _ = remote.chatGPTSignInFields(&draft, serverConnection{Connection: draft}, name, func(string) {}, nil)
	login, _ = findButton(fields, fyne.Position{}, "Continue with ChatGPT")
	signOut, _ = findButton(fields, fyne.Position{}, "Sign out")
	if login.Visible() || signOut.Visible() || !hasLabel(fields, errOldServer.Error()+".") {
		t.Fatal("an older server: the sign-in is offered, or no word of the update")
	}
	if account, _ := findFlow(fields, fyne.Position{}, "Not signed in on the server."); account == nil {
		t.Fatal("not signed in: not said")
	}
}
