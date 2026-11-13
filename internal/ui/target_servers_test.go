// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/remoteapi"
)

// fakeServer is a paired 1loop server that lists a project and a chat.
func fakeServer(t *testing.T, name, project, chat string) (*httptest.Server, serverAddress) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-"+name {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/state":
			json.NewEncoder(w).Encode(map[string]any{"Name": name, "Workspace": project,
				"Chats": []remoteapi.Chat{{ID: name + "-1", Title: chat, Workspace: project}}})
		case "/api/projects":
			json.NewEncoder(w).Encode(map[string]any{"Projects": []string{project}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	pin := sha256.Sum256(server.Certificate().Raw)
	return server, serverAddress{URL: server.URL, Fingerprint: hex.EncodeToString(pin[:]), Token: "token-" + name}
}

// Each paired server is listed as its own target, with its own projects,
// chats and offline state: here two, one of them stopped (#383).
func TestSeveralPairedServersListed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, alpha := fakeServer(t, "alpha", "/srv/alpha", "Alpha chat")
	stopped, beta := fakeServer(t, "beta", "/srv/beta", "Beta chat")
	stopped.Close()
	for _, address := range []serverAddress{alpha, beta} {
		if err := saveServerAddress(address); err != nil {
			t.Fatal(err)
		}
	}
	ui := sidebarUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ui.ctx = ctx
	ui.serverProjects = map[string][]string{beta.URL: {"/srv/beta"}} // as it listed them the last time
	ui.linkServers()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var alphaReach, betaReach reach
		fyne.DoAndWait(func() {
			alphaReach = ui.reaches[serverTarget(alpha.URL).key()].state
			betaReach = ui.reaches[serverTarget(beta.URL).key()].state
		})
		if alphaReach == reachOnline && betaReach == reachOffline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alpha %v, beta %v: want online and offline", alphaReach, betaReach)
		}
		time.Sleep(20 * time.Millisecond)
	}
	fyne.DoAndWait(func() {
		var keys []string
		for _, group := range ui.sidebarGroups() {
			keys = append(keys, group.target.key())
		}
		want := []string{"local", "ssh:m1", serverTarget(alpha.URL).key(), serverTarget(beta.URL).key()}
		if !slices.Equal(keys, want) {
			t.Errorf("groups %q, want %q", keys, want)
		}
		ui.renderChats()
		wantListed(t, ui, "app", "site", "alpha", "Alpha chat", "beta")
		if row := headerRow(t, ui, "beta"); row.trailing != offlineMark || row.mark != iconBrowser {
			t.Errorf("the stopped server's project: trailing %q, mark %v", row.trailing, row.mark)
		}
		if row := headerRow(t, ui, "alpha"); row.trailing == offlineMark || row.mark != iconBrowser {
			t.Errorf("the answering server's project: trailing %q, mark %v", row.trailing, row.mark)
		}
		// Both are remembered for the next start, not only the last one read.
		if len(ui.serverProjects) != 2 || !slices.Equal(ui.serverProjects[alpha.URL], []string{"/srv/alpha"}) {
			t.Errorf("remembered %v", ui.serverProjects)
		}
	})
}

// Pairing a server keeps the others paired; the file keeps the last one
// at its top, where an older 1loop reads its one server.
func TestPairingKeepsTheOtherServers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := serverAddress{URL: "https://old.lan:47101", Token: "o"}
	data, _ := json.Marshal(old) // as written before #383
	os.MkdirAll(filepath.Dir(remoteAddressPath()), 0700)
	if err := os.WriteFile(remoteAddressPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadServerAddresses(); !slices.Equal(got, []serverAddress{old}) {
		t.Fatalf("an older file pairs %v", got)
	}
	added := serverAddress{URL: testServer, Token: "t"}
	saveServerAddress(added)
	again := serverAddress{URL: old.URL, Token: "o2"} // paired again: in its place
	saveServerAddress(again)
	if got := loadServerAddresses(); !slices.Equal(got, []serverAddress{again, added}) {
		t.Fatalf("paired %v", got)
	}
	if got := loadServerAddress(); got != again {
		t.Fatalf("last paired %v, want %v", got, again)
	}
	var older serverAddress // what a version before #383 reads
	data, _ = os.ReadFile(remoteAddressPath())
	json.Unmarshal(data, &older)
	if older != again {
		t.Fatalf("an older version reads %v", older)
	}
}
