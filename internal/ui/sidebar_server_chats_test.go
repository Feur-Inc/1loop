// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"

	"1loop/internal/remoteapi"
)

// What #600 ruled out: every kind of chat a server lists gets a row of the
// sidebar, and a row that came in with the server's state is drawn whole
// once the sidebar was rendered again during its fade-in.

// serverSidebar is sidebarUI working on a server that answered with chats.
func serverSidebar(t *testing.T, chats []remoteapi.Chat, worktrees map[string]string) *desktopUI {
	t.Helper()
	ui := sidebarUI(t)
	ui.server = &remoteDesktop{parent: ui, live: remoteSync{answered: true}, client: &remoteClient{address: serverAddress{URL: "https://box.lan:47101"}}, name: "box",
		projects: serverProjectList("/srv/api", []string{"/srv/api"}, chats, worktrees), chats: chats, worktrees: worktrees, convs: map[string]*conversation{}}
	return ui
}

// A chat in a worktree (of a listed project, or of one the server does not
// list), in a folder that is no project, with no folder at all, a subchat
// whose parent is in another folder, one whose parent is gone, and chats
// past the first five of a project once "Show more" is pressed: each has a
// row.
func TestSidebarListsEveryKindOfServerChat(t *testing.T) {
	chats := []remoteapi.Chat{
		{ID: "wt", Title: "In a worktree", Workspace: "/srv/api-wt"},
		{ID: "wt2", Title: "In a worktree of an unlisted project", Workspace: "/srv/lib-wt"},
		{ID: "other", Title: "In a folder that is no project", Workspace: "/tmp/other"},
		{ID: "none", Title: "With no workspace"},
		{ID: "sub", Title: "Subchat of a chat elsewhere", Workspace: "/srv/api-wt", Parent: "other"},
		{ID: "orphan", Title: "Subchat whose parent is gone", Workspace: "/srv/api", Parent: "deleted"},
	}
	for i := range 7 {
		chats = append(chats, remoteapi.Chat{ID: fmt.Sprint("p", i), Title: fmt.Sprint("Project chat ", i), Workspace: "/srv/api"})
	}
	ui := serverSidebar(t, chats, map[string]string{"/srv/api-wt": "/srv/api", "/srv/lib-wt": "/srv/lib"})
	ui.renderChats()
	listed := map[string]bool{}
	for _, text := range sidebarTexts(ui) {
		listed[text] = true
	}
	if !listed["Show 4 more"] { // its 9 chats: 7, one in its worktree, one subchat of a gone parent
		t.Fatalf("no Show more under the project: %q", sidebarTexts(ui))
	}
	ui.allChatsShown[groupKey(serverTarget("https://box.lan:47101"), "/srv/api")] = true
	ui.renderChats()
	for _, chat := range chats {
		wantListed(t, ui, chat.Title)
	}
	wantListed(t, ui, "Other chats")
}

// The server's rows fade in as its state arrives; a chats event renders
// the sidebar again before the fade ends. Once it ends, no row is left
// transparent (the family of #569).
func TestSidebarServerRowsDrawnAfterAFadeCutShort(t *testing.T) {
	was := animationsOff
	animationsOff = false
	t.Cleanup(func() { animationsOff = was })
	ui := serverSidebar(t, []remoteapi.Chat{{ID: "s1", Title: "Server chat", Workspace: "/srv/api"}}, nil)
	ui.projectList.Resize(fyne.NewSize(260, 2000))
	ui.server.live.answered = false
	ui.renderChats() // Connecting…
	ui.server.live.answered = true
	ui.renderChats() // its state came: its rows fade in
	if !ui.sideGlide.fresh[ui.sideRows[chatKey("https://box.lan:47101", "s1")].object] {
		t.Fatal("the server's chat is not fading in")
	}
	stopMotion(&ui.sideGlide.anim)
	ui.sideGlide.step(0.3) // the server's rows half faded in
	ui.server.chats = append([]remoteapi.Chat{{ID: "s2", Title: "Made since", Workspace: "/srv/api"}}, ui.server.chats...)
	ui.renderChats()
	stopMotion(&ui.sideGlide.anim)
	ui.sideGlide.step(1)
	for _, object := range ui.projectList.Objects {
		if row := firstRow(object); row != nil && row.alpha != 1 {
			t.Errorf("%q is drawn at alpha %v", row.text, row.alpha)
		}
	}
	wantListed(t, ui, "Server chat", "Made since")
}
