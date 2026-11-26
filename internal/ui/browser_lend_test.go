// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

// Lending the browser is off for every server until the user turns it on
// for one; the others, and which server was paired last, stay as they were.
func TestLendBrowserIsOptInPerServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first := serverAddress{URL: "https://one.lan:47101", Token: "a"}
	second := serverAddress{URL: "https://two.lan:47101", Token: "b"}
	saveServerAddress(first)
	saveServerAddress(second)
	for _, saved := range loadServerAddresses() {
		if saved.LendBrowser {
			t.Fatalf("%s lends the browser without being asked", saved.URL)
		}
	}
	if err := saveLendBrowser(first.URL, true); err != nil {
		t.Fatal(err)
	}
	one, _ := pairedServer(first.URL)
	two, _ := pairedServer(second.URL)
	if !one.LendBrowser || two.LendBrowser || loadServerAddress().URL != second.URL {
		t.Fatalf("after turning it on for one: %+v %+v, last paired %s", one, two, loadServerAddress().URL)
	}
	if err := saveLendBrowser("https://unknown.lan:47101", true); err == nil {
		t.Fatal("lending to a server that is not paired")
	}
	if decodePairings([]byte(`{"url":"https://old.lan:47101","token":"t"}`)).LendBrowser {
		t.Fatal("a record from before lending lends the browser")
	}

	sidebarUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fyne.DoAndWait(func() { lendBrowser(ctx, two, nil) })
	if lendings[second.URL] != nil {
		t.Fatal("a server not let lends the browser")
	}
}

// While a server's agent uses the browser, the bar says so; Stop ends it
// and turns the setting off.
func TestLendBarStops(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	address := serverAddress{URL: "https://127.0.0.1:1", Token: "a", LendBrowser: true}
	saveServerAddress(address)
	ui := sidebarUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ui.ctx = ctx
	stopped := false
	fyne.DoAndWait(func() {
		ui.buildLendBar()
		lendings[address.URL] = &lendSession{address: address, cancel: func() { stopped = true }, refs: 1, attached: true}
		showLending()
	})
	t.Cleanup(func() { fyne.DoAndWait(func() { delete(lendings, address.URL); delete(lendViews, ui) }) })
	if ui.lendBar.Visible() {
		t.Fatal("the bar shows while the agent does not use the browser")
	}
	fyne.DoAndWait(func() {
		lendings[address.URL].inUse = true
		showLending()
	})
	if !ui.lendBar.Visible() || !strings.Contains(ui.lendText.spans[0].text, "The server's agent is using this computer's browser (127.0.0.1)") {
		t.Fatalf("bar while used: shown %v, %q", ui.lendBar.Visible(), ui.lendText.spans[0].text)
	}
	fyne.DoAndWait(ui.stopLending)
	saved, _ := pairedServer(address.URL)
	if ui.lendBar.Visible() || !stopped || lendings[address.URL] != nil || saved.LendBrowser {
		t.Fatalf("after Stop: shown %v, stopped %v, setting %v", ui.lendBar.Visible(), stopped, saved.LendBrowser)
	}
}

// A server from before lending: the setting says to update it.
func TestLendBrowserToOldServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(old.Close)
	pin := sha256.Sum256(old.Certificate().Raw)
	address := serverAddress{URL: old.URL, Fingerprint: hex.EncodeToString(pin[:]), Token: "a", LendBrowser: true}
	saveServerAddress(address)
	client, err := newRemoteClient(address)
	if err != nil {
		t.Fatal(err)
	}
	sidebarUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fyne.DoAndWait(func() { lendBrowser(ctx, address, client) })
	deadline := time.Now().Add(10 * time.Second)
	for {
		var status string
		fyne.DoAndWait(func() { status = lendStatusText(address.URL) })
		if strings.Contains(status, "cannot use this computer's browser: update the server") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status: %q", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
}
