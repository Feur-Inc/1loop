// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"1loop/internal/remoteapi"
	"1loop/internal/sshmachine"
)

const testServer = "https://box.lan:47101"

// headerRow is the sidebar's row named text: a project's header.
func headerRow(t *testing.T, ui *desktopUI, text string) *tapRow {
	t.Helper()
	for _, object := range ui.projectList.Objects {
		if row := firstRow(object); row != nil && row.text == text {
			return row
		}
	}
	t.Fatalf("no row %q in the sidebar: %q", text, sidebarTexts(ui))
	return nil
}

// sidebarLines are every text of the sidebar, its rows' and its notes'.
func sidebarLines(ui *desktopUI) []string {
	var lines []string
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch object := object.(type) {
		case *tapRow:
			lines = append(lines, object.text)
		case *canvas.Text:
			lines = append(lines, object.Text)
		case *fitLabel:
			lines = append(lines, object.text)
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		}
	}
	for _, object := range ui.projectList.Objects {
		walk(object)
	}
	return lines
}

// linkedUI is sidebarUI with a paired server followed by its link, which
// has not answered yet, and the projects it listed the last time.
func linkedUI(t *testing.T) *desktopUI {
	ui := sidebarUI(t)
	ui.links = []*serverLink{{address: serverAddress{URL: testServer, Token: "t"}, wake: make(chan struct{}, 1)}}
	ui.serverProjects = map[string][]string{testServer: {"/srv/api"}}
	return ui
}

// The sidebar lists this computer's projects first, then each SSH
// machine's in Settings' order, then the server's; a machine with no
// project and no chat is left out.
func TestSidebarGroupsOrder(t *testing.T) {
	ui := linkedUI(t)
	ui.sshMachines = append([]sshmachine.Settings{{ID: "m0", Host: "empty.lan", User: "u"}}, ui.sshMachines...)
	var got []string
	for _, group := range ui.sidebarGroups() {
		got = append(got, group.target.key())
	}
	want := []string{"local", "ssh:m1", "server:" + testServer}
	if !slices.Equal(got, want) {
		t.Fatalf("groups %q, want %q", got, want)
	}
	if groups := ui.sidebarGroups(); !groups[0].active || groups[1].active || groups[2].active {
		t.Fatal("only this computer is in use")
	}
}

// A project whose target does not answer stays listed, marked offline,
// with its chats when they are at hand: an SSH machine's are kept here.
func TestOfflineMachineStaysListed(t *testing.T) {
	ui := sidebarUI(t)
	ui.reaches = map[string]targetReach{sshTarget("m1").key(): {state: reachOffline, problem: "build.lan:22 could not be reached"}}
	ui.renderChats()
	wantListed(t, ui, "app", "Fix the parser", "site", "Deploy the site")
	if row := headerRow(t, ui, "site"); row.trailing != offlineMark {
		t.Errorf("the offline machine's project shows %q, want %q", row.trailing, offlineMark)
	}
	if row := headerRow(t, ui, "app"); row.trailing == offlineMark {
		t.Error("this computer's project is marked offline")
	}
}

// A paired server that does not answer keeps the projects it listed the
// last time, marked offline; its chats are not at hand, and say so.
func TestOfflineServerStaysListed(t *testing.T) {
	ui := linkedUI(t)
	ui.reaches = map[string]targetReach{serverTarget(testServer).key(): {state: reachOffline, problem: "connection refused"}}
	ui.renderChats()
	if row := headerRow(t, ui, "api"); row.trailing != offlineMark {
		t.Errorf("the offline server's project shows %q, want %q", row.trailing, offlineMark)
	}
	if !slices.Contains(sidebarLines(ui), "Chats show once it's back") {
		t.Errorf("no note on the server's chats: %q", sidebarLines(ui))
	}
	wantListed(t, ui, "app", "site") // the other targets are listed as usual
	// Its note is cut short, never wider than the narrowest sidebar's
	// column (the sidebar has 12 points of margin on each side).
	if width := ui.projectList.MinSize().Width; width > sidebarMinWidth-24 {
		t.Errorf("the list needs %v points, more than the narrowest sidebar's %v", width, sidebarMinWidth-24)
	}
}

// Once the server answered, its projects and chats are listed; when it
// stops answering, they stay, marked offline, rather than disappearing.
func TestServerChatsStayWhenItGoesOffline(t *testing.T) {
	ui := linkedUI(t)
	ui.links[0].answered = true
	ui.links[0].listing = serverListing{name: "box", projects: []string{"/srv/api", "/srv/web"},
		chats: []remoteapi.Chat{{ID: "s1", Title: "Server chat", Workspace: "/srv/web"}}}
	ui.reaches = map[string]targetReach{serverTarget(testServer).key(): {state: reachOnline}}
	ui.renderChats()
	wantListed(t, ui, "api", "web", "Server chat")

	ui.setReach(serverTarget(testServer), reachOffline, "connection refused")
	wantListed(t, ui, "api", "web", "Server chat")
	if row := headerRow(t, ui, "web"); row.trailing != offlineMark {
		t.Errorf("trailing %q, want %q", row.trailing, offlineMark)
	}
}

// A project of another target than this computer shows its target's icon
// after its name; this computer's show none.
func TestProjectsShowTheirTarget(t *testing.T) {
	ui := linkedUI(t)
	ui.renderChats()
	for name, want := range map[string]fyne.Resource{"app": nil, "site": iconTerminal, "api": iconBrowser} {
		if got := headerRow(t, ui, name).mark; got != want {
			t.Errorf("%s: mark %v, want %v", name, got, want)
		}
	}
}

// The server's projects are remembered with the state, for a start when
// it does not answer; only the servers paired now are.
func TestServerProjectsRemembered(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no server paired
	path := filepath.Join(t.TempDir(), "state.json")
	before := &desktopUI{ctx: context.Background(), statePath: path, foldedProjects: map[string]bool{}, shortcutKeys: map[string]string{}}
	before.rememberServerProjects("https://old.lan:47101", []string{"/srv/old"})
	before.rememberServerProjects(testServer, []string{"/srv/api", "/srv/web"})
	if before.saveTimer != nil {
		before.saveTimer.Stop()
	}
	before.persist(true)
	if before.saveError != "" {
		t.Fatal(before.saveError)
	}
	after := &desktopUI{ctx: context.Background(), statePath: path, foldedProjects: map[string]bool{}, shortcutKeys: map[string]string{}}
	after.load()
	if len(after.serverProjects) != 1 || !slices.Equal(after.serverProjects[testServer], []string{"/srv/api", "/srv/web"}) {
		t.Fatalf("remembered %v", after.serverProjects)
	}
}

// Leaving the server for another target, its projects and chats stay
// listed as the window's connection last read them.
func TestLeavingTheServerKeepsItListed(t *testing.T) {
	ui := linkedUI(t)
	remote := &remoteDesktop{parent: ui, client: &remoteClient{address: serverAddress{URL: testServer}}, name: "box",
		projects: []string{"/srv/api"}, chats: []remoteapi.Chat{{ID: "s1", Title: "Server chat", Workspace: "/srv/api"}}}
	ui.handOver(remote)
	if link := ui.links[0]; !link.answered || !slices.Equal(link.listing.projects, []string{"/srv/api"}) {
		t.Fatalf("link after the hand-over: %+v", link)
	}
	ui.renderChats()
	wantListed(t, ui, "api", "Server chat", "app", "site")
}

func TestServerProjectList(t *testing.T) {
	chats := []remoteapi.Chat{{Workspace: "/srv/web"}, {Workspace: "/srv/api/.worktrees/fix"}, {Workspace: "/srv/api"}, {Workspace: ""}}
	worktrees := map[string]string{"/srv/api/.worktrees/fix": "/srv/api"}
	got := serverProjectList("/srv/api", []string{"/srv/docs", "/srv/api"}, chats, worktrees)
	// Its default folder first, then those it lists, then its chats'; each
	// once, a worktree under its project, no empty folder.
	if want := []string{"/srv/api", "/srv/docs", "/srv/web"}; !slices.Equal(got, want) {
		t.Fatalf("projects %q, want %q", got, want)
	}
	if got := serverProjectList("", nil, nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("an empty server lists %#v, want an empty list", got)
	}
}

// A target's project has a key of its own in the sidebar's folds; this
// computer's keeps its path, as saved before targets.
func TestGroupKeys(t *testing.T) {
	keys := map[string]bool{}
	for _, target := range []projectTarget{localTarget, sshTarget("m1"), sshTarget("m2"), serverTarget(testServer)} {
		key := groupKey(target, "/srv/app")
		if keys[key] {
			t.Fatalf("%v shares the key %q", target, key)
		}
		keys[key] = true
	}
	if key := groupKey(localTarget, "/home/u/app"); key != "/home/u/app" {
		t.Fatalf("this computer's key %q", key)
	}
	group := targetGroup{target: serverTarget(testServer), projects: []string{"/srv/api"}, worktrees: map[string]string{"/srv/api/.worktrees/fix": "/srv/api"}}
	listed := group.listed()
	for folder, want := range map[string]string{
		"/srv/api/.worktrees/fix": groupKey(group.target, "/srv/api"), // under its project
		"/srv/gone":               groupKey(group.target, ""),         // Other chats
	} {
		if got := group.keyOf(listed, sideChat{folder: folder}); got != want {
			t.Errorf("keyOf(%s) = %q, want %q", folder, got, want)
		}
	}
	if got := group.keyOf(listed, sideChat{folder: "/x", scratch: true}); got != scratchGroup {
		t.Errorf("a chat with no folder: %q", got)
	}
}

// A new chat with no folder runs on the device in use: this computer.
func TestNoFolderRunsHere(t *testing.T) {
	if noFolderTarget() != localTarget {
		t.Fatalf("no folder runs on %v", noFolderTarget())
	}
}

// The target's icon after a project's name never runs into its trailing
// text: in a narrow sidebar it steps aside for the name.
func TestRowMarkStepsAside(t *testing.T) {
	test.NewTempApp(t)
	row := newRow("docs", iconChevron, styleNav, nil)
	row.mark, row.trailing, row.truncate, row.endInset = iconBrowser, offlineMark, true, 38
	renderer := test.TempWidgetRenderer(t, row).(*tapRowRenderer)
	renderer.Layout(fyne.NewSize(256, 32)) // the default sidebar
	if !renderer.mark.Visible() {
		t.Fatal("no mark in the default sidebar")
	}
	if end, trailing := renderer.mark.Position().X+renderer.mark.Size().Width, renderer.trailing.Position().X; end > trailing {
		t.Fatalf("the mark ends at %v, past the trailing text at %v", end, trailing)
	}
	renderer.Layout(fyne.NewSize(150, 32)) // narrower than the narrowest sidebar
	if renderer.mark.Visible() {
		t.Fatal("the mark stays where the name has no room")
	}
}
