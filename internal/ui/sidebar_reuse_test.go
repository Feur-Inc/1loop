// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"testing"

	"1loop/internal/agent"
)

// Every kind of sidebar entry is kept from one rendering to the next, and
// only the rows that show something new are refreshed (#475): subchats,
// "Show more", the group with no folder and its placeholder.
func TestSidebarKeepsItsEntries(t *testing.T) {
	ui := sidebarUI(t)
	ui.chats = append(ui.chats, &conversation{ID: "sub", Title: "Check the tests",
		Config: agent.Config{Workspace: "/home/u/app"}, Subchat: &agent.SubchatLink{Parent: "here"}})
	for i := range groupChatLimit + 1 {
		ui.chats = append(ui.chats, &conversation{ID: fmt.Sprint("more", i), Title: fmt.Sprint("Chat ", i), Config: agent.Config{Workspace: "/home/u/app"}})
	}
	ui.renderChats()
	before := map[string]any{}
	for _, object := range ui.projectList.Objects {
		before[ui.sideGlide.keys[object]] = object
	}
	for _, key := range []string{"chat:sub", "more:" + groupKey(localTarget, "/home/u/app"), "chat:here"} {
		if before[key] == nil {
			t.Fatalf("%s is not listed: %v", key, sidebarTexts(ui))
		}
	}
	ui.chats[2].Title = "Check the tests again"
	ui.renderChats()
	for _, object := range ui.projectList.Objects {
		key := ui.sideGlide.keys[object]
		if was, ok := before[key]; ok && was != object {
			t.Errorf("%s was made again", key)
		}
	}
	wantListed(t, ui, "Check the tests again")
}
