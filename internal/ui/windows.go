// SPDX-License-Identifier: MPL-2.0

package ui

// Several windows (#285). Each window is a desktopUI: its own chat on
// screen, composer, side panel and scroll. They share one desktopApp: the
// backend, the chats, the settings, the tray. A chat is shown in at most
// one window; what happens to a chat (an answer streaming in, an approval,
// a question) is drawn in the window showing it, whichever window started
// the run.
//
// The first window is the main one. It is the master window (closing it for
// good ends 1loop), so it is only hidden while other windows stay open, and
// it is the one the tray and a second launch bring back.

import (
	"fmt"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

// savedWindow is a window opened beside the main one, as the state file
// keeps it.
type savedWindow struct {
	Chat   string      `json:"chat,omitempty"` // the chat it showed, "" for a new chat
	Width  float32     `json:"width,omitempty"`
	Height float32     `json:"height,omitempty"`
	Split  *savedSplit `json:"split,omitempty"` // its panes, when it is split (panes.go)
}

// viewOf is the window showing chat, else ui itself (or the main window,
// once ui was closed).
func (ui *desktopUI) viewOf(chat *conversation) *desktopUI {
	if ui.desktopApp == nil || chat != nil && ui.current == chat {
		return ui
	}
	views := ui.allPanes() // every pane of every window (panes.go)
	if chat != nil {
		for _, view := range views {
			if view.current == chat {
				return view
			}
		}
	}
	if len(ui.windows) > 0 && !slices.Contains(views, ui) {
		return ui.mainView().focused() // ui's window or pane was closed: never draw in it
	}
	return ui
}

// sheetView is where an approval or questions for chat appear: its window,
// else (the chat on screen nowhere) the window the user works in.
func (ui *desktopUI) sheetView(chat *conversation) *desktopUI {
	if ui.current == chat || ui.desktopApp == nil {
		return ui
	}
	return ui.activeView()
}

// mainView is the first window.
func (ui *desktopUI) mainView() *desktopUI {
	if ui.desktopApp == nil || len(ui.windows) == 0 {
		return ui.windowOf()
	}
	return ui.windows[0]
}

// allWindows is every window, the main one first.
func (ui *desktopUI) allWindows() []*desktopUI {
	if ui.desktopApp == nil || len(ui.windows) == 0 {
		return []*desktopUI{ui.windowOf()}
	}
	return ui.windows
}

// shown reports whether the user can see ui's window.
func (ui *desktopUI) shown() bool {
	return !ui.windowOf().hiddenAlone && (ui.tray == nil || !ui.tray.hidden)
}

// showing reports whether a window the user can see shows chat.
func (app *desktopApp) showing(chat *conversation) bool {
	for _, view := range app.windows {
		for _, view := range view.panesOf() {
			if view.current == chat && view.shown() {
				return true
			}
		}
	}
	return false
}

// activate notes ui as the window and pane the user works in: links and
// #12 references in any window act there, the window's sidebar shows it.
func (ui *desktopUI) activate() {
	if ui == nil || ui.desktopApp == nil {
		return
	}
	window := ui.windowOf()
	ui.active = window
	if window.focus == ui {
		return
	}
	before := window.focused()
	window.focus = ui
	if before != ui && window.tree != nil {
		ui.focusChanged() // panes.go
	}
}

// activeView is the window the user last worked in, else the main one.
func (ui *desktopUI) activeView() *desktopUI {
	if ui.desktopApp != nil && ui.active != nil && slices.Contains(ui.windows, ui.active) && ui.active.shown() {
		return ui.active.focused()
	}
	return ui.mainView().focused()
}

// raise brings ui's window to the front, out of the tray if need be.
func (ui *desktopUI) raise() {
	window := ui.windowOf()
	if window == ui.mainView() {
		window.showWindow()
	} else {
		if ui.tray != nil && ui.tray.hidden {
			ui.mainView().showWindow() // every window comes back with it
		}
		window.window.Show()
		window.window.RequestFocus()
	}
	ui.activate() // and its pane takes the focus
	if ui.isSplit() && ui.prompt != nil {
		ui.window.Canvas().Focus(ui.prompt)
	}
}

// openWhereShown opens a chat from a notification or the tray: in the window
// that shows it, else in the main window.
func (ui *desktopUI) openWhereShown(chat *conversation) {
	main := ui.mainView()
	if !slices.Contains(ui.chats, chat) { // removed since
		main.showWindow()
		return
	}
	view := ui.viewOf(chat)
	if view.current != chat {
		view = main.focused()
		view.openChat(chat)
	}
	view.raise()
}

// otherWindowShown reports whether another window than ui stays on screen.
func (ui *desktopUI) otherWindowShown() bool {
	for _, view := range ui.allWindows() {
		if view != ui && view.shown() {
			return true
		}
	}
	return false
}

// canOpenWindow reports whether chat can have a window of its own: this
// computer's and SSH machines' chats, not a server's (which the main window
// shows through its target).
func (ui *desktopUI) canOpenWindow(chat *conversation) bool {
	return ui.desktopApp != nil && ui.statePath != "" && (chat == nil || chat.server == nil)
}

// openWindow opens a new window on chat (nil: a new chat in the project of
// the window asking). A chat on screen elsewhere moves: the window it leaves
// goes to a new chat, so that a chat is never shown twice.
func (ui *desktopUI) openWindow(chat *conversation) *desktopUI {
	if !ui.canOpenWindow(chat) {
		return nil
	}
	if chat != nil {
		if from := ui.viewOf(chat); from.current == chat {
			from.startNew()
		}
	}
	size := ui.window.Canvas().Size()
	if size.Width < 400 || size.Height < 300 {
		size = fyne.NewSize(1180, 760)
	}
	view := ui.newWindowView(ui.cfg, size)
	if chat != nil {
		view.openLocalChat(chat)
	}
	view.window.Show()
	windowIdentity(view.window)
	view.window.RequestFocus()
	view.window.Canvas().Focus(view.prompt)
	ui.save()
	return view
}

// newWindowView builds a window on the start screen of cfg's project.
func (ui *desktopUI) newWindowView(cfg agent.Config, size fyne.Size) *desktopUI {
	app := ui.desktopApp
	app.windowSerial++
	view := &desktopUI{desktopApp: app, cfg: cfg, draftSuffix: fmt.Sprintf("@window%d", app.windowSerial)}
	view.cfg.ClearSessions()
	view.cfg.RanWith = nil
	a := fyne.CurrentApp()
	if drv, ok := a.Driver().(desktop.Driver); ok && ui.chrome {
		view.window = drv.CreateSplashWindow()
		view.window.SetTitle("1loop")
	} else {
		view.window = a.NewWindow("1loop")
	}
	app.windows = append(app.windows, view)
	view.window.SetPadded(false)
	view.window.Resize(size)
	view.window.SetIcon(brandResource("1loop-ring-on-black.png"))
	view.window.SetContent(view.build())
	view.window.SetOnDropped(func(at fyne.Position, uris []fyne.URI) {
		var files []string
		for _, uri := range uris {
			if uri.Scheme() == "file" {
				files = append(files, uri.Path())
			}
		}
		view.paneAt(at).attachFiles(files)
	})
	view.prompt.switchDraft(view.draftKey())
	view.window.SetCloseIntercept(view.closeWindow)
	view.registerShortcuts()
	view.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { view.focused().copySelection() })
	view.updateHeader()
	view.renderSidebar(view.sidebarGroups())
	view.adoptModels(ui.mainView())
	view.drawUpdateRow()
	view.usageChanged()
	view.syncRun()
	view.activate()
	platform.Devf("ui", "window %d opened", len(app.windows))
	return view
}

// closeAlone closes ui's window while another one stays: the main one is
// hidden (closing it would end 1loop), any other goes, its chat's panel kept
// with the chat.
func (ui *desktopUI) closeAlone() {
	ui = ui.windowOf()
	if ui == ui.mainView() {
		ui.hiddenAlone = true
		ui.window.Hide()
		if ui.active == ui {
			ui.active = nil
		}
		return
	}
	panes := ui.panesOf()
	for _, pane := range panes {
		pane.releaseWindow()
	}
	ui.windows = slices.DeleteFunc(ui.windows, func(view *desktopUI) bool { return view == ui })
	if ui.active == ui {
		ui.active = nil
	}
	for _, pane := range panes {
		pane.current = nil
	}
	ui.window.SetCloseIntercept(nil)
	ui.window.Close()
	platform.Devf("ui", "window closed, %d left", len(ui.windows))
	ui.save()
}

// releaseWindow stops what a window other than the main one runs: its
// chat's side panel goes back to the chat, its start screen's tabs end.
func (ui *desktopUI) releaseWindow() {
	ui.cancelChatLoad()
	if ui.panel != nil {
		ui.panel.stash()
		for _, tab := range ui.panel.home.tabs {
			tab.dispose()
		}
		ui.panel.home.tabs = nil
		ui.panel.owner = nil
	}
	ui.mark.stop()
	ui.workMark.stop()
	ui.chatWait.stop()
}

// savedWindows lists the windows beside the main one, for the state file.
func (ui *desktopUI) savedWindows() []savedWindow {
	var out []savedWindow
	for _, view := range ui.allWindows()[1:] {
		saved := savedWindow{}
		if view.current != nil {
			saved.Chat = view.current.ID
		}
		if view.window != nil {
			size := view.window.Canvas().Size()
			saved.Width, saved.Height = size.Width, size.Height
		}
		saved.Split = view.savedLayout() // panes.go
		out = append(out, saved)
	}
	return out
}

// restoreWindows opens the windows of the last run again, each on its chat.
// A window whose chat was deleted since is not.
func (ui *desktopUI) restoreWindows(saved []savedWindow) {
	for _, window := range restorableWindows(saved, ui.chats) {
		size := fyne.NewSize(window.Width, window.Height)
		if size.Width < 400 || size.Height < 300 {
			size = fyne.NewSize(1180, 760)
		}
		view := ui.newWindowView(ui.cfg, size)
		if chat := ui.chatByID(window.Chat); chat != nil {
			view.openLocalChat(chat)
		}
		view.restoreLayout(window.Split)
		view.window.Show()
		windowIdentity(view.window)
	}
}

// restorableWindows are the saved windows to open again: a window whose
// chat is gone, or already shown by an earlier one, is not.
func restorableWindows(saved []savedWindow, chats []*conversation) []savedWindow {
	var out []savedWindow
	seen := map[string]bool{}
	for _, window := range saved {
		if window.Chat != "" {
			if seen[window.Chat] || !slices.ContainsFunc(chats, func(chat *conversation) bool { return chat.ID == window.Chat }) {
				continue
			}
			seen[window.Chat] = true
		}
		out = append(out, window)
	}
	return out
}

// adoptModels lists in ui's model picker the models another window loaded,
// with ui's own model selected.
func (ui *desktopUI) adoptModels(from *desktopUI) {
	if ui.model == nil || from.model == nil {
		return
	}
	ui.model.Options, ui.model.Details = from.model.Options, from.model.Details
	if from.modelsLoading {
		ui.model.Disable()
		ui.model.placeholder = "Loading models…"
		ui.send.Disable()
		return
	}
	ui.model.Enable()
	if len(ui.model.Options) == 0 {
		ui.model.placeholder = "No model available"
		ui.model.refresh()
		ui.send.Disable() // as in the main window: nothing to send to
		return
	}
	selected := ui.model.Options[0]
	for _, label := range ui.model.Options {
		if choice := ui.choices[label]; choice.Connection.ID == ui.cfg.ConnectionID && choice.ID == ui.cfg.Model {
			selected = label
			break
		}
	}
	ui.model.SetSelected(selected)
	ui.send.Enable()
	ui.refreshContextRing()
}
