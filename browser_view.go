package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

var (
	iconBrowser = lineIcon("browser", `<circle cx="8" cy="8" r="5.5"/><path d="M2.5 8h11M8 2.5c3 3 3 8 0 11-3-3-3-8 0-11"/>`)
	iconBack    = lineIcon("back", `<path d="m9.5 3.5-4.5 4.5 4.5 4.5"/>`)
	iconForward = lineIcon("forward", `<path d="m6.5 3.5 4.5 4.5-4.5 4.5"/>`)
	iconReload  = lineIcon("reload", `<path d="M12.7 6A5 5 0 1 0 13 9M12.7 2.7V6H9.4"/>`)
	// The agent's pointer: rounded, amber, with a dark edge and a soft
	// shadow; the phone draws the same (android Panel.kt, AgentCursor).
	iconModelCursor = fyne.NewStaticResource("model-browser-cursor.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">`+
		`<path fill="#000000" fill-opacity="0.35" d="M2.00 3.00 L21.50 12.12 Q23.12 12.88 21.38 13.62 L13.75 16.25 L10.38 23.75 Q9.62 25.38 9.00 23.62 Z"/>`+
		`<path fill="#FFB000" stroke="#121212" stroke-width="1.5" stroke-linejoin="round" d="M1.00 1.00 L20.50 10.12 Q22.12 10.88 20.38 11.62 L12.75 14.25 L9.38 21.75 Q8.62 23.38 8.00 21.62 Z"/></svg>`))
)

type browserView struct {
	widget.BaseWidget
	page           *browserPage
	panel          *toolPanel
	chat           *conversation
	content        fyne.CanvasObject
	screen         *browserScreen
	address        *widget.Entry
	dimensions     *widget.Entry
	mode           *picker
	status         *canvas.Text
	console        *fyne.Container
	consoleScroll  *container.Scroll
	consoleVisible bool
	frameMu        sync.Mutex
	latest         image.Image
	framePending   bool
	logPending     bool
}

func (panel *toolPanel) browserTab(chat *conversation, info browserInfo, tabs []panelTab) panelTab {
	return panel.browserTabAttached(chat, info, tabs, "")
}
func (panel *toolPanel) browserTabAttached(chat *conversation, info browserInfo, tabs []panelTab, attach target.ID) panelTab {
	info.PixelRatio = float64(panel.ui.window.Canvas().Scale())
	if panel.ui.browser == nil {
		panel.ui.browser = newBrowserEngine(panel.ui.ctx)
		panel.ui.browser.scale = float64(panel.ui.window.Canvas().Scale())
	}
	var session *browserSession
	for _, tab := range tabs {
		if tab.browser != nil && tab.browser.life.Err() == nil {
			session = tab.browser.session
			break
		}
	}
	if session == nil {
		session = newBrowserSession(panel.ui.browser)
	}
	browser := newBrowserPage(session, info)
	browser.attachTarget = attach
	view := newBrowserView(panel, chat, browser)
	browser.view = view
	browser.onFrame = view.frame
	browser.onChange = func(info browserInfo) {
		fyne.Do(func() {
			if browser.life.Err() != nil {
				return
			}
			view.changed(info)
		})
	}
	browser.onCursor = func(x, y float64) {
		fyne.Do(func() {
			if browser.life.Err() != nil {
				return
			}
			view.screen.glide(x, y)
		})
	}
	browser.onPopup = func(id target.ID, url string) {
		fyne.Do(func() {
			if browser.life.Err() != nil {
				return
			}
			state := panel.stateOf(view.chat)
			tabs := state.tabs
			if view.chat == panel.owner {
				tabs = panel.tabs
			}
			tab := panel.browserTabAttached(view.chat, browserInfo{URL: url, Adaptive: true}, tabs, id)
			tabs = append(tabs, tab)
			state.tabs, state.active, state.open = tabs, len(tabs)-1, true
			if view.chat == panel.owner {
				panel.tabs, panel.active = tabs, len(tabs)-1
				panel.render()
				panel.unfold()
			}
			panel.ui.save()
		})
	}
	browser.onClosed = func() { fyne.Do(func() { panel.removeBrowser(view.chat, browser) }) }
	browser.onLogs = view.logsChanged
	go browser.start()
	workspace := panel.ui.cfg.Workspace
	if chat != nil {
		workspace = chat.Config.Workspace
	}
	return panelTab{kind: "browser", title: "Browser", icon: iconBrowser, browser: browser, workspace: workspace, content: view}
}

func (panel *toolPanel) openBrowser() {
	tab := panel.browserTab(panel.owner, browserInfo{URL: "about:blank", Adaptive: true}, panel.tabs)
	panel.tabs = append(panel.tabs, tab)
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
	panel.ui.window.Canvas().Focus(tab.browser.view.address)
}

func newBrowserView(panel *toolPanel, chat *conversation, browser *browserPage) *browserView {
	view := &browserView{page: browser, panel: panel, chat: chat, status: textLine("Starting browser…", 11, colMuted)}
	view.ExtendBaseWidget(view)
	view.screen = newBrowserScreen(view)
	view.address = widget.NewEntry()
	view.address.PlaceHolder = "Enter a URL"
	view.address.SetText(browser.snapshot().URL)
	navigate := func(action, url string) {
		browser.async(func(ctx context.Context) error { return browser.navigate(ctx, action, url) })
	}
	view.address.OnSubmitted = func(url string) { navigate("navigate", url); panel.ui.window.Canvas().Focus(view.screen) }
	back := newRow("", iconBack, styleWindow, func() { navigate("back", "") })
	forward := newRow("", iconForward, styleWindow, func() { navigate("forward", "") })
	reload := newRow("", iconReload, styleWindow, func() { navigate("reload", "") })
	consoleToggle := newRow("Console", iconTerminal, styleChip, nil)
	consoleToggle.onTap = func() {
		view.consoleVisible = !view.consoleVisible
		consoleToggle.SetSelected(view.consoleVisible)
		if view.consoleVisible {
			view.console.Show()
			view.refreshLogs()
		} else {
			view.console.Hide()
		}
		view.Refresh()
	}
	nav := container.NewBorder(nil, nil, container.NewHBox(back, forward, reload), consoleToggle, view.address)
	view.mode = newPicker(panel.ui.window, "Viewport", styleChip, nil)
	view.mode.Options = []string{"Adaptive", "Desktop", "Tablet", "Mobile", "Custom"}
	view.mode.Details = map[string]string{"Adaptive": "Follow the panel size", "Desktop": "1280 × 800", "Tablet": "768 × 1024", "Mobile": "390 × 844", "Custom": "Choose CSS viewport dimensions"}
	view.dimensions = widget.NewEntry()
	view.dimensions.SetPlaceHolder("width × height")
	view.dimensions.SetText(fmt.Sprintf("%d × %d", browser.snapshot().Width, browser.snapshot().Height))
	view.mode.onChanged = func(value string) {
		width, height, mobile, adaptive := 1280, 800, false, false
		switch value {
		case "Adaptive":
			width, height = int(view.screen.Size().Width), int(view.screen.Size().Height)
			adaptive = true
		case "Tablet":
			width, height = 768, 1024
		case "Mobile":
			width, height, mobile = 390, 844, true
		case "Custom":
			return
		}
		width = max(240, min(3840, width))
		height = max(200, min(2160, height))
		browser.async(func(ctx context.Context) error { return browser.resize(ctx, width, height, mobile, adaptive) })
	}
	apply := func() {
		parts := strings.FieldsFunc(view.dimensions.Text, func(r rune) bool { return r == 'x' || r == '×' || r == ',' || r == ' ' })
		if len(parts) != 2 {
			view.status.Text = "Use width × height"
			view.status.Refresh()
			return
		}
		w, e1 := strconv.Atoi(parts[0])
		h, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil {
			view.status.Text = "Use numeric dimensions"
			view.status.Refresh()
			return
		}
		browser.async(func(ctx context.Context) error { return browser.resize(ctx, w, h, false, false) })
	}
	view.dimensions.OnSubmitted = func(string) { apply() }
	viewport := container.NewBorder(nil, nil, view.mode.row, newRow("Apply", nil, styleOutline, apply), view.dimensions)
	logs := container.NewVBox()
	view.consoleScroll = container.NewVScroll(logs)
	js := widget.NewMultiLineEntry()
	js.SetMinRowsVisible(2)
	js.SetPlaceHolder("JavaScript expression")
	execute := func() {
		expression := strings.TrimSpace(js.Text)
		if expression == "" {
			return
		}
		browser.addLog("command", expression, "JavaScript")
		browser.async(func(ctx context.Context) error {
			value, err := browser.evaluate(ctx, expression)
			if err == nil {
				browser.addLog("result", string(value), "JavaScript")
			}
			return err
		})
	}
	run := newRow("Run JS", iconSend, styleOutline, execute)
	view.console = container.NewBorder(nil, container.NewBorder(nil, nil, nil, run, js), nil, nil, view.consoleScroll)
	view.console.Hide()
	view.content = container.NewBorder(padded(6, 6, 8, 8, container.NewVBox(nav, viewport)), padded(4, 5, 10, 10, view.status), nil, nil, container.New(browserBodyLayout{view}, view.screen, view.console))
	view.changed(browser.snapshot())
	return view
}
func (view *browserView) MinSize() fyne.Size { return fyne.NewSize(240, 200) }

func (view *browserView) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(view.content)
}
func (view *browserView) frame(img image.Image) {
	view.frameMu.Lock()
	view.latest = img
	if view.framePending {
		view.frameMu.Unlock()
		return
	}
	view.framePending = true
	view.frameMu.Unlock()
	fyne.Do(func() {
		view.frameMu.Lock()
		latest := view.latest
		view.framePending = false
		view.frameMu.Unlock()
		if view.page.life.Err() != nil {
			return
		}
		resized := view.screen.image.Image == nil || view.screen.image.Image.Bounds() != latest.Bounds()
		view.screen.image.Image = latest
		view.screen.image.Refresh()
		if resized {
			view.screen.Refresh()
		}
	})
}
func (view *browserView) changed(info browserInfo) {
	if view.panel.ui.window.Canvas().Focused() != view.address {
		view.address.SetText(info.URL)
	}
	if view.panel.ui.window.Canvas().Focused() != view.dimensions {
		view.dimensions.SetText(fmt.Sprintf("%d × %d", info.Width, info.Height))
	}
	selected := "Custom"
	switch {
	case info.Adaptive:
		selected = "Adaptive"
	case info.Mobile && info.Width == 390 && info.Height == 844:
		selected = "Mobile"
	case info.Width == 768 && info.Height == 1024:
		selected = "Tablet"
	case info.Width == 1280 && info.Height == 800:
		selected = "Desktop"
	}
	// Update the picker display without invoking a second resize.
	view.mode.Selected = selected
	view.mode.row.SetText(selected)
	if info.Error != "" {
		view.status.Text = info.Error
		view.status.Color = colDanger
	} else {
		view.status.Text = fmt.Sprintf("%d × %d CSS px", info.Width, info.Height)
		view.status.Color = colMuted
	}
	view.status.Refresh()
	view.screen.Refresh()
	tabs := view.panel.tabs
	if view.chat != view.panel.owner && view.chat != nil && view.chat.panel != nil {
		tabs = view.chat.panel.tabs
	}
	for i := range tabs {
		if tabs[i].browser == view.page {
			tabs[i].title = info.Title
			if tabs[i].title == "" {
				tabs[i].title = "Browser"
			}
		}
	}
	if view.chat == view.panel.owner {
		view.panel.renderStrip()
	}
	view.panel.ui.save()
}
func (view *browserView) logsChanged() {
	view.frameMu.Lock()
	if view.logPending {
		view.frameMu.Unlock()
		return
	}
	view.logPending = true
	view.frameMu.Unlock()
	time.AfterFunc(100*time.Millisecond, func() {
		fyne.Do(func() {
			view.frameMu.Lock()
			view.logPending = false
			view.frameMu.Unlock()
			if view.page.life.Err() == nil && view.consoleVisible {
				view.refreshLogs()
			}
		})
	})
}
func (view *browserView) refreshLogs() {
	entries := view.page.logSnapshot(0)
	rows := []fyne.CanvasObject{}
	for _, entry := range entries {
		ink := colMuted
		if entry.Level == "error" {
			ink = colDanger
		} else if entry.Level == "warn" || entry.Level == "warning" {
			ink = colAmber
		} else if entry.Level == "result" {
			ink = colText
		}
		rows = append(rows, newFlowText([]span{{text: entry.Time + "  " + entry.Level + "  " + entry.Text, code: false}}, 11.5, ink))
	}
	if len(rows) == 0 {
		rows = append(rows, note("No browser logs yet."))
	}
	view.consoleScroll.Content = container.NewVBox(rows...)
	view.consoleScroll.Refresh()
	view.consoleScroll.ScrollToBottom()
}

type browserBodyLayout struct{ view *browserView }

func (layoutSpec browserBodyLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(240, 200)
}
func (layoutSpec browserBodyLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	height := float32(0)
	if layoutSpec.view.consoleVisible {
		height = fyne.Min(300, size.Height*.45)
		objects[1].Move(fyne.NewPos(0, size.Height-height))
		objects[1].Resize(fyne.NewSize(size.Width, height))
	}
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(size.Width, size.Height-height))
}

type browserScreen struct {
	widget.BaseWidget
	view             *browserView
	image            *canvas.Image
	background       *canvas.Rectangle
	modelCursor      *fyne.Container // the agent's pointer and its name
	cursorX, cursorY float64
	cursorGlide      *fyne.Animation // the pointer on its way to where the agent acts
	position         fyne.Position
	rendered         fyne.Size
	resizeTimer      *time.Timer
	buttons          int64
	modifiers        int64
}

func newBrowserScreen(view *browserView) *browserScreen {
	screen := &browserScreen{view: view, image: canvas.NewImageFromImage(nil), background: canvas.NewRectangle(color.NRGBA{R: 18, G: 18, B: 18, A: 255}), modelCursor: newAgentCursor()}
	screen.image.FillMode = canvas.ImageFillStretch
	screen.modelCursor.Hide()
	screen.ExtendBaseWidget(screen)
	return screen
}
func (screen *browserScreen) CreateRenderer() fyne.WidgetRenderer {
	return &browserScreenRenderer{screen: screen, objects: []fyne.CanvasObject{screen.background, screen.image, screen.modelCursor}}
}

type browserScreenRenderer struct {
	screen  *browserScreen
	objects []fyne.CanvasObject
}

func (renderer *browserScreenRenderer) MinSize() fyne.Size           { return fyne.NewSize(240, 200) }
func (renderer *browserScreenRenderer) Objects() []fyne.CanvasObject { return renderer.objects }
func (renderer *browserScreenRenderer) Destroy() {
	if renderer.screen.resizeTimer != nil {
		renderer.screen.resizeTimer.Stop()
	}
}
func (renderer *browserScreenRenderer) Refresh() {
	renderer.Layout(renderer.screen.Size())
	canvas.Refresh(renderer.screen)
}
func (renderer *browserScreenRenderer) Layout(size fyne.Size) {
	screen := renderer.screen
	info := screen.view.page.snapshot()
	screen.background.Resize(size)
	scale := fyne.Min(size.Width/float32(info.Width), size.Height/float32(info.Height))
	screen.rendered = fyne.NewSize(float32(info.Width)*scale, float32(info.Height)*scale)
	screen.position = fyne.NewPos((size.Width-screen.rendered.Width)/2, (size.Height-screen.rendered.Height)/2)
	screen.image.Move(screen.position)
	screen.image.Resize(screen.rendered)
	if screen.cursorGlide == nil {
		screen.modelCursor.Move(screen.cursorTarget(scale))
	}
	width, height := max(240, min(3840, int(size.Width))), max(200, min(2160, int(size.Height)))
	ratio := float64(screen.view.panel.ui.window.Canvas().Scale())
	if ratio != info.PixelRatio || (info.Adaptive && (width != info.Width || height != info.Height)) {
		if screen.resizeTimer != nil {
			screen.resizeTimer.Stop()
		}
		screen.resizeTimer = time.AfterFunc(250*time.Millisecond, func() {
			if screen.view.page.life.Err() == nil {
				screen.view.page.async(func(ctx context.Context) error {
					latest := screen.view.page.snapshot()
					if !latest.Adaptive {
						width, height = latest.Width, latest.Height
					}
					return screen.view.page.resize(ctx, width, height, latest.Mobile, latest.Adaptive, ratio)
				})
			}
		})
	}
}
func (screen *browserScreen) coords(pos fyne.Position) (float64, float64, bool) {
	if screen.rendered.Width <= 0 || screen.rendered.Height <= 0 {
		return 0, 0, false
	}
	info := screen.view.page.snapshot()
	x := float64((pos.X-screen.position.X)/screen.rendered.Width) * float64(info.Width)
	y := float64((pos.Y-screen.position.Y)/screen.rendered.Height) * float64(info.Height)
	return x, y, x >= 0 && y >= 0 && x < float64(info.Width) && y < float64(info.Height)
}
func (screen *browserScreen) FocusGained()     {}
func (screen *browserScreen) FocusLost()       { screen.modifiers = 0 }
func (screen *browserScreen) AcceptsTab() bool { return true }
func (screen *browserScreen) TypedRune(r rune) {
	if screen.modifiers&7 != 0 {
		return
	}
	text := string(r)
	screen.view.page.input(input.InsertText(text))
}
func browserKey(key fyne.KeyName) string {
	switch key {
	case fyne.KeyReturn, fyne.KeyEnter:
		return kb.Enter
	case fyne.KeyTab:
		return kb.Tab
	case fyne.KeyBackspace:
		return kb.Backspace
	case fyne.KeyDelete:
		return kb.Delete
	case fyne.KeyEscape:
		return kb.Escape
	case fyne.KeyLeft:
		return kb.ArrowLeft
	case fyne.KeyRight:
		return kb.ArrowRight
	case fyne.KeyUp:
		return kb.ArrowUp
	case fyne.KeyDown:
		return kb.ArrowDown
	case fyne.KeyHome:
		return kb.Home
	case fyne.KeyEnd:
		return kb.End
	case fyne.KeyPageUp:
		return kb.PageUp
	case fyne.KeyPageDown:
		return kb.PageDown
	}
	return ""
}
func (screen *browserScreen) TypedKey(e *fyne.KeyEvent) {
	if key := browserKey(e.Name); key != "" {
		modifiers := screen.modifiers
		screen.view.page.input(chromedp.KeyEvent(key, func(p *input.DispatchKeyEventParams) *input.DispatchKeyEventParams {
			p.Modifiers = input.Modifier(modifiers)
			return p
		}))
	}
}
func (screen *browserScreen) TypedShortcut(shortcut fyne.Shortcut) {
	switch shortcut.(type) {
	case *fyne.ShortcutPaste:
		screen.view.page.input(input.InsertText(screen.view.panel.ui.window.Clipboard().Content()))
	case *fyne.ShortcutCopy, *fyne.ShortcutCut:
		_, cut := shortcut.(*fyne.ShortcutCut)
		screen.view.page.async(func(ctx context.Context) error {
			value, err := screen.view.page.evaluate(ctx, `(()=>{const e=document.activeElement;if(e&&typeof e.selectionStart==='number')return e.value.slice(e.selectionStart,e.selectionEnd);return window.getSelection()?.toString()||''})()`)
			if err != nil {
				return err
			}
			var text string
			if json.Unmarshal(value, &text) != nil {
				return nil
			}
			fyne.Do(func() { screen.view.panel.ui.window.Clipboard().SetContent(text) })
			if cut {
				_, err = screen.view.page.evaluate(ctx, `(()=>{const e=document.activeElement;if(e&&typeof e.setRangeText==='function'){e.setRangeText('',e.selectionStart,e.selectionEnd,'end');e.dispatchEvent(new Event('input',{bubbles:true}))}})()`)
			}
			return err
		})
	case *fyne.ShortcutSelectAll:
		screen.view.page.input(input.DispatchKeyEvent(input.KeyDown).WithKey("a").WithModifiers(2).WithWindowsVirtualKeyCode(65))
		screen.view.page.input(input.DispatchKeyEvent(input.KeyUp).WithKey("a").WithModifiers(2).WithWindowsVirtualKeyCode(65))
	}
}
func (screen *browserScreen) MouseIn(e *desktop.MouseEvent) { screen.MouseMoved(e) }
func (screen *browserScreen) MouseOut()                     {}
func (screen *browserScreen) MouseMoved(e *desktop.MouseEvent) {
	x, y, ok := screen.coords(e.Position)
	if !ok {
		return
	}
	screen.view.page.pointer(x, y, screen.buttons)
}
func (screen *browserScreen) MouseDown(e *desktop.MouseEvent) {
	screen.view.panel.ui.window.Canvas().Focus(screen)
	screen.modelCursor.Hide()
	screen.buttons = 1
	screen.mouse(e, input.MousePressed)
}
func (screen *browserScreen) MouseUp(e *desktop.MouseEvent) {
	screen.mouse(e, input.MouseReleased)
	screen.buttons = 0
}
func (screen *browserScreen) mouse(e *desktop.MouseEvent, kind input.MouseType) {
	x, y, ok := screen.coords(e.Position)
	if !ok {
		return
	}
	button := input.Left
	if e.Button == desktop.MouseButtonSecondary {
		button = input.Right
	}
	if e.Button == desktop.MouseButtonTertiary {
		button = input.Middle
	}
	screen.view.page.input(input.DispatchMouseEvent(kind, x, y).WithButton(button).WithClickCount(1))
}
func (screen *browserScreen) Scrolled(e *fyne.ScrollEvent) {
	x, y, ok := screen.coords(e.Position)
	if !ok {
		return
	}
	screen.view.page.input(input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(float64(-e.Scrolled.DX) * 20).WithDeltaY(float64(-e.Scrolled.DY) * 20))
}

func (panel *toolPanel) syncBrowsers() {
	var active *browserPage
	if panel.target > 0 && panel.active >= 0 && panel.active < len(panel.tabs) {
		active = panel.tabs[panel.active].browser
	}
	visit := func(tabs []panelTab) {
		for _, tab := range tabs {
			if tab.browser != nil {
				tab.browser.setVisible(tab.browser == active)
			}
		}
	}
	visit(panel.tabs)
	visit(panel.home.tabs)
	for _, chat := range panel.ui.chats {
		if chat.panel != nil {
			visit(chat.panel.tabs)
		}
	}
}

func (panel *toolPanel) removeBrowser(chat *conversation, browser *browserPage) {
	if chat == panel.owner {
		for i, tab := range panel.tabs {
			if tab.browser == browser {
				panel.close(i)
				return
			}
		}
		return
	}
	state := panel.stateOf(chat)
	for i, tab := range state.tabs {
		if tab.browser == browser {
			tab.dispose()
			state.tabs = append(state.tabs[:i], state.tabs[i+1:]...)
			state.active = min(state.active, len(state.tabs)-1)
			if chat != nil {
				chat.Panel = state.saved()
			}
			panel.ui.save()
			return
		}
	}
}

func (screen *browserScreen) KeyDown(e *fyne.KeyEvent) {
	switch e.Name {
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		screen.modifiers |= 2
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		screen.modifiers |= 1
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		screen.modifiers |= 8
	case desktop.KeySuperLeft, desktop.KeySuperRight:
		screen.modifiers |= 4
	}
}
func (screen *browserScreen) KeyUp(e *fyne.KeyEvent) {
	switch e.Name {
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		screen.modifiers &^= 2
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		screen.modifiers &^= 1
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		screen.modifiers &^= 8
	case desktop.KeySuperLeft, desktop.KeySuperRight:
		screen.modifiers &^= 4
	}
}

// newAgentCursor builds the agent's pointer with its name tag beside it,
// like a collaborator's cursor. Its tip is the container's origin.
func newAgentCursor() *fyne.Container {
	arrow := canvas.NewImageFromResource(iconModelCursor)
	arrow.Move(fyne.NewPos(-1, -1))
	arrow.Resize(fyne.NewSize(26, 26))
	label := canvas.NewText("1loop", color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff})
	label.TextSize, label.TextStyle = 9.5, fyne.TextStyle{Bold: true}
	size := fyne.MeasureText(label.Text, label.TextSize, label.TextStyle)
	pill := canvas.NewRectangle(colAmber)
	pill.CornerRadius = (size.Height + 3) / 2
	pill.Move(fyne.NewPos(17, 21))
	pill.Resize(fyne.NewSize(size.Width+12, size.Height+3))
	label.Move(fyne.NewPos(23, 22.5))
	label.Resize(size)
	cursor := container.NewWithoutLayout(arrow, pill, label)
	cursor.Resize(fyne.NewSize(28+size.Width, 23+size.Height))
	cursor.Hide()
	return cursor
}

func (screen *browserScreen) cursorTarget(scale float32) fyne.Position {
	return screen.position.Add(fyne.NewPos(float32(screen.cursorX)*scale, float32(screen.cursorY)*scale))
}

// glide moves the agent's pointer to a point of the page, smoothly, as a
// hand would; the first time it appears there.
func (screen *browserScreen) glide(x, y float64) {
	screen.cursorX, screen.cursorY = x, y
	info := screen.view.page.snapshot()
	scale := fyne.Min(screen.Size().Width/float32(info.Width), screen.Size().Height/float32(info.Height))
	to := screen.cursorTarget(scale)
	if screen.cursorGlide != nil {
		screen.cursorGlide.Stop()
		screen.cursorGlide = nil
	}
	if !screen.modelCursor.Visible() {
		screen.modelCursor.Move(to)
		screen.modelCursor.Show()
		return
	}
	from := screen.modelCursor.Position()
	var glide *fyne.Animation
	glide = fyne.NewAnimation(260*time.Millisecond, func(t float32) {
		screen.modelCursor.Move(fyne.NewPos(from.X+(to.X-from.X)*t, from.Y+(to.Y-from.Y)*t))
		if t == 1 && screen.cursorGlide == glide {
			screen.cursorGlide = nil
		}
	})
	glide.Curve = fyne.AnimationEaseOut
	screen.cursorGlide = glide
	glide.Start()
}
