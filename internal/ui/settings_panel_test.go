// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
)

// shownRows counts the rows of a code well that have a text on screen.
func shownRows(view *outputView) int {
	if view.render == nil {
		return 0
	}
	n := 0
	for slot, row := range view.render.owner {
		if row >= 0 && view.render.texts[slot].Visible() {
			n++
		}
	}
	return n
}

// The commands in the side panel are drawn as soon as Settings closes.
func TestSidePanelCommandsShownAfterSettings(t *testing.T) {
	test.NewTempApp(t)
	ui, _ := twoWindows(t)
	ui.windows = ui.windows[:1]
	ui.window = test.NewWindow(nil)
	ui.window.SetContent(ui.build())
	ui.window.Resize(fyne.NewSize(1400, 820))
	chat := &conversation{ID: "c", Title: "Chat", Items: []agent.ChatItem{{Kind: "tool", Tool: "shell", Args: `{"command":"ls -la"}`, Text: "out"}}}
	ui.chats = []*conversation{chat}
	ui.openLocalChat(chat)
	ui.panel.open(chat, 0)
	ui.panel.unfold()

	ui.settings()
	ui.window.Resize(fyne.NewSize(1300, 800))
	ui.prefs.closeNow()

	if len(ui.panel.texts) == 0 {
		t.Fatal("the panel has no code wells")
	}
	for i, well := range ui.panel.texts {
		if shownRows(well) == 0 {
			t.Errorf("well %d shows no row after Settings closed (top %v, bottom %v)", i, well.top, well.bottom)
		}
	}
}
