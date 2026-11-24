// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
	"1loop/internal/sshmachine"
)

// sidebarUI is a window reduced to its sidebar, with a project and a chat
// on this computer and on an SSH machine (#359).
func sidebarUI(t *testing.T) *desktopUI {
	t.Helper()
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: &desktopApp{ctx: context.Background(), foldedProjects: map[string]bool{}, allChatsShown: map[string]bool{}, worktreesShown: map[string]bool{}}}
	ui.sideGlide = newGlideLayout()
	ui.projectList = container.New(ui.sideGlide)
	ui.projects = []string{"/home/u/app"}
	ui.sshMachines = []sshmachine.Settings{{ID: "m1", Name: "build-box", Host: "build.lan", User: "u", Port: 22}}
	ui.machineProjects = map[string][]string{"m1": {"/srv/site"}}
	ui.chats = []*conversation{
		{ID: "here", Title: "Fix the parser", Config: agent.Config{Workspace: "/home/u/app"}},
		{ID: "there", Title: "Deploy the site", Config: agent.Config{Workspace: "/srv/site", MachineID: "m1"}},
	}
	return ui
}

// sidebarTexts are the texts of the sidebar's rows, top to bottom.
func sidebarTexts(ui *desktopUI) []string {
	var texts []string
	for _, object := range ui.projectList.Objects {
		if row := firstRow(object); row != nil {
			texts = append(texts, row.text)
		}
	}
	return texts
}

func wantListed(t *testing.T, ui *desktopUI, texts ...string) {
	t.Helper()
	listed := sidebarTexts(ui)
	for _, text := range texts {
		if !slices.Contains(listed, text) {
			t.Errorf("%q is not in the sidebar: %q", text, listed)
		}
	}
}

// This computer in use, the SSH machine's project and chat are listed too.
func TestSidebarListsEveryTargetsProjects(t *testing.T) {
	ui := sidebarUI(t)
	ui.renderChats()
	wantListed(t, ui, "app", "Fix the parser", "site", "Deploy the site")
}

// The SSH machine in use, this computer's projects stay listed: switching
// to a target no longer hides the others.
func TestSidebarKeepsThisComputerOnAMachine(t *testing.T) {
	ui := sidebarUI(t)
	ui.cfg.MachineID = "m1"
	ui.renderChats()
	wantListed(t, ui, "app", "Fix the parser", "site", "Deploy the site")
}

// A server in use, this computer's and the machine's projects stay listed
// beside the server's.
func TestSidebarKeepsThisComputerOnAServer(t *testing.T) {
	ui := sidebarUI(t)
	ui.server = &remoteDesktop{parent: ui, live: remoteSync{answered: true}, client: &remoteClient{address: serverAddress{URL: "https://box.lan:47101"}}, name: "box",
		projects: []string{"/srv/api"}, chats: []remoteapi.Chat{{ID: "s1", Title: "Server chat", Workspace: "/srv/api"}}, convs: map[string]*conversation{}}
	ui.renderChats()
	wantListed(t, ui, "app", "Fix the parser", "site", "Deploy the site", "api", "Server chat")
}

// This computer's chats with no folder stay listed, under No folder, while
// the window works on an SSH machine.
func TestSidebarKeepsNoFolderChatsOnAMachine(t *testing.T) {
	ui := sidebarUI(t)
	ui.chats = append(ui.chats, &conversation{ID: "loose", Title: "Quick question", Config: agent.Config{Workspace: "/state/scratch/a", Scratch: "/state/scratch/a"}})
	ui.cfg.MachineID = "m1"
	ui.renderChats()
	wantListed(t, ui, noFolderLabel, "Quick question")
}

// A project copied to a server keeps its chats' IDs (projectsync): the
// same ID is then this computer's chat and the server's. Their rows shared
// one key, so from the second rendering on they shared one row, drawn at
// the second place only: the first chat was gone, an empty gap where it
// was (#600).
func TestSidebarSameChatIDOnAServer(t *testing.T) {
	ui := sidebarUI(t)
	ui.server = &remoteDesktop{parent: ui, live: remoteSync{answered: true}, client: &remoteClient{address: serverAddress{URL: "https://box.lan:47101"}}, name: "box",
		projects: []string{"/srv/api"}, chats: []remoteapi.Chat{{ID: "here", Title: "Fix the parser, on the server", Workspace: "/srv/api"}}, convs: map[string]*conversation{}}
	for range 2 { // the rows are reused from the second rendering on
		ui.renderChats()
	}
	wantListed(t, ui, "Fix the parser", "Fix the parser, on the server")
	seen := map[fyne.CanvasObject]bool{}
	for _, object := range ui.projectList.Objects {
		if seen[object] {
			t.Errorf("%q is listed twice", firstRow(object).text)
		}
		seen[object] = true
	}
	keys := map[string]bool{}
	for _, key := range ui.sideGlide.keys {
		if keys[key] {
			t.Errorf("two rows have the key %q: the glide puts them at one place", key)
		}
		keys[key] = true
	}
}

// A server's chat with no folder runs in a folder the server made for it:
// it is listed under No folder, not as a project of that folder's name.
func TestSidebarServerChatWithNoFolder(t *testing.T) {
	ui := sidebarUI(t)
	chats := []remoteapi.Chat{{ID: "q", Title: "Quick question", Workspace: "/data/scratch/x600", Scratch: true}}
	projects := serverProjectList("/srv/api", nil, chats, nil)
	if slices.Contains(projects, "/data/scratch/x600") {
		t.Errorf("its folder is a project of the server: %q", projects)
	}
	ui.server = &remoteDesktop{parent: ui, live: remoteSync{answered: true}, client: &remoteClient{address: serverAddress{URL: "https://box.lan:47101"}}, name: "box",
		projects: projects, chats: chats, convs: map[string]*conversation{}}
	ui.renderChats()
	wantListed(t, ui, noFolderLabel, "Quick question")
	if slices.Contains(sidebarTexts(ui), "x600") {
		t.Errorf("its folder is listed as a project: %q", sidebarTexts(ui))
	}
}

// A pane of the window works on a server while the other pane, on this
// computer, has the focus: the sidebar shows the focused pane, and it
// listed the server's chats as the link last read them, which stops
// reading while a pane works on that server. A chat made on the server
// since was missing (#600).
func TestSidebarServerChatsFromTheOtherPane(t *testing.T) {
	ui := sidebarUI(t)
	const address = "https://box.lan:47101"
	old := remoteapi.Chat{ID: "s1", Title: "Server chat", Workspace: "/srv/api"}
	ui.links = []*serverLink{{address: serverAddress{URL: address}, answered: true,
		listing: serverListing{name: "box", projects: []string{"/srv/api"}, chats: []remoteapi.Chat{old}}}}
	ui.server = &remoteDesktop{parent: ui, live: remoteSync{answered: true}, client: &remoteClient{address: serverAddress{URL: address}}, name: "box",
		projects: []string{"/srv/api"}, chats: []remoteapi.Chat{{ID: "s2", Title: "Made since", Workspace: "/srv/api"}, old}, convs: map[string]*conversation{}}
	right := &desktopUI{desktopApp: ui.desktopApp}
	ui.tree = &splitNode{pane: ui}
	ui.tree.splitWith(right, false, 0.5)
	right.shareWindow(ui)
	ui.focus = right
	if right.serverInUse(address) != ui.server {
		t.Error("the server is not seen in use: its link goes on reading it")
	}
	ui.renderChats()
	wantListed(t, ui, "api", "Server chat", "Made since")
}
