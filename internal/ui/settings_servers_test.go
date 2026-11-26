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
	"1loop/internal/update"
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
		if !contains(page, "Reconnect") || !contains(page, "Offline") || !contains(page, "Connections · on "+label) ||
			!contains(page, "Connection refused: nothing answers at "+label) || contains(page, "dial tcp") {
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

// serverRowOf is a server's row under Servers.
func serverRowOf(view *settingsView, label string) *tapRow {
	for _, object := range view.serverNav.Objects {
		if row, ok := object.(*tapRow); ok && row.text == label {
			return row
		}
	}
	return nil
}

// In a small window, the settings' sidebar scrolls to a server's last
// pages and the servers after it, and keeps the entry shown in view.
func TestSettingsSidebarScrolls(t *testing.T) {
	ui := settingsUI(t)
	ui.window.Resize(fyne.NewSize(1180, 480))
	_, alpha := settingsServerAPI(t, "alpha")
	stopped, beta := settingsServerAPI(t, "beta")
	stopped.Close()
	saveServerAddress(alpha)
	saveServerAddress(beta)
	ui.linkServers()
	updates := serverPageID(alpha.URL, "Updates")
	fyne.DoAndWait(func() {
		ui.settings()
		ui.prefs.show(updates)
	})
	fyne.DoAndWait(func() {
		scroll := ui.prefs.sideScroll
		if scroll.Content.MinSize().Height <= scroll.Size().Height {
			t.Fatalf("the sidebar's entries (%v high) fit its %v: nothing to scroll", scroll.Content.MinSize().Height, scroll.Size().Height)
		}
		row := ui.prefs.rows[updates]
		at, ok := offsetIn(scroll.Content, row)
		if !ok || at.Y < scroll.Offset.Y || at.Y+row.Size().Height > scroll.Offset.Y+scroll.Size().Height {
			t.Errorf("the selected Updates entry at %v is out of the sidebar's view (%v, %v high)", at.Y, scroll.Offset.Y, scroll.Size().Height)
		}
		last := serverRowOf(ui.prefs, ui.serverLabel(beta.URL))
		if last == nil {
			t.Fatal("the second server is not listed")
		}
		end, _ := offsetIn(scroll.Content, last)
		if end.Y+last.Size().Height > scroll.Content.Size().Height {
			t.Errorf("the second server's row ends at %v, past the scrolled content's %v", end.Y+last.Size().Height, scroll.Content.Size().Height)
		}
	})
}

// The open server's row folds its pages, and unfolds them; the page shown
// stays, and the server's row says where you are meanwhile.
func TestOpenServerFolds(t *testing.T) {
	ui := settingsUI(t)
	_, alpha := settingsServerAPI(t, "alpha")
	saveServerAddress(alpha)
	ui.linkServers()
	connections := serverPageID(alpha.URL, "Connections")
	label := ui.serverLabel(alpha.URL)
	fyne.DoAndWait(func() {
		ui.settings()
		ui.prefs.show(connections)
		serverRowOf(ui.prefs, label).onTap()
	})
	fyne.DoAndWait(func() {
		header := serverRowOf(ui.prefs, label)
		if contains(serverNavTexts(ui.prefs), "Devices") || ui.prefs.page != connections || !header.selected || header.icon != iconChevronRight {
			t.Errorf("folded: sidebar %q, page %q, row selected %v", serverNavTexts(ui.prefs), ui.prefs.page, header.selected)
		}
		header.onTap()
		if !contains(serverNavTexts(ui.prefs), "Devices") || ui.prefs.rows[connections] == nil || !ui.prefs.rows[connections].selected {
			t.Errorf("unfolded: sidebar %q", serverNavTexts(ui.prefs))
		}
	})
}

// Two servers on one host are told apart by their ports; one alone keeps
// its host's name.
func TestServerLabelsTellServersApart(t *testing.T) {
	ui := sidebarUI(t)
	ui.links = []*serverLink{{address: serverAddress{URL: "https://127.0.0.1:47101"}}, {address: serverAddress{URL: "https://127.0.0.1:47102"}},
		{address: serverAddress{URL: "https://dietpi:47101"}}}
	for address, want := range map[string]string{"https://127.0.0.1:47101": "127.0.0.1:47101", "https://127.0.0.1:47102": "127.0.0.1:47102", "https://dietpi:47101": "dietpi"} {
		if got := ui.serverLabel(address); got != want {
			t.Errorf("serverLabel(%s) = %q, want %q", address, got, want)
		}
	}
}

// A dev build is not behind the release page: only an older release is.
func TestDevBuildIsNotOutdated(t *testing.T) {
	for _, c := range []struct {
		status update.Status
		behind bool
	}{
		{update.Status{Current: "dev", Latest: "0.3.2", Available: true}, false},
		{update.Status{Current: "0.3.1", Latest: "0.3.2", Available: true}, true},
		{update.Status{Current: "0.3.2", Latest: "0.3.2"}, false},
	} {
		if got := releaseBehind(&c.status); got != c.behind {
			t.Errorf("%s with %s out: behind %v, want %v", c.status.Current, c.status.Latest, got, c.behind)
		}
	}
	if word, _ := serverWord(reachOnline); word != "connected" {
		t.Errorf("a server that answers is %q", word)
	}
}

// The wheel over the settings' sidebar, sent through the window's canvas
// as the driver does, scrolls it to the servers below the window's bottom:
// in the window size of a small laptop screen.
func TestSettingsSidebarTakesTheWheel(t *testing.T) {
	ui := settingsUI(t)
	ui.window.Resize(fyne.NewSize(1180, 746))
	_, alpha := settingsServerAPI(t, "alpha")
	stopped, beta := settingsServerAPI(t, "beta")
	stopped.Close()
	saveServerAddress(alpha)
	saveServerAddress(beta)
	ui.linkServers()
	fyne.DoAndWait(func() {
		ui.settings()
		ui.prefs.show(serverPageID(alpha.URL, "Connections"))
	})
	until(t, "the server's page", func() bool { return contains(shownPage(ui.prefs), "Server OpenRouter") })
	fyne.DoAndWait(func() {
		scroll := ui.prefs.sideScroll
		driver := fyne.CurrentApp().Driver()
		at := driver.AbsolutePositionForObject(scroll)
		point := at.Add(fyne.NewPos(scroll.Size().Width/2, scroll.Size().Height*3/4))
		found := scrollableAt(ui.window.Canvas().Content(), fyne.Position{}, point)
		t.Logf("scroll at %v size %v, content size %v min %v; the wheel at %v goes to %T", at, scroll.Size(), scroll.Content.Size(), scroll.Content.MinSize(), point, found)
		for range 20 {
			test.Scroll(ui.window.Canvas(), point, 0, -30)
		}
		if found != fyne.Scrollable(scroll) {
			t.Errorf("the wheel over the sidebar goes to %T, not its scroll", found)
		}
		last := serverRowOf(ui.prefs, ui.serverLabel(beta.URL))
		end, _ := offsetIn(scroll.Content, last)
		if scroll.Offset.Y == 0 || end.Y+last.Size().Height > scroll.Offset.Y+scroll.Size().Height+1 {
			t.Errorf("after the wheel: offset %v, the second server's row ends at %v, the view at %v", scroll.Offset.Y, end.Y+last.Size().Height, scroll.Offset.Y+scroll.Size().Height)
		}

		// Over the title, above the scroll: the column takes the wheel.
		title := at.Add(fyne.NewPos(scroll.Size().Width/2, -60))
		if got := scrollableAt(ui.window.Canvas().Content(), fyne.Position{}, title); got != fyne.Scrollable(ui.prefs.side.(*sideColumn)) {
			t.Errorf("the wheel over the sidebar's title goes to %T", got)
		}
		for range 20 {
			test.Scroll(ui.window.Canvas(), title, 0, 30)
		}
		if scroll.Offset.Y != 0 {
			t.Errorf("the wheel over the title left the sidebar at %v", scroll.Offset.Y)
		}
	})
}

// A server's state beside its name stays short; why one does not answer
// is said in words.
func TestServerStateInWords(t *testing.T) {
	for state, want := range map[reach]string{reachOnline: "●", reachUnknown: "◌", reachOffline: "○ offline"} {
		word, _ := serverWord(state)
		if got := serverMark(state, word); got != want {
			t.Errorf("state %v reads %q, want %q", state, got, want)
		}
	}
	for raw, want := range map[string]string{
		`Get "https://127.0.0.1:47102/api/state": dial tcp 127.0.0.1:47102: connect: connection refused`:        "Connection refused: nothing answers at 127.0.0.1:47102.",
		`Get "https://nas:47101/api/state": dial tcp: lookup nas: no such host`:                                 "nas:47101 can't be found on the network.",
		`Get "https://nas:47101/api/state": context deadline exceeded (Client.Timeout exceeded while awaiting)`: "No answer from nas:47101 in time.",
		`Server certificate fingerprint changed; verify it on the server console`:                               "Server certificate fingerprint changed; verify it on the server console",
	} {
		address := "https://" + strings.Split(strings.Split(strings.TrimPrefix(raw, `Get "https://`), "/")[0], `"`)[0]
		if !strings.HasPrefix(raw, "Get") {
			address = "https://nas:47101"
		}
		if got := plainProblem(address, raw); got != want {
			t.Errorf("%s\nreads %q, want %q", raw, got, want)
		}
	}
}

// scrollableAt is the scroll the driver gives a wheel at a point: the last
// visible one under it, in the order the tree is drawn.
func scrollableAt(object fyne.CanvasObject, origin, point fyne.Position) (found fyne.Scrollable) {
	if object == nil || !object.Visible() {
		return nil
	}
	at := origin.Add(object.Position())
	size := object.Size()
	if point.X < at.X || point.Y < at.Y || point.X >= at.X+size.Width || point.Y >= at.Y+size.Height {
		return nil
	}
	if scrollable, ok := object.(fyne.Scrollable); ok {
		found = scrollable
	}
	var children []fyne.CanvasObject
	switch shown := object.(type) {
	case *fyne.Container:
		children = shown.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(shown).Objects()
	}
	for _, child := range children {
		if inner := scrollableAt(child, at, point); inner != nil {
			found = inner
		}
	}
	return found
}
