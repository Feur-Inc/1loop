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
	iconBrowser     = lineIcon("browser", `<circle cx="8" cy="8" r="5.5"/><path d="M2.5 8h11M8 2.5c3 3 3 8 0 11-3-3-3-8 0-11"/>`)
	iconBack        = lineIcon("back", `<path d="m9.5 3.5-4.5 4.5 4.5 4.5"/>`)
	iconForward     = lineIcon("forward", `<path d="m6.5 3.5 4.5 4.5-4.5 4.5"/>`)
	iconReload      = lineIcon("reload", `<path d="M12.7 6A5 5 0 1 0 13 9M12.7 2.7V6H9.4"/>`)
	iconModelCursor = fyne.NewStaticResource("model-browser-cursor.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 28"><path d="M2 2v20l6-5 5 9 4-2-5-9h9z" fill="#ffb700" stroke="#161616" stroke-width="2"/></svg>`))
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

func (p *toolPanel) browserTab(chat *conversation, info browserInfo, tabs []panelTab) panelTab {
	return p.browserTabAttached(chat, info, tabs, "")
}
func (p *toolPanel) browserTabAttached(chat *conversation, info browserInfo, tabs []panelTab, attach target.ID) panelTab {
	if p.u.browser == nil {
		p.u.browser = newBrowserEngine(p.u.ctx)
	}
	var session *browserSession
	for _, tab := range tabs {
		if tab.browser != nil && tab.browser.life.Err() == nil {
			session = tab.browser.session
			break
		}
	}
	if session == nil {
		session = newBrowserSession(p.u.browser)
	}
	b := newBrowserPage(session, info)
	b.attachTarget = attach
	v := newBrowserView(p, chat, b)
	b.view = v
	b.onFrame = v.frame
	b.onChange = func(info browserInfo) {
		fyne.Do(func() {
			if b.life.Err() != nil {
				return
			}
			v.changed(info)
		})
	}
	b.onCursor = func(x, y float64) {
		fyne.Do(func() {
			if b.life.Err() != nil {
				return
			}
			v.screen.cursorX, v.screen.cursorY = x, y
			v.screen.modelCursor.Show()
			v.screen.Refresh()
		})
	}
	b.onPopup = func(id target.ID, url string) {
		fyne.Do(func() {
			if b.life.Err() != nil {
				return
			}
			state := p.stateOf(v.chat)
			tabs := state.tabs
			if v.chat == p.owner {
				tabs = p.tabs
			}
			tab := p.browserTabAttached(v.chat, browserInfo{URL: url, Adaptive: true}, tabs, id)
			tabs = append(tabs, tab)
			state.tabs, state.active, state.open = tabs, len(tabs)-1, true
			if v.chat == p.owner {
				p.tabs, p.active = tabs, len(tabs)-1
				p.render()
				p.unfold()
			}
			p.u.save()
		})
	}
	b.onClosed = func() { fyne.Do(func() { p.removeBrowser(v.chat, b) }) }
	b.onLogs = v.logsChanged
	go b.start()
	workspace := p.u.cfg.Workspace
	if chat != nil {
		workspace = chat.Config.Workspace
	}
	return panelTab{kind: "browser", title: "Browser", icon: iconBrowser, browser: b, workspace: workspace, content: v}
}

func (p *toolPanel) openBrowser() {
	tab := p.browserTab(p.owner, browserInfo{URL: "about:blank", Adaptive: true}, p.tabs)
	p.tabs = append(p.tabs, tab)
	p.choose(len(p.tabs) - 1)
	p.unfold()
	p.u.window.Canvas().Focus(tab.browser.view.address)
}

func newBrowserView(p *toolPanel, chat *conversation, b *browserPage) *browserView {
	v := &browserView{page: b, panel: p, chat: chat, status: textLine("Starting browser…", 11, colMuted)}
	v.ExtendBaseWidget(v)
	v.screen = newBrowserScreen(v)
	v.address = widget.NewEntry()
	v.address.PlaceHolder = "Enter a URL"
	v.address.SetText(b.snapshot().URL)
	navigate := func(action, url string) {
		b.async(func(ctx context.Context) error { return b.navigate(ctx, action, url) })
	}
	v.address.OnSubmitted = func(url string) { navigate("navigate", url); p.u.window.Canvas().Focus(v.screen) }
	back := newRow("", iconBack, styleWindow, func() { navigate("back", "") })
	forward := newRow("", iconForward, styleWindow, func() { navigate("forward", "") })
	reload := newRow("", iconReload, styleWindow, func() { navigate("reload", "") })
	consoleToggle := newRow("Console", iconTerminal, styleChip, nil)
	consoleToggle.onTap = func() {
		v.consoleVisible = !v.consoleVisible
		consoleToggle.SetSelected(v.consoleVisible)
		if v.consoleVisible {
			v.console.Show()
			v.refreshLogs()
		} else {
			v.console.Hide()
		}
		v.Refresh()
	}
	nav := container.NewBorder(nil, nil, container.NewHBox(back, forward, reload), consoleToggle, v.address)
	v.mode = newPicker(p.u.window, "Viewport", styleChip, nil)
	v.mode.Options = []string{"Adaptive", "Desktop", "Tablet", "Mobile", "Custom"}
	v.mode.Details = map[string]string{"Adaptive": "Follow the panel size", "Desktop": "1280 × 800", "Tablet": "768 × 1024", "Mobile": "390 × 844", "Custom": "Choose CSS viewport dimensions"}
	v.dimensions = widget.NewEntry()
	v.dimensions.SetPlaceHolder("width × height")
	v.dimensions.SetText(fmt.Sprintf("%d × %d", b.snapshot().Width, b.snapshot().Height))
	v.mode.onChanged = func(value string) {
		width, height, mobile, adaptive := 1280, 800, false, false
		switch value {
		case "Adaptive":
			width, height = int(v.screen.Size().Width), int(v.screen.Size().Height)
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
		b.async(func(ctx context.Context) error { return b.resize(ctx, width, height, mobile, adaptive) })
	}
	apply := func() {
		parts := strings.FieldsFunc(v.dimensions.Text, func(r rune) bool { return r == 'x' || r == '×' || r == ',' || r == ' ' })
		if len(parts) != 2 {
			v.status.Text = "Use width × height"
			v.status.Refresh()
			return
		}
		w, e1 := strconv.Atoi(parts[0])
		h, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil {
			v.status.Text = "Use numeric dimensions"
			v.status.Refresh()
			return
		}
		b.async(func(ctx context.Context) error { return b.resize(ctx, w, h, false, false) })
	}
	v.dimensions.OnSubmitted = func(string) { apply() }
	viewport := container.NewBorder(nil, nil, v.mode.row, newRow("Apply", nil, styleOutline, apply), v.dimensions)
	logs := container.NewVBox()
	v.consoleScroll = container.NewVScroll(logs)
	js := widget.NewMultiLineEntry()
	js.SetMinRowsVisible(2)
	js.SetPlaceHolder("JavaScript expression")
	execute := func() {
		expression := strings.TrimSpace(js.Text)
		if expression == "" {
			return
		}
		b.addLog("command", expression, "JavaScript")
		b.async(func(ctx context.Context) error {
			value, err := b.evaluate(ctx, expression)
			if err == nil {
				b.addLog("result", string(value), "JavaScript")
			}
			return err
		})
	}
	run := newRow("Run JS", iconSend, styleOutline, execute)
	v.console = container.NewBorder(nil, container.NewBorder(nil, nil, nil, run, js), nil, nil, v.consoleScroll)
	v.console.Hide()
	v.content = container.NewBorder(padded(6, 6, 8, 8, container.NewVBox(nav, viewport)), padded(4, 5, 10, 10, v.status), nil, nil, container.New(browserBodyLayout{v}, v.screen, v.console))
	v.changed(b.snapshot())
	return v
}
func (v *browserView) MinSize() fyne.Size { return fyne.NewSize(240, 200) }

func (v *browserView) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(v.content)
}
func (v *browserView) frame(img image.Image) {
	v.frameMu.Lock()
	v.latest = img
	if v.framePending {
		v.frameMu.Unlock()
		return
	}
	v.framePending = true
	v.frameMu.Unlock()
	fyne.Do(func() {
		v.frameMu.Lock()
		latest := v.latest
		v.framePending = false
		v.frameMu.Unlock()
		if v.page.life.Err() != nil {
			return
		}
		v.screen.image.Image = latest
		v.screen.image.Refresh()
		v.screen.Refresh()
	})
}
func (v *browserView) changed(info browserInfo) {
	if v.panel.u.window.Canvas().Focused() != v.address {
		v.address.SetText(info.URL)
	}
	if v.panel.u.window.Canvas().Focused() != v.dimensions {
		v.dimensions.SetText(fmt.Sprintf("%d × %d", info.Width, info.Height))
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
	v.mode.Selected = selected
	v.mode.row.SetText(selected)
	if info.Error != "" {
		v.status.Text = info.Error
		v.status.Color = colDanger
	} else {
		v.status.Text = fmt.Sprintf("%d × %d CSS px", info.Width, info.Height)
		v.status.Color = colMuted
	}
	v.status.Refresh()
	v.screen.Refresh()
	tabs := v.panel.tabs
	if v.chat != v.panel.owner && v.chat != nil && v.chat.panel != nil {
		tabs = v.chat.panel.tabs
	}
	for i := range tabs {
		if tabs[i].browser == v.page {
			tabs[i].title = info.Title
			if tabs[i].title == "" {
				tabs[i].title = "Browser"
			}
		}
	}
	if v.chat == v.panel.owner {
		v.panel.renderStrip()
	}
	v.panel.u.save()
}
func (v *browserView) logsChanged() {
	v.frameMu.Lock()
	if v.logPending {
		v.frameMu.Unlock()
		return
	}
	v.logPending = true
	v.frameMu.Unlock()
	time.AfterFunc(100*time.Millisecond, func() {
		fyne.Do(func() {
			v.frameMu.Lock()
			v.logPending = false
			v.frameMu.Unlock()
			if v.page.life.Err() == nil && v.consoleVisible {
				v.refreshLogs()
			}
		})
	})
}
func (v *browserView) refreshLogs() {
	entries := v.page.logSnapshot(0)
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
	v.consoleScroll.Content = container.NewVBox(rows...)
	v.consoleScroll.Refresh()
	v.consoleScroll.ScrollToBottom()
}

type browserBodyLayout struct{ view *browserView }

func (l browserBodyLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(240, 200) }
func (l browserBodyLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	height := float32(0)
	if l.view.consoleVisible {
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
	modelCursor      *canvas.Image
	cursorX, cursorY float64
	position         fyne.Position
	rendered         fyne.Size
	resizeTimer      *time.Timer
	buttons          int64
	modifiers        int64
}

func newBrowserScreen(v *browserView) *browserScreen {
	s := &browserScreen{view: v, image: canvas.NewImageFromImage(nil), background: canvas.NewRectangle(color.NRGBA{R: 18, G: 18, B: 18, A: 255}), modelCursor: canvas.NewImageFromResource(iconModelCursor)}
	s.image.FillMode = canvas.ImageFillStretch
	s.modelCursor.Hide()
	s.ExtendBaseWidget(s)
	return s
}
func (s *browserScreen) CreateRenderer() fyne.WidgetRenderer {
	return &browserScreenRenderer{screen: s, objects: []fyne.CanvasObject{s.background, s.image, s.modelCursor}}
}

type browserScreenRenderer struct {
	screen  *browserScreen
	objects []fyne.CanvasObject
}

func (r *browserScreenRenderer) MinSize() fyne.Size           { return fyne.NewSize(240, 200) }
func (r *browserScreenRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *browserScreenRenderer) Destroy() {
	if r.screen.resizeTimer != nil {
		r.screen.resizeTimer.Stop()
	}
}
func (r *browserScreenRenderer) Refresh() { r.Layout(r.screen.Size()); canvas.Refresh(r.screen) }
func (r *browserScreenRenderer) Layout(size fyne.Size) {
	s := r.screen
	info := s.view.page.snapshot()
	s.background.Resize(size)
	scale := fyne.Min(size.Width/float32(info.Width), size.Height/float32(info.Height))
	s.rendered = fyne.NewSize(float32(info.Width)*scale, float32(info.Height)*scale)
	s.position = fyne.NewPos((size.Width-s.rendered.Width)/2, (size.Height-s.rendered.Height)/2)
	s.image.Move(s.position)
	s.image.Resize(s.rendered)
	s.modelCursor.Move(s.position.Add(fyne.NewPos(float32(s.cursorX)*scale, float32(s.cursorY)*scale)))
	s.modelCursor.Resize(fyne.NewSize(24, 28))
	width, height := max(240, min(3840, int(size.Width))), max(200, min(2160, int(size.Height)))
	if info.Adaptive && (width != info.Width || height != info.Height) {
		if s.resizeTimer != nil {
			s.resizeTimer.Stop()
		}
		s.resizeTimer = time.AfterFunc(250*time.Millisecond, func() {
			if s.view.page.life.Err() == nil && s.view.page.snapshot().Adaptive {
				s.view.page.async(func(ctx context.Context) error {
					return s.view.page.resize(ctx, width, height, s.view.page.snapshot().Mobile, true)
				})
			}
		})
	}
}
func (s *browserScreen) coords(pos fyne.Position) (float64, float64, bool) {
	if s.rendered.Width <= 0 || s.rendered.Height <= 0 {
		return 0, 0, false
	}
	info := s.view.page.snapshot()
	x := float64((pos.X-s.position.X)/s.rendered.Width) * float64(info.Width)
	y := float64((pos.Y-s.position.Y)/s.rendered.Height) * float64(info.Height)
	return x, y, x >= 0 && y >= 0 && x < float64(info.Width) && y < float64(info.Height)
}
func (s *browserScreen) FocusGained()     {}
func (s *browserScreen) FocusLost()       { s.modifiers = 0 }
func (s *browserScreen) AcceptsTab() bool { return true }
func (s *browserScreen) TypedRune(r rune) {
	if s.modifiers&7 != 0 {
		return
	}
	text := string(r)
	s.view.page.async(func(ctx context.Context) error { return s.view.page.run(ctx, input.InsertText(text)) })
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
func (s *browserScreen) TypedKey(e *fyne.KeyEvent) {
	if key := browserKey(e.Name); key != "" {
		modifiers := s.modifiers
		s.view.page.async(func(ctx context.Context) error {
			return s.view.page.run(ctx, chromedp.KeyEvent(key, func(p *input.DispatchKeyEventParams) *input.DispatchKeyEventParams {
				p.Modifiers = input.Modifier(modifiers)
				return p
			}))
		})
	}
}
func (s *browserScreen) TypedShortcut(shortcut fyne.Shortcut) {
	switch shortcut.(type) {
	case *fyne.ShortcutPaste:
		text := s.view.panel.u.window.Clipboard().Content()
		s.view.page.async(func(ctx context.Context) error { return s.view.page.run(ctx, input.InsertText(text)) })
	case *fyne.ShortcutCopy, *fyne.ShortcutCut:
		_, cut := shortcut.(*fyne.ShortcutCut)
		s.view.page.async(func(ctx context.Context) error {
			value, err := s.view.page.evaluate(ctx, `(()=>{const e=document.activeElement;if(e&&typeof e.selectionStart==='number')return e.value.slice(e.selectionStart,e.selectionEnd);return window.getSelection()?.toString()||''})()`)
			if err != nil {
				return err
			}
			var text string
			if json.Unmarshal(value, &text) != nil {
				return nil
			}
			fyne.Do(func() { s.view.panel.u.window.Clipboard().SetContent(text) })
			if cut {
				_, err = s.view.page.evaluate(ctx, `(()=>{const e=document.activeElement;if(e&&typeof e.setRangeText==='function'){e.setRangeText('',e.selectionStart,e.selectionEnd,'end');e.dispatchEvent(new Event('input',{bubbles:true}))}})()`)
			}
			return err
		})
	case *fyne.ShortcutSelectAll:
		s.view.page.async(func(ctx context.Context) error {
			return s.view.page.run(ctx, input.DispatchKeyEvent(input.KeyDown).WithKey("a").WithModifiers(2).WithWindowsVirtualKeyCode(65), input.DispatchKeyEvent(input.KeyUp).WithKey("a").WithModifiers(2).WithWindowsVirtualKeyCode(65))
		})
	}
}
func (s *browserScreen) MouseIn(e *desktop.MouseEvent) { s.MouseMoved(e) }
func (s *browserScreen) MouseOut()                     {}
func (s *browserScreen) MouseMoved(e *desktop.MouseEvent) {
	x, y, ok := s.coords(e.Position)
	if !ok {
		return
	}
	buttons := s.buttons
	select {
	case s.view.page.jobs <- func(ctx context.Context) error {
		return s.view.page.run(ctx, input.DispatchMouseEvent(input.MouseMoved, x, y).WithButtons(buttons))
	}:
	default:
	}
}
func (s *browserScreen) MouseDown(e *desktop.MouseEvent) {
	s.view.panel.u.window.Canvas().Focus(s)
	s.modelCursor.Hide()
	s.buttons = 1
	s.mouse(e, input.MousePressed)
}
func (s *browserScreen) MouseUp(e *desktop.MouseEvent) {
	s.mouse(e, input.MouseReleased)
	s.buttons = 0
}
func (s *browserScreen) mouse(e *desktop.MouseEvent, kind input.MouseType) {
	x, y, ok := s.coords(e.Position)
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
	s.view.page.async(func(ctx context.Context) error {
		return s.view.page.run(ctx, input.DispatchMouseEvent(kind, x, y).WithButton(button).WithClickCount(1))
	})
}
func (s *browserScreen) Scrolled(e *fyne.ScrollEvent) {
	x, y, ok := s.coords(e.Position)
	if !ok {
		return
	}
	s.view.page.async(func(ctx context.Context) error {
		return s.view.page.run(ctx, input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(float64(-e.Scrolled.DX)*20).WithDeltaY(float64(-e.Scrolled.DY)*20))
	})
}

func (p *toolPanel) syncBrowsers() {
	var active *browserPage
	if p.target > 0 && p.active >= 0 && p.active < len(p.tabs) {
		active = p.tabs[p.active].browser
	}
	visit := func(tabs []panelTab) {
		for _, tab := range tabs {
			if tab.browser != nil {
				tab.browser.setVisible(tab.browser == active)
			}
		}
	}
	visit(p.tabs)
	visit(p.home.tabs)
	for _, chat := range p.u.chats {
		if chat.panel != nil {
			visit(chat.panel.tabs)
		}
	}
}

func (p *toolPanel) removeBrowser(chat *conversation, b *browserPage) {
	if chat == p.owner {
		for i, tab := range p.tabs {
			if tab.browser == b {
				p.close(i)
				return
			}
		}
		return
	}
	state := p.stateOf(chat)
	for i, tab := range state.tabs {
		if tab.browser == b {
			tab.dispose()
			state.tabs = append(state.tabs[:i], state.tabs[i+1:]...)
			state.active = min(state.active, len(state.tabs)-1)
			if chat != nil {
				chat.Panel = state.saved()
			}
			p.u.save()
			return
		}
	}
}

func (s *browserScreen) KeyDown(e *fyne.KeyEvent) {
	switch e.Name {
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		s.modifiers |= 2
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		s.modifiers |= 1
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		s.modifiers |= 8
	case desktop.KeySuperLeft, desktop.KeySuperRight:
		s.modifiers |= 4
	}
}
func (s *browserScreen) KeyUp(e *fyne.KeyEvent) {
	switch e.Name {
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		s.modifiers &^= 2
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		s.modifiers &^= 1
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		s.modifiers &^= 8
	case desktop.KeySuperLeft, desktop.KeySuperRight:
		s.modifiers &^= 4
	}
}
