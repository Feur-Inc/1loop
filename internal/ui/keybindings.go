// SPDX-License-Identifier: MPL-2.0

package ui

// The keyboard shortcuts: each is a setting of the shared list
// (internal/settings), with its default key there and the user's own in the
// state file. The page Settings › General › Keyboard shortcuts changes
// them, and so does the model's settings tool, through setShortcut.

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/keys"
	registry "1loop/internal/settings"
)

// shortcutActions is what each shortcut does.
func (ui *desktopUI) shortcutActions() map[string]func() {
	return map[string]func(){
		registry.ShortcutFind:        ui.findInChat,
		registry.ShortcutSearch:      ui.search,
		registry.ShortcutNewChat:     ui.startNew,
		registry.ShortcutNewProject:  ui.addProject,
		registry.ShortcutNewWindow:   func() { ui.openWindow(nil) },
		registry.ShortcutCloseWindow: ui.closePaneOrWindow,
		registry.ShortcutSplitRight:  func() { ui.splitPane(nil, false) },
		registry.ShortcutSplitDown:   func() { ui.splitPane(nil, true) },
		registry.ShortcutPaneLeft:    func() { ui.focusPane(-1, 0) },
		registry.ShortcutPaneRight:   func() { ui.focusPane(1, 0) },
		registry.ShortcutPaneUp:      func() { ui.focusPane(0, -1) },
		registry.ShortcutPaneDown:    func() { ui.focusPane(0, 1) },
		registry.ShortcutSettings: func() {
			if ui.prefs.open {
				ui.prefs.close()
			} else {
				ui.settings()
			}
		},
		registry.ShortcutSidebar: ui.windowOf().toggleSidebar, // the window's (panes.go)
		registry.ShortcutPrompt: func() {
			ui.prefs.close()
			ui.window.Canvas().Focus(ui.prompt)
		},
		registry.ShortcutModel:        func() { ui.model.row.onTap() },
		registry.ShortcutEditor:       func() { ui.editor.toggle() },
		registry.ShortcutStop:         ui.stopRun,
		registry.ShortcutCopyAnswer:   ui.copyLastAnswer,
		registry.ShortcutPreviousChat: func() { ui.stepChat(-1) },
		registry.ShortcutNextChat:     func() { ui.stepChat(1) },
		registry.ShortcutRestart:      ui.restart,
		registry.ShortcutQuit:         ui.quit, // really quits, even when closing hides the window in the tray
	}
}

// shortcutFor is the key of a shortcut: the user's, or else its default.
func (ui *desktopUI) shortcutFor(key string) string {
	if value, ok := ui.shortcutKeys[key]; ok {
		return value
	}
	setting, _ := registry.Lookup(key)
	return setting.Default
}

// shortcutLabel is a shortcut's key as the interface shows it ("Ctrl K"),
// "" when it has none.
func (ui *desktopUI) shortcutLabel(key string) string {
	return chordLabel(ui.shortcutFor(key))
}

func chordLabel(value string) string {
	if value == "" || value == registry.ShortcutOff {
		return ""
	}
	chord, err := keys.Parse(value)
	if err != nil {
		return value
	}
	var parts []string
	for _, modifier := range []struct {
		on   bool
		name string
	}{{chord.Ctrl, "Ctrl"}, {chord.Shift, "Shift"}, {chord.Alt, "Alt"}, {chord.Meta, "Super"}} {
		if modifier.on {
			parts = append(parts, modifier.name)
		}
	}
	key := map[string]string{"ArrowUp": "↑", "ArrowDown": "↓", "ArrowLeft": "←", "ArrowRight": "→", "PageUp": "PgUp", "PageDown": "PgDn", "Backspace": "⌫"}[chord.Key]
	if key == "" {
		key = strings.ToUpper(chord.Key)
	}
	return strings.Join(append(parts, key), " ")
}

// withShortcut adds a shortcut's key to a tooltip: "New chat · Ctrl N".
func (ui *desktopUI) withShortcut(tip, key string) string {
	if label := ui.shortcutLabel(key); label != "" {
		return tip + " · " + label
	}
	return tip
}

// fyneKeys are the named keys of internal/keys as Fyne names them.
var fyneKeys = map[string]fyne.KeyName{
	"Enter": fyne.KeyReturn, "Tab": fyne.KeyTab, "Escape": fyne.KeyEscape, "Backspace": fyne.KeyBackspace,
	"Delete": fyne.KeyDelete, "Insert": fyne.KeyInsert, "Home": fyne.KeyHome, "End": fyne.KeyEnd,
	"PageUp": fyne.KeyPageUp, "PageDown": fyne.KeyPageDown, "ArrowUp": fyne.KeyUp, "ArrowDown": fyne.KeyDown,
	"ArrowLeft": fyne.KeyLeft, "ArrowRight": fyne.KeyRight, "Space": fyne.KeySpace,
	"F1": fyne.KeyF1, "F2": fyne.KeyF2, "F3": fyne.KeyF3, "F4": fyne.KeyF4, "F5": fyne.KeyF5, "F6": fyne.KeyF6,
	"F7": fyne.KeyF7, "F8": fyne.KeyF8, "F9": fyne.KeyF9, "F10": fyne.KeyF10, "F11": fyne.KeyF11, "F12": fyne.KeyF12,
}

// fyneShortcut is a stored shortcut as Fyne reports it.
func fyneShortcut(value string) (*desktop.CustomShortcut, bool) {
	if value == "" || value == registry.ShortcutOff {
		return nil, false
	}
	chord, err := keys.Parse(value)
	if err != nil {
		return nil, false
	}
	name, named := fyneKeys[chord.Key]
	if !named {
		name = fyne.KeyName(strings.ToUpper(chord.Key))
	}
	var modifier fyne.KeyModifier
	for _, held := range []struct {
		on  bool
		key fyne.KeyModifier
	}{{chord.Ctrl, fyne.KeyModifierControl}, {chord.Shift, fyne.KeyModifierShift}, {chord.Alt, fyne.KeyModifierAlt}, {chord.Meta, fyne.KeyModifierSuper}} {
		if held.on {
			modifier |= held.key
		}
	}
	return &desktop.CustomShortcut{KeyName: name, Modifier: modifier}, true
}

// chordOf is a shortcut Fyne reported, written as it is stored; "" for a
// key internal/keys has no name for.
func chordOf(shortcut fyne.Shortcut) string {
	builtIn := map[string]string{"Copy": "Ctrl+C", "Paste": "Ctrl+V", "Cut": "Ctrl+X", "SelectAll": "Ctrl+A", "Undo": "Ctrl+Z", "Redo": "Ctrl+Y"}
	if text, ok := builtIn[shortcut.ShortcutName()]; ok {
		return text
	}
	custom, ok := shortcut.(*desktop.CustomShortcut)
	if !ok {
		return ""
	}
	chord := keys.Chord{
		Ctrl: custom.Modifier&fyne.KeyModifierControl != 0, Shift: custom.Modifier&fyne.KeyModifierShift != 0,
		Alt: custom.Modifier&fyne.KeyModifierAlt != 0, Meta: custom.Modifier&fyne.KeyModifierSuper != 0,
	}
	for name, key := range fyneKeys {
		if key == custom.KeyName {
			chord.Key = name
		}
	}
	if custom.KeyName == fyne.KeyEnter {
		chord.Key = "Enter"
	}
	if chord.Key == "" {
		if len([]rune(string(custom.KeyName))) != 1 {
			return ""
		}
		chord.Key = string(custom.KeyName)
	}
	return registry.FormatChord(chord)
}

// registerShortcuts gives the window the shortcuts' keys, again after one
// changed. The focused message box passes them on too (chatEntry).
func (ui *desktopUI) registerShortcuts() {
	canvas := ui.window.Canvas()
	for _, old := range ui.registered {
		canvas.RemoveShortcut(old)
	}
	ui.registered = ui.registered[:0]
	for _, setting := range registry.Shortcuts() {
		if shortcut, ok := fyneShortcut(ui.shortcutFor(setting.Key)); ok {
			canvas.AddShortcut(shortcut, func(s fyne.Shortcut) { ui.focused().shortcut(s) }) // the pane with the focus (panes.go)
			ui.registered = append(ui.registered, shortcut)
		}
	}
	if ui.newChat != nil {
		ui.newChat.trailing = ui.shortcutLabel(registry.ShortcutNewChat)
		ui.newChat.Refresh()
	}
	if ui.searchRow != nil {
		ui.searchRow.trailing = ui.shortcutLabel(registry.ShortcutSearch)
		ui.searchRow.Refresh()
	}
}

// runShortcut does what the key pressed is bound to; false when it is
// bound to nothing.
func (ui *desktopUI) runShortcut(s fyne.Shortcut) bool {
	chord := chordOf(s)
	if chord == "" {
		return false
	}
	for _, setting := range registry.Shortcuts() {
		if ui.shortcutFor(setting.Key) == chord {
			if action := ui.shortcutActions()[setting.Key]; action != nil {
				action()
				return true
			}
		}
	}
	return false
}

// shortcutOwner is the shortcut other than key that has value, if any.
func (ui *desktopUI) shortcutOwner(key, value string) (registry.Setting, bool) {
	if value == registry.ShortcutOff {
		return registry.Setting{}, false
	}
	for _, setting := range registry.Shortcuts() {
		if setting.Key != key && ui.shortcutFor(setting.Key) == value {
			return setting, true
		}
	}
	return registry.Setting{}, false
}

// setShortcut gives a shortcut a key (as typed, or "off"). A key another
// shortcut has is refused, unless take: that one is then left without.
func (ui *desktopUI) setShortcut(key, text string, take bool) error {
	setting, ok := registry.Lookup(key)
	if !ok || setting.Kind != registry.Shortcut {
		return fmt.Errorf("%s is not a shortcut", key)
	}
	value, err := registry.ShortcutValue(text)
	if err != nil {
		return err
	}
	if owner, taken := ui.shortcutOwner(key, value); taken {
		if !take {
			return fmt.Errorf("%s is already the key of %s (%s): give that one another key, or off, first", value, owner.Title, owner.Key)
		}
		ui.storeShortcut(owner, registry.ShortcutOff)
	}
	ui.storeShortcut(setting, value)
	ui.save()
	ui.registerShortcuts()
	return nil
}

func (ui *desktopUI) storeShortcut(setting registry.Setting, value string) {
	if value == setting.Default {
		delete(ui.shortcutKeys, setting.Key)
	} else {
		ui.shortcutKeys[setting.Key] = value
	}
}

// resetShortcuts gives every shortcut its default key again.
func (ui *desktopUI) resetShortcuts() {
	clear(ui.shortcutKeys)
	ui.save()
	ui.registerShortcuts()
}

// stopRun stops the turn of the chat on screen (the composer's stop button).
func (ui *desktopUI) stopRun() {
	if ui.current != nil && ui.current.server != nil {
		ui.current.server.stop()
		return
	}
	if ui.current != nil && ui.current.run != nil {
		ui.current.run.cancel()
	}
}

// copyLastAnswer copies the newest answer of the chat on screen.
func (ui *desktopUI) copyLastAnswer() {
	chat := ui.current
	if chat == nil {
		return
	}
	for i := len(chat.Items) - 1; i >= 0; i-- {
		if chat.Items[i].Kind == "assistant" && strings.TrimSpace(chat.Items[i].Text) != "" {
			fyne.CurrentApp().Clipboard().SetContent(answerText(chat, i))
			return
		}
	}
}

// stepChat opens the chat above (-1) or below (1) the open one, in the
// sidebar's order; the first one when none is open.
func (ui *desktopUI) stepChat(step int) {
	chats := ui.sideOrder
	if len(chats) == 0 {
		return
	}
	at := -1
	for i, chat := range chats {
		if chat.selected {
			at = i
		}
	}
	next := at + step
	if at < 0 {
		next = 0
	}
	if next < 0 || next >= len(chats) {
		return
	}
	chat := chats[next]
	delete(ui.foldedProjects, chat.group) // show where it is
	chat.open()
	ui.renderChats()
}

// shortcutSettings is the page Settings › General › Keyboard shortcuts.
func (ui *desktopUI) shortcutSettings() fyne.CanvasObject {
	view := ui.prefs
	reset := newRow("Reset all", nil, styleOutline, func() {
		ui.resetShortcuts()
		view.show(registry.ShortcutsPage)
	})
	if len(ui.shortcutKeys) == 0 {
		reset.Hide()
	}
	var rows []fyne.CanvasObject
	for _, setting := range registry.Shortcuts() {
		setting := setting
		current := ui.shortcutFor(setting.Key)
		label := chordLabel(current)
		if label == "" {
			label = "None"
		}
		change := newRow(label, nil, styleOutline, func() { ui.captureShortcut(setting) })
		controls := container.New(layout.NewCustomPaddedHBoxLayout(6))
		if current != setting.Default {
			back := newRow("", iconArrowTurn, styleChip, func() {
				if err := ui.setShortcut(setting.Key, setting.Default, true); err != nil {
					ui.notice("Keyboard shortcut", err.Error())
				}
				view.show(registry.ShortcutsPage)
			})
			back.onHover = func(in bool) {
				if in {
					ui.tips.show(back, "Back to "+chordLabel(setting.Default))
				} else {
					ui.tips.hide()
				}
			}
			controls.Add(back)
		}
		controls.Add(change)
		rows = append(rows, view.mark(setting.Key, settingRow(setting.Title, setting.Description, controls)))
	}
	return container.NewVBox(
		view.pageTitle(registry.ShortcutsPage, "General", container.NewCenter(reset)),
		padded(0, 4, 2, 0, plainFlow("Click a key to change it. The text boxes keep Ctrl+A, C, V, X, Z and Y, and Ctrl+Enter steers a running task.", 13, colMuted)),
		card(rows...),
	)
}

// captureShortcut asks for a shortcut's new key: the next one pressed.
func (ui *desktopUI) captureShortcut(setting registry.Setting) {
	var dialog *sheet
	pressed := textLine("Press the keys…", 18, colText)
	pressed.TextStyle = fyne.TextStyle{Bold: true}
	pressed.Alignment = fyne.TextAlignCenter
	message := widget.NewLabel("With Ctrl, Alt or Super. Escape cancels.")
	message.Wrapping = fyne.TextWrapWord
	message.Alignment = fyne.TextAlignCenter
	var wanted string
	done := func() {
		dialog.Hide()
		if ui.prefs.at(registry.ShortcutsPage) {
			ui.prefs.show(registry.ShortcutsPage)
		}
	}
	take := newRow("Use it here", nil, stylePrimary, func() {
		if err := ui.setShortcut(setting.Key, wanted, true); err != nil {
			message.SetText(err.Error())
			return
		}
		done()
	})
	take.Hide()
	catcher := newKeyCatcher()
	catcher.onChord = func(chord string) {
		take.Hide()
		pressed.Text = chordLabel(chord)
		pressed.Refresh()
		value, err := registry.ShortcutValue(chord)
		if err != nil {
			message.SetText(strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + ".")
			return
		}
		if value == ui.shortcutFor(setting.Key) {
			done()
			return
		}
		if owner, taken := ui.shortcutOwner(setting.Key, value); taken {
			wanted = value
			message.SetText(fmt.Sprintf("%s is the key of “%s”. Use it here, and that one will have none, or press other keys.", chordLabel(value), owner.Title))
			take.Show()
			return
		}
		if err := ui.setShortcut(setting.Key, value, false); err != nil {
			message.SetText(err.Error())
			return
		}
		done()
	}
	catcher.onEscape = func() { dialog.dismiss() }
	none := newRow("No shortcut", nil, styleOutline, func() {
		if err := ui.setShortcut(setting.Key, registry.ShortcutOff, false); err == nil {
			done()
		}
	})
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	body := container.NewStack(catcher, container.NewVBox(
		padded(4, 4, 0, 0, plainFlow(setting.Description, 13, colMuted)),
		padded(18, 10, 0, 0, pressed),
		message))
	dialog = ui.modal(setting.Title, body, 420, nil, none, cancel, take)
	ui.window.Canvas().Focus(catcher)
}

// keyCatcher takes the focus and reports the next shortcut pressed.
type keyCatcher struct {
	widget.BaseWidget
	onChord  func(string)
	onEscape func()
}

func newKeyCatcher() *keyCatcher {
	catcher := &keyCatcher{}
	catcher.ExtendBaseWidget(catcher)
	return catcher
}

func (catcher *keyCatcher) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(layout.NewSpacer())
}

func (catcher *keyCatcher) FocusGained()   {}
func (catcher *keyCatcher) FocusLost()     {}
func (catcher *keyCatcher) TypedRune(rune) {}

func (catcher *keyCatcher) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyEscape:
		catcher.onEscape()
	case fyne.KeyTab, fyne.KeyReturn, fyne.KeyEnter:
	default:
		// A key alone, or with Shift: said to need a modifier.
		if text := chordOf(&desktop.CustomShortcut{KeyName: key.Name}); text != "" {
			catcher.onChord(text)
		}
	}
}

func (catcher *keyCatcher) TypedShortcut(shortcut fyne.Shortcut) {
	if text := chordOf(shortcut); text != "" {
		catcher.onChord(text)
	}
}
