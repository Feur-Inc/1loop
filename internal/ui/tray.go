// SPDX-License-Identifier: MPL-2.0

package ui

// The tray: 1loop's ring in the system tray shows how the work goes (idle,
// working, waiting for the user, failed), and its menu reaches the window,
// the chat concerned, a new chat and Quit. Closing the window hides it
// there, so runs and the phone connection go on, but only while a tray host
// shows the icon: without one (GNOME without its extension, a bar that
// hosts no tray items) closing still quits, or 1loop would run out of reach.

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	xdraw "golang.org/x/image/draw"

	"1loop/internal/platform"
)

type trayState string

const (
	trayIdle    trayState = "idle"
	trayWorking trayState = "working"
	trayWaiting trayState = "waiting" // an approval or a question
	trayError   trayState = "error"
)

// trayRank orders the states by how much they need the user.
var trayRank = map[trayState]int{trayIdle: 0, trayWorking: 1, trayError: 2, trayWaiting: 3}

// trayChat is what the tray needs to know of a chat.
type trayChat struct {
	title                    string
	running, waiting, failed bool
	started                  int64 // when its run started (Unix nanoseconds)
}

func (chat trayChat) state() trayState {
	switch {
	case chat.waiting:
		return trayWaiting
	case chat.failed:
		return trayError
	case chat.running:
		return trayWorking
	}
	return trayIdle
}

// trayStatus sums the chats up: the state of the one that most needs the
// user, which chat that is, and how many others are in the same state.
type trayStatus struct {
	state  trayState
	chat   int // index in the chats summed up, -1 when idle
	others int
}

// summarizeTray picks the chat the tray speaks of: one waiting for the user
// first (its run is stuck until they answer), then one that failed, then the
// run started last.
func summarizeTray(chats []trayChat) trayStatus {
	status := trayStatus{state: trayIdle, chat: -1}
	for i, chat := range chats {
		state := chat.state()
		switch {
		case state == trayIdle:
		case trayRank[state] > trayRank[status.state]:
			status = trayStatus{state: state, chat: i}
		case state == status.state:
			status.others++
			if chat.started > chats[status.chat].started {
				status.chat = i
			}
		}
	}
	return status
}

// label is the menu's line about the work.
func (status trayStatus) label(title string) string {
	if status.state == trayIdle {
		return "No task running"
	}
	if title == "" {
		title = "New chat"
	}
	text := map[trayState]string{trayWorking: "Working", trayWaiting: "Waiting for you", trayError: "Failed"}[status.state]
	text += " · " + title
	if status.others > 0 {
		text += fmt.Sprintf(" (+%d)", status.others)
	}
	return text
}

// trayIcon is the tray's side of the window.
type trayIcon struct {
	app    desktop.App
	icon   trayState
	menu   string // what the menu was last built from, to rebuild it only on a change
	chat   *conversation
	hidden bool // the window is hidden in the tray
}

// startTray puts the icon in the tray. It is there before the first frame
// so the tray host lists it at once; whether closing hides the window
// depends on a host being there at that moment (trayHostPresent).
func (ui *desktopUI) startTray(a fyne.App) {
	desk, ok := a.(desktop.App)
	if !ok {
		return
	}
	ui.tray = &trayIcon{app: desk}
	// Before the menu, which starts the tray: the host learns then that a
	// click is not for the menu but for the window.
	traySetOnTapped(func() { fyne.Do(ui.showWindow) })
	ui.syncTray()
}

// syncTray follows the chats' states. The icon and the menu are only sent
// to the tray host when they change.
func (ui *desktopUI) syncTray() {
	tray := ui.tray
	if tray == nil {
		return
	}
	chats := make([]trayChat, len(ui.chats))
	for i, chat := range ui.chats {
		chats[i] = trayChat{title: chat.Title, failed: chat.failed}
		if run := chat.run; run != nil {
			chats[i].running = true
			chats[i].waiting = run.approval != nil || run.questions != nil
			chats[i].started = run.started.UnixNano()
		}
	}
	status := summarizeTray(chats)
	tray.chat = nil
	title := ""
	if status.chat >= 0 {
		tray.chat = ui.chats[status.chat]
		title = tray.chat.Title
	}
	if tray.icon != status.state || tray.menu == "" {
		tray.icon = status.state
		tray.app.SetSystemTrayIcon(trayIconFor(status.state))
	}
	work := status.label(title)
	today := todayText(ui.todayUsage())
	if menu := fmt.Sprint(work, tray.hidden, today); menu != tray.menu {
		tray.menu = menu
		tooltip := "1loop · " + work
		if today != "" {
			tooltip += " · today " + today
		}
		traySetTooltip(tooltip)
		ui.buildTrayMenu(work, status.chat >= 0, today)
	}
}

func (ui *desktopUI) buildTrayMenu(work string, chat bool, today string) {
	tray := ui.tray
	toggle := fyne.NewMenuItem("Hide window", ui.hideWindow)
	if tray.hidden {
		toggle = fyne.NewMenuItem("Show window", ui.showWindow)
	}
	status := fyne.NewMenuItem(work, func() {
		if slices.Contains(ui.chats, tray.chat) {
			ui.openWhereShown(tray.chat)
		}
	})
	status.Disabled = !chat
	newChat := fyne.NewMenuItem("New chat", func() {
		ui.startNew()
		ui.showWindow()
	})
	if today == "" {
		today = "nothing yet"
	}
	spent := fyne.NewMenuItem("Spent today: "+today, func() {
		ui.showUsage()
		ui.showWindow()
	})
	quit := fyne.NewMenuItem("Quit", ui.quit)
	quit.IsQuit = true
	tray.app.SetSystemTrayMenu(fyne.NewMenu("1loop", toggle, fyne.NewMenuItemSeparator(), status, newChat, spent, fyne.NewMenuItemSeparator(), quit))
}

// closeWindow is the window's close button: hidden in the tray when the
// setting allows it and a tray host shows the icon, quit otherwise.
func (ui *desktopUI) closeWindow() {
	if ui.otherWindowShown() { // another 1loop window stays: only this one goes (windows.go)
		ui.closeAlone()
		return
	}
	if ui.closeQuits || ui.tray == nil || !trayHostPresent() {
		ui.quit()
		return
	}
	if ui.trayNoticeShown {
		ui.hideWindow()
		return
	}
	ui.trayNotice()
}

// trayNotice says once, the first time the window is closed, that 1loop
// keeps running in the tray, and lets the user choose to quit instead.
func (ui *desktopUI) trayNotice() {
	text := widget.NewLabel("Closing the window keeps 1loop running in the system tray, so running tasks and the phone connection go on. Click the tray icon to bring the window back. Quit is in the icon's menu, or press Ctrl+Q.\n\nYou can change this in Settings → General.")
	text.Wrapping = fyne.TextWrapWord
	var dialog *sheet
	quit := newRow("Always quit", nil, styleOutline, func() {
		dialog.Hide()
		ui.closeQuits, ui.trayNoticeShown = true, true
		ui.quit()
	})
	keep := newRow("Keep running in the tray", nil, stylePrimary, func() {
		dialog.Hide()
		ui.trayNoticeShown = true
		ui.save()
		ui.hideWindow()
	})
	dialog = ui.modal("1loop stays in the tray", container.NewPadded(text), 460, nil, quit, keep)
}

func (ui *desktopUI) hideWindow() {
	if ui.tray == nil || ui.tray.hidden {
		return
	}
	platform.Devf("ui", "window hidden in the tray")
	ui.tray.hidden = true
	ui.inForeground = false // notifications are for every chat now
	for _, view := range ui.allWindows() {
		view.window.Hide()
	}
	ui.syncTray()
}

// showWindow brings the window back and to the front.
func (ui *desktopUI) showWindow() {
	ui = ui.mainView() // the tray and the single-instance handoff bring back the main window
	if ui.tray != nil && ui.tray.hidden {
		platform.Devf("ui", "window shown from the tray")
		ui.tray.hidden = false
		for _, view := range ui.allWindows()[1:] { // the others hidden with it
			view.window.Show()
		}
		ui.syncTray()
	}
	ui.hiddenAlone = false
	ui.window.Show()
	ui.window.RequestFocus()
}

// trayIcons are drawn once per state.
var trayIcons = map[trayState]fyne.Resource{}

const trayIconSize = 64

// trayIconFor draws the ring of the work's state, like the activity line's:
// the brand ring when idle, the open ring with the thinking dots while
// working, with the approval shield while waiting, and closed in red with a
// cross after a failure.
func trayIconFor(state trayState) fyne.Resource {
	if icon := trayIcons[state]; icon != nil {
		return icon
	}
	out := image.NewNRGBA(image.Rect(0, 0, trayIconSize, trayIconSize))
	layer := func(markup string) {
		draw.Draw(out, out.Bounds(), drawLayer(markup, trayIconSize, 1), image.Point{}, draw.Over)
	}
	openRing := `<path d="M95.24 30.43 A46 46 0 1 1 67.99 14.70" stroke="#FFB000" stroke-width="12" stroke-linecap="round"/><circle cx="83" cy="20.2" r="6" fill="#F4F4F4"/>`
	switch state {
	case trayWorking:
		layer(openRing)
		for _, cx := range []string{"46", "60", "74"} {
			layer(`<circle cx="` + cx + `" cy="60" r="5" fill="#F4F4F4"/>`)
		}
	case trayWaiting:
		layer(openRing)
		layer(`<g ` + glyphStyle + `>` + glyphMarkup("approval") + `</g>`)
	case trayError:
		layer(`<circle cx="60" cy="60" r="46" stroke="#E86A5F" stroke-width="12"/>`)
		layer(`<g ` + glyphStyle + `>` + glyphMarkup("stopped") + `</g>`)
	default:
		brand, err := png.Decode(bytes.NewReader(brandResource("1loop-ring.png").Content()))
		if err == nil {
			xdraw.CatmullRom.Scale(out, out.Bounds(), brand, brand.Bounds(), draw.Src, nil)
		}
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, out)
	trayIcons[state] = fyne.NewStaticResource("tray-"+string(state)+".png", encoded.Bytes())
	return trayIcons[state]
}

// trayHostDecision says whether closing may hide the window: a
// StatusNotifierWatcher must own its name on the bus, and it must have a
// host (the bar or panel that draws the icons) when it says. A watcher
// that cannot tell is trusted, since some answer nothing to the property.
func trayHostDecision(watcher, hostRegistered, hostKnown bool) bool {
	return watcher && (hostRegistered || !hostKnown)
}
