// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// settingsUI is a window with its settings built, a project of this
// computer open.
func settingsUI(t *testing.T) *desktopUI {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	test.NewTempApp(t)
	ui, _ := twoWindows(t)
	ui.windows = ui.windows[:1]
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ui.ctx = ctx
	ui.window = test.NewWindow(nil)
	ui.window.SetContent(ui.build())
	ui.window.Resize(fyne.NewSize(1400, 820))
	ui.cfg.Workspace = "/home/u/app"
	return ui
}

// texts are the words an object shows, in order.
func texts(object fyne.CanvasObject) []string {
	var found []string
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		if object == nil || !object.Visible() {
			return
		}
		switch shown := object.(type) {
		case *flowText:
			var text strings.Builder
			for _, piece := range shown.spans {
				text.WriteString(piece.text)
			}
			found = append(found, text.String())
		case *tapRow:
			found = append(found, shown.text, shown.trailing)
		case *widget.Label:
			found = append(found, shown.Text)
		case *widget.Entry:
			found = append(found, shown.Text, shown.PlaceHolder)
		case *canvas.Text:
			found = append(found, shown.Text)
		case *fyne.Container:
			for _, child := range shown.Objects {
				walk(child)
			}
		case fyne.Widget:
			if renderer := test.WidgetRenderer(shown); renderer != nil {
				for _, child := range renderer.Objects() {
					walk(child)
				}
			}
		}
	}
	walk(object)
	return found
}

// shownPage is what the settings show now.
func shownPage(view *settingsView) []string {
	return texts(view.column)
}

// No page of this computer's settings changes with the project open: a
// server's project shows them as a local one does (#639).
func TestLocalSettingsIgnoreTheOpenProject(t *testing.T) {
	ui := settingsUI(t)
	saveServerAddress(serverAddress{URL: "https://dietpi:47101", Token: "t"})
	ui.linkServers() // paired: its group is under Servers either way
	// Updates is left out: its line follows the release page's check.
	pages := []string{"General", "Models & accounts", "Agent & approvals", "Tools & extensions", "Advanced", "Extensions", "Connectors"}
	shown := map[string][]string{}
	ui.settings()
	for _, page := range pages {
		ui.prefs.showNow(page)
		shown[page] = shownPage(ui.prefs)
	}
	ui.prefs.closeNow()

	// A server's project open, as useServer leaves the window.
	ctx, cancel := context.WithCancel(ui.ctx)
	t.Cleanup(cancel)
	client, _ := newRemoteClient(serverAddress{URL: "https://dietpi:47101", Token: "t"})
	ui.localCfg, ui.cfg = ui.cfg, agent.Config{Workspace: "/srv/dietpi"}
	ui.server = &remoteDesktop{parent: ui, client: client, ctx: ctx, cancel: cancel, window: ui.window, status: ui.serverStatus,
		wake: make(chan struct{}, 1), convs: map[string]*conversation{}, shown: true, name: "dietpi"}
	ui.settings()
	for _, page := range pages {
		ui.prefs.showNow(page)
		if got := shownPage(ui.prefs); !slices.Equal(got, shown[page]) {
			t.Errorf("%s with a server's project open:\n%q\nwith this computer's:\n%q", page, got, shown[page])
		}
		if ui.prefs.server != "" {
			t.Errorf("%s is shown as a page of %s", page, ui.prefs.server)
		}
	}
}

// settingsServerAPI is a paired server answering what its settings pages
// ask.
func settingsServerAPI(t *testing.T, name string) (*httptest.Server, serverAddress) {
	t.Helper()
	server, address := fakeServer(t, name, "/srv/"+name, "A chat")
	handler := server.Config.Handler
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/connections":
			json.NewEncoder(w).Encode(map[string]any{"Connections": []serverConnection{
				{Connection: agent.Connection{ID: "c1", Name: "Server OpenRouter", Provider: "openrouter", Enabled: true}, HasKey: true}}})
		case "/api/devices":
			json.NewEncoder(w).Encode(map[string]any{"devices": []serverDevice{{ID: "d1", Name: "1loop desktop", Current: true}}})
		default:
			handler.ServeHTTP(w, r)
		}
	})
	return server, address
}

// until waits for a condition read on the interface's thread.
func until(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ok bool
		fyne.DoAndWait(func() { ok = done() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(texts []string, part string) bool {
	return slices.ContainsFunc(texts, func(text string) bool { return strings.Contains(text, part) })
}

// serverNavTexts are the rows of the Servers group.
func serverNavTexts(view *settingsView) []string { return texts(view.serverNav) }

// A paired server the window does not work on has its own group under
// Servers: its pages open from there, name it, and read the server through
// a connection of the settings' own, closed when Settings leave it.
func TestServerPagesFromTheServersGroup(t *testing.T) {
	ui := settingsUI(t)
	_, alpha := settingsServerAPI(t, "alpha")
	saveServerAddress(alpha)
	ui.linkServers()
	label := serverName(alpha.URL)
	fyne.DoAndWait(func() { ui.settings() })
	until(t, "the server's row", func() bool { return contains(serverNavTexts(ui.prefs), label) })

	var header *tapRow
	fyne.DoAndWait(func() {
		for _, object := range ui.prefs.serverNav.Objects {
			if row, ok := object.(*tapRow); ok && row.text == label {
				header = row
			}
		}
	})
	if header == nil {
		t.Fatal("no row for the server under Servers")
	}
	fyne.DoAndWait(func() { header.onTap() })
	connections := serverPageID(alpha.URL, "Connections")
	until(t, "the server's connections", func() bool { return contains(shownPage(ui.prefs), "Server OpenRouter") })
	fyne.DoAndWait(func() {
		page := shownPage(ui.prefs)
		if ui.prefs.page != connections || !contains(page, "Connections · on "+label) || !contains(page, "Used by chats on "+label) {
			t.Errorf("page %q shows %q", ui.prefs.page, page)
		}
		if row := ui.prefs.rows[connections]; row == nil || !row.selected {
			t.Error("the server's Connections is not selected in the sidebar")
		}
		for _, name := range []string{"Chat names", "Agent", "Extensions", "Installs", "Devices", "Updates"} {
			if !contains(serverNavTexts(ui.prefs), name) {
				t.Errorf("%s is not among the server's pages", name)
			}
		}
		if ui.server != nil {
			t.Error("opening a server's settings made it the window's target")
		}
	})

	devices := serverPageID(alpha.URL, "Devices")
	fyne.DoAndWait(func() { ui.prefs.show(devices) })
	until(t, "the server's devices", func() bool { return contains(shownPage(ui.prefs), "1loop desktop · this device") })

	var entry *settingsServer
	fyne.DoAndWait(func() {
		entry = ui.prefs.servers[alpha.URL]
		ui.prefs.show("General")
	})
	fyne.DoAndWait(func() {
		if entry == nil || entry.remote != nil {
			t.Error("the settings' connection to the server stays open on this computer's page")
		}
		if contains(serverNavTexts(ui.prefs), "Devices") {
			t.Error("the server's pages stay listed while this computer's are shown")
		}
	})
}

// A server that does not answer says so on its pages, with Reconnect,
// instead of an empty page.
func TestOfflineServerPageExplains(t *testing.T) {
	ui := settingsUI(t)
	stopped, beta := settingsServerAPI(t, "beta")
	stopped.Close()
	saveServerAddress(beta)
	ui.linkServers()
	label := serverName(beta.URL)
	fyne.DoAndWait(func() {
		ui.settings()
		ui.prefs.show(serverPageID(beta.URL, "Connections"))
	})
	until(t, "the offline explanation", func() bool { return contains(shownPage(ui.prefs), label+" doesn't answer") })
	fyne.DoAndWait(func() {
		page := shownPage(ui.prefs)
		if !contains(page, "Reconnect") || !contains(page, "Offline") || !contains(page, "Connections · on "+label) {
			t.Errorf("the offline page shows %q", page)
		}
		if !contains(serverNavTexts(ui.prefs), "○ offline") {
			t.Errorf("the sidebar shows %q", serverNavTexts(ui.prefs))
		}
	})
}

// The search finds a server's settings too, under its name.
func TestSearchFindsServerSettings(t *testing.T) {
	ui := settingsUI(t)
	_, alpha := settingsServerAPI(t, "alpha")
	saveServerAddress(alpha)
	ui.linkServers()
	label := serverName(alpha.URL)
	var found []serverMatch
	fyne.DoAndWait(func() {
		ui.settings()
		found = ui.prefs.findSettings("paired devices")
	})
	i := slices.IndexFunc(found, func(match serverMatch) bool { return match.address == alpha.URL && match.page == "Devices" })
	if i < 0 {
		t.Fatalf("the search for paired devices found %v", found)
	}
	fyne.DoAndWait(func() {
		ui.prefs.find("paired devices")
		if page := shownPage(ui.prefs); !contains(page, "Servers › "+label+" › Devices") {
			t.Errorf("the results show %q", page)
		}
		ui.prefs.openMatch(found[i])
		if ui.prefs.page != serverPageID(alpha.URL, "Devices") {
			t.Errorf("the result opened %q", ui.prefs.page)
		}
	})
}
