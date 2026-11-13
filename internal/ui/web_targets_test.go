// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"maps"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"1loop/internal/remoteapi"
)

const (
	shopServer = "https://shop.lan:47101"
	oldServer  = "https://old.lan:47101"
)

// browserUI is the browser client's sidebar (#382): the page's own server
// in use, a linked server that answered and one that does not, known by
// the projects it listed last.
func browserUI(t *testing.T) *desktopUI {
	t.Helper()
	test.NewTempApp(t)
	ui := &desktopUI{ctx: context.Background(), home: testServer,
		foldedProjects: map[string]bool{}, allChatsShown: map[string]bool{}, worktreesShown: map[string]bool{}}
	ui.sideGlide = newGlideLayout()
	ui.projectList = container.New(ui.sideGlide)
	ui.server = &remoteDesktop{parent: ui, client: &remoteClient{address: serverAddress{URL: testServer}}, name: "box",
		projects: []string{"/srv/api"}, chats: []remoteapi.Chat{{ID: "s1", Title: "Home chat", Workspace: "/srv/api"}}, convs: map[string]*conversation{}}
	ui.links = []*serverLink{
		{address: serverAddress{URL: testServer, Token: "a"}},
		{address: serverAddress{URL: shopServer, Token: "b"}, answered: true, listing: serverListing{name: "shop",
			projects: []string{"/srv/shop"}, chats: []remoteapi.Chat{{ID: "s2", Title: "Shop chat", Workspace: "/srv/shop"}}}},
		{address: serverAddress{URL: oldServer, Token: "c"}},
	}
	ui.serverProjects = map[string][]string{oldServer: {"/srv/legacy"}}
	ui.reaches = map[string]targetReach{
		serverTarget(shopServer).key(): {state: reachOnline},
		serverTarget(oldServer).key():  {state: reachOffline, problem: "Failed to fetch"},
	}
	return ui
}

// The browser client lists every linked server's projects and chats with
// its own server's; an offline one stays listed, marked so, and only the
// projects of another server than the page's show its icon.
func TestBrowserListsEveryLinkedServer(t *testing.T) {
	ui := browserUI(t)
	var keys []string
	for _, group := range ui.sidebarGroups() {
		keys = append(keys, group.target.key())
	}
	if want := []string{"server:" + testServer, "server:" + shopServer, "server:" + oldServer}; !slices.Equal(keys, want) {
		t.Fatalf("groups %q, want %q", keys, want)
	}
	ui.renderChats()
	wantListed(t, ui, "api", "Home chat", "shop", "Shop chat", "legacy")
	for name, want := range map[string]fyne.Resource{"api": nil, "shop": iconBrowser, "legacy": iconBrowser} {
		if got := headerRow(t, ui, name).mark; got != want {
			t.Errorf("%s: mark %v, want %v", name, got, want)
		}
	}
	if row := headerRow(t, ui, "legacy"); row.trailing != offlineMark {
		t.Errorf("the offline server's project shows %q, want %q", row.trailing, offlineMark)
	}
	if row := headerRow(t, ui, "shop"); row.trailing == offlineMark {
		t.Errorf("the linked server that answers is marked offline")
	}
}

// Working on a linked server, the page's own stays listed, from what its
// link read once the window left it.
func TestBrowserKeepsHomeOnALinkedServer(t *testing.T) {
	ui := browserUI(t)
	home := ui.server
	ui.handOver(home)
	ui.server = &remoteDesktop{parent: ui, client: &remoteClient{address: serverAddress{URL: shopServer}}, name: "shop",
		projects: []string{"/srv/shop"}, chats: []remoteapi.Chat{{ID: "s2", Title: "Shop chat", Workspace: "/srv/shop"}}, convs: map[string]*conversation{}}
	if !ui.links[0].answered {
		t.Fatal("the page's server's link was not handed what the window last read")
	}
	ui.renderChats()
	wantListed(t, ui, "api", "Home chat", "shop", "Shop chat", "legacy")
	for _, group := range ui.sidebarGroups() {
		if group.active != (group.target.id == shopServer) {
			t.Errorf("%s active %v", group.target.id, group.active)
		}
	}
}

// Each linked server's projects are remembered for the next visit; one no
// longer linked is forgotten.
func TestBrowserRemembersEachServersProjects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveServerAddress(serverAddress{URL: oldServer, Token: "c"})
	ui := browserUI(t)
	ui.serverProjects = map[string][]string{oldServer: {"/srv/legacy"}, "https://gone.lan:1": {"/x"}}
	ui.rememberServerProjects(testServer, []string{"/srv/api"})
	want := map[string][]string{testServer: {"/srv/api"}, oldServer: {"/srv/legacy"}}
	if !maps.EqualFunc(ui.serverProjects, want, slices.Equal) {
		t.Fatalf("remembered %v, want %v", ui.serverProjects, want)
	}
}

// The browser client keeps the servers it linked in the same pairings as
// the desktop (remote_pairings.go): its own server first, then the others
// in the order they were linked; unlinking one keeps the others.
func TestBrowserServersArePairings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ui := &desktopUI{home: testServer}
	if got := ui.browserServers(); !slices.Equal(got, []serverAddress{{URL: testServer}}) {
		t.Fatalf("before pairing: %v", got)
	}
	shop, old, home := serverAddress{URL: shopServer, Token: "b"}, serverAddress{URL: oldServer, Token: "c"}, serverAddress{URL: testServer, Token: "a"}
	for _, address := range []serverAddress{shop, home, old} {
		saveServerAddress(address)
	}
	if got := ui.browserServers(); !slices.Equal(got, []serverAddress{home, shop, old}) {
		t.Fatalf("servers %v", got)
	}
	forgetServerAddress(oldServer)
	if got := ui.browserServers(); !slices.Equal(got, []serverAddress{home, shop}) {
		t.Fatalf("after unlinking: %v", got)
	}
	if got := loadServerAddress(); got != home { // the last paired went: the one before it in the list takes its place
		t.Fatalf("last paired %v, want %v", got, home)
	}
}

func TestBrowserTargets(t *testing.T) {
	home := serverAddress{URL: testServer, Token: "a"}
	linked := []serverAddress{{URL: shopServer, Token: "b"}, {URL: testServer, Token: "stale"}, {URL: shopServer}}
	got := browserTargets(home, linked)
	if want := []serverAddress{home, {URL: shopServer, Token: "b"}}; !slices.Equal(got, want) {
		t.Fatalf("targets %v, want %v", got, want)
	}
	if got := browserTargets(serverAddress{}, linked[:1]); !slices.Equal(got, linked[:1]) {
		t.Fatalf("with no home: %v", got)
	}
}

// The desktop's paired server keeps no icon rule of the browser's: a
// server is marked unless it is the page's own.
func TestServerMarkLeavesHomeOut(t *testing.T) {
	if serverTarget(testServer).mark("") != iconBrowser || serverTarget(testServer).mark(testServer) != nil || serverTarget(shopServer).mark(testServer) != iconBrowser {
		t.Fatal("wrong server marks")
	}
}
