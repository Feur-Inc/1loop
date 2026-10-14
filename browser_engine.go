package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

type browserInfo struct {
	ID         string  `json:"id"`
	URL        string  `json:"url"`
	Title      string  `json:"title"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	PixelRatio float64 `json:"pixelRatio"`
	Adaptive   bool    `json:"adaptive"`
	Mobile     bool    `json:"mobile"`
	Error      string  `json:"error,omitempty"`
}

type browserLog struct {
	Index  int    `json:"index"`
	Time   string `json:"time"`
	Level  string `json:"level"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// One Chromium process serves the app. Each chat has a separate browser
// context, so cookies and local storage are shared by its tabs only.
type browserEngine struct {
	ctx       context.Context
	cancel    func()
	once      sync.Once
	root      context.Context
	err       error
	pages     sync.Map
	userAgent string
	metadata  *emulation.UserAgentMetadata
	scale     float64 // the window's scale when Chrome starts: its pages' native density
}

func newBrowserEngine(parent context.Context) *browserEngine {
	ctx, cancel := context.WithCancel(parent)
	return &browserEngine{ctx: ctx, cancel: cancel}
}

func (engine *browserEngine) start() error {
	engine.once.Do(func() {
		executable := ""
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome"} {
			if path, err := exec.LookPath(name); err == nil {
				executable = path
				break
			}
		}
		if executable == "" {
			engine.err = fmt.Errorf("Install Chromium or Google Chrome to use browser tabs")
			return
		}
		opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(executable), chromedp.Flag("headless", "new"), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("enable-automation", false), chromedp.Flag("disable-blink-features", "AutomationControlled"))
		if engine.scale > 1 {
			// Chrome draws its pages at the screen's density itself: frames
			// are sharp without pinning the visible area (see browserMetrics).
			opts = append(opts, chromedp.Flag("force-device-scale-factor", strconv.FormatFloat(engine.scale, 'f', -1, 64)))
		}
		// Read native client hints before a process-wide UA override suppresses
		// them. The process-wide UA also covers a popup's very first request.
		profileAlloc, stopProfileAlloc := chromedp.NewExecAllocator(engine.ctx, opts...)
		profileRoot, stopProfileRoot := chromedp.NewContext(profileAlloc)
		var native struct {
			UserAgent string
			Metadata  *emulation.UserAgentMetadata
		}
		engine.err = chromedp.Run(profileRoot, chromedp.Navigate("chrome://version"), chromedp.Evaluate(`(async()=>({userAgent:navigator.userAgent,metadata:navigator.userAgentData?await navigator.userAgentData.getHighEntropyValues(['architecture','bitness','platformVersion','fullVersionList','model','wow64']):null}))()`, &native, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		stopProfileRoot()
		stopProfileAlloc()
		if engine.err != nil {
			return
		}
		engine.userAgent = strings.ReplaceAll(native.UserAgent, "HeadlessChrome/", "Chrome/")
		engine.metadata = native.Metadata
		opts = append(opts, chromedp.UserAgent(engine.userAgent))
		alloc, stopAlloc := chromedp.NewExecAllocator(engine.ctx, opts...)
		root, stopRoot := chromedp.NewContext(alloc, chromedp.WithErrorf(func(f string, a ...any) { devf("browser", f, a...) }))
		context.AfterFunc(engine.ctx, func() { stopRoot(); stopAlloc() })
		engine.root = root
		chromedp.ListenBrowser(root, func(event any) {
			switch event := event.(type) {
			case *target.EventTargetCreated:
				if value, ok := engine.pages.Load(event.TargetInfo.OpenerID); ok && event.TargetInfo.Type == "page" {
					parent := value.(*browserPage)
					if parent.onPopup != nil {
						parent.onPopup(event.TargetInfo.TargetID, event.TargetInfo.URL)
					}
				}
			case *target.EventTargetInfoChanged:
				if value, ok := engine.pages.Load(event.TargetInfo.TargetID); ok {
					browser := value.(*browserPage)
					browser.change(func(info *browserInfo) {
						info.Title = event.TargetInfo.Title
						info.URL = event.TargetInfo.URL
					})
				}
			case *target.EventTargetDestroyed:
				if value, ok := engine.pages.Load(event.TargetID); ok {
					closedBrowser := value.(*browserPage)
					if closedBrowser.life.Err() == nil && closedBrowser.onClosed != nil {
						closedBrowser.onClosed()
					}
				}
			}
		})
		engine.err = chromedp.Run(root)
	})
	return engine.err
}

type browserSession struct {
	engine *browserEngine
	ctx    context.Context
	cancel func()
	once   sync.Once
	root   context.Context
	err    error
	refs   int // UI thread owns references held by its tabs
}

func newBrowserSession(engine *browserEngine) *browserSession {
	ctx, cancel := context.WithCancel(engine.ctx)
	return &browserSession{engine: engine, ctx: ctx, cancel: cancel}
}

func (session *browserSession) start() error {
	session.once.Do(func() {
		if session.err = session.engine.start(); session.err != nil {
			return
		}
		if session.err = session.ctx.Err(); session.err != nil {
			return
		}
		root, cancel := chromedp.NewContext(session.engine.root, chromedp.WithNewBrowserContext())
		context.AfterFunc(session.ctx, cancel)
		session.root = root
		session.err = chromedp.Run(root)
	})
	return session.err
}

type browserPage struct {
	session      *browserSession
	life         context.Context
	cancel       func()
	ready        chan struct{}
	ctx          context.Context // published when ready closes
	gate         chan struct{}
	jobs         chan func(context.Context) error
	inputMu      sync.Mutex
	inputs       []chromedp.Action               // clicks, keys and wheel, in order
	move         *input.DispatchMouseEventParams // the latest pointer position not sent yet
	inputWake    chan struct{}
	frames       chan *page.EventScreencastFrame
	mu           sync.Mutex
	info         browserInfo
	logs         []browserLog
	logIndex     int
	visible      bool
	onFrame      func(image.Image)
	onChange     func(browserInfo)
	onLogs       func()
	onCursor     func(float64, float64)
	phones       map[*phoneWatcher]bool // phones following the page (browser_phone.go)
	phoneFrame   []byte                 // the latest frame made for them
	loading      bool                   // the main frame is loading
	cursorX      float64                // the agent's cursor, as last placed
	cursorY      float64
	cursorShown  bool
	attachTarget target.ID
	closeOnce    sync.Once
	onPopup      func(target.ID, string)
	onClosed     func()
	view         *browserView // UI thread only
}

func newBrowserPage(session *browserSession, info browserInfo) *browserPage {
	if info.ID == "" {
		info.ID = identifier()[:16]
	}
	if info.URL == "" {
		info.URL = "about:blank"
	}
	if info.Width == 0 {
		info.Width = 1280
	}
	if info.Height == 0 {
		info.Height = 800
	}
	if info.PixelRatio <= 0 {
		info.PixelRatio = 1
	}
	life, cancel := context.WithCancel(session.ctx)
	browser := &browserPage{session: session, life: life, cancel: cancel, ready: make(chan struct{}), gate: make(chan struct{}, 1), jobs: make(chan func(context.Context) error, 64), frames: make(chan *page.EventScreencastFrame, 1), inputWake: make(chan struct{}, 1), info: info}
	session.refs++
	return browser
}

func (browser *browserPage) snapshot() browserInfo {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	return browser.info
}
func (browser *browserPage) change(update func(*browserInfo)) {
	browser.mu.Lock()
	update(&browser.info)
	info := browser.info
	browser.mu.Unlock()
	// UI callbacks may read the page again; invoke them after releasing its lock.
	if browser.onChange != nil {
		browser.onChange(info)
	}
	browser.toPhones(nil)
}
func (browser *browserPage) addLog(level, text, source string) {
	browser.mu.Lock()
	browser.logIndex++
	browser.logs = append(browser.logs, browserLog{browser.logIndex, time.Now().Format("15:04:05.000"), level, truncateText(text, 4000), source})
	if len(browser.logs) > 500 {
		browser.logs = append([]browserLog{}, browser.logs[len(browser.logs)-500:]...)
	}
	browser.mu.Unlock()
	if browser.onLogs != nil {
		browser.onLogs()
	}
}
func (browser *browserPage) logSnapshot(after int) []browserLog {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	var out []browserLog
	for _, entry := range browser.logs {
		if entry.Index > after {
			out = append(out, entry)
		}
	}
	return out
}

func (browser *browserPage) start() {
	err := browser.session.start()
	if err == nil {
		options := []chromedp.ContextOption{}
		if browser.attachTarget != "" {
			options = append(options, chromedp.WithTargetID(browser.attachTarget))
		}
		ctx, cancel := chromedp.NewContext(browser.session.root, options...)
		context.AfterFunc(browser.life, cancel)
		browser.ctx = ctx
		chromedp.ListenTarget(ctx, browser.event)
		info := browser.snapshot()
		err = chromedp.Run(ctx,
			runtime.Enable(), log.Enable(), network.Enable(),
			emulation.SetUserAgentOverride(browser.session.engine.userAgent).WithUserAgentMetadata(browser.session.engine.metadata),
			browserMetrics(info.Width, info.Height, info.PixelRatio, info.Mobile))
	}
	if err != nil {
		browser.change(func(info *browserInfo) { info.Error = err.Error() })
		close(browser.ready)
		return
	}
	id := chromedp.FromContext(browser.ctx).Target.TargetID
	browser.session.engine.pages.Store(id, browser)
	context.AfterFunc(browser.life, func() { browser.session.engine.pages.Delete(id) })
	if browser.attachTarget == "" {
		navigation, stop := context.WithTimeout(browser.ctx, 30*time.Second)
		err = chromedp.Run(navigation, chromedp.Navigate(browser.snapshot().URL))
		stop()
	}
	if err != nil {
		browser.change(func(browserInfoValue *browserInfo) { browserInfoValue.Error = err.Error() })
	}
	close(browser.ready)
	go browser.frameLoop()
	go browser.inputLoop()
	go func() {
		for {
			select {
			case <-browser.life.Done():
				return
			case job := <-browser.jobs:
				if err := job(browser.life); err != nil && browser.life.Err() == nil {
					browser.addLog("error", err.Error(), "1loop")
				}
			}
		}
	}()
	browser.mu.Lock()
	// A phone may have started following the page while it loaded.
	wanted := browser.visible || len(browser.phones) > 0
	browser.mu.Unlock()
	if wanted {
		browser.stream()
	}
	go browser.refreshTitle()
}

func (browser *browserPage) event(event any) {
	switch e := event.(type) {
	case *page.EventScreencastFrame:
		// Event handlers must not issue synchronous CDP commands: that would
		// block the reader needed for their replies. Decode only the latest frame.
		go func() { _ = chromedp.Run(browser.ctx, page.ScreencastFrameAck(e.SessionID)) }()
		select {
		case browser.frames <- e:
		default:
			select {
			case <-browser.frames:
			default:
			}
			select {
			case browser.frames <- e:
			default:
			}
		}
	case *page.EventFrameNavigated:
		if e.Frame.ParentID == "" {
			browser.change(func(info *browserInfo) { info.URL = e.Frame.URL; info.Error = "" })
		}
	case *page.EventFrameStartedLoading:
		browser.mainLoading(string(e.FrameID), true)
	case *page.EventFrameStoppedLoading:
		browser.mainLoading(string(e.FrameID), false)
	case *page.EventNavigatedWithinDocument:
		browser.change(func(browserInfoValue *browserInfo) { browserInfoValue.URL = e.URL })
	case *page.EventLoadEventFired:
		go browser.refreshTitle()
	case *runtime.EventConsoleAPICalled:
		var parts []string
		for _, arg := range e.Args {
			if len(arg.Value) > 0 {
				parts = append(parts, string(arg.Value))
			} else {
				parts = append(parts, arg.Description)
			}
		}
		browser.addLog(string(e.Type), strings.Join(parts, " "), "console")
	case *runtime.EventExceptionThrown:
		text := e.ExceptionDetails.Text
		if e.ExceptionDetails.Exception != nil {
			text += " · " + e.ExceptionDetails.Exception.Description
		}
		browser.addLog("error", text, e.ExceptionDetails.URL)
	case *log.EventEntryAdded:
		browser.addLog(string(e.Entry.Level), e.Entry.Text, e.Entry.URL)
	case *network.EventLoadingFailed:
		if !e.Canceled {
			browser.addLog("error", e.ErrorText, "network")
		}
	case *network.EventResponseReceived:
		if e.Response.Status >= 400 {
			browser.addLog("error", fmt.Sprintf("HTTP %d %s", e.Response.Status, e.Response.URL), "network")
		}
	}
}

func (browser *browserPage) frameLoop() {
	for {
		select {
		case <-browser.life.Done():
			return
		case frame := <-browser.frames:
			data, err := base64.StdEncoding.DecodeString(frame.Data)
			if err != nil {
				continue
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				continue
			}
			if browser.phonesWatching() {
				browser.toPhones(phoneJPEG(img))
			}
			if browser.onFrame != nil && browser.isVisible() {
				if _, ok := img.(*image.RGBA); !ok {
					// Converted here rather than by the painter, on the UI thread.
					rgba := image.NewRGBA(img.Bounds())
					draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
					img = rgba
				}
				browser.onFrame(img)
			}
		}
	}
}

func (browser *browserPage) run(ctx context.Context, actions ...chromedp.Action) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-browser.life.Done():
		return fmt.Errorf("Browser tab is closed")
	case <-browser.ready:
	}
	if info := browser.snapshot(); info.Error != "" && browser.ctx == nil {
		return fmt.Errorf("%s", info.Error)
	}
	select {
	case browser.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-browser.life.Done():
		return fmt.Errorf("Browser tab is closed")
	}
	defer func() { <-browser.gate }()
	operation, cancel := context.WithTimeout(browser.ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return chromedp.Run(operation, actions...)
}

func (browser *browserPage) async(job func(context.Context) error) {
	select {
	case browser.jobs <- job:
	case <-browser.life.Done():
	default:
		browser.addLog("warn", "Input queue busy; try again.", "1loop")
	}
}

// pointer sends the pointer's position. Only the latest one waits: each
// takes Chrome a few milliseconds, and queuing every position made clicks
// and hovers wait behind a second of stale moves.
func (browser *browserPage) pointer(x, y float64, buttons int64) {
	browser.inputMu.Lock()
	browser.move = input.DispatchMouseEvent(input.MouseMoved, x, y).WithButtons(buttons)
	browser.inputMu.Unlock()
	browser.wakeInput()
}

// input sends a click, key or text in order, before any pending move: it
// carries its own position, so the move would only come after it, stale.
func (browser *browserPage) input(action chromedp.Action) {
	browser.inputMu.Lock()
	browser.move = nil
	if wheel, ok := action.(*input.DispatchMouseEventParams); ok && wheel.Type == input.MouseWheel && len(browser.inputs) > 0 {
		if last, ok := browser.inputs[len(browser.inputs)-1].(*input.DispatchMouseEventParams); ok && last.Type == input.MouseWheel && last.X == wheel.X && last.Y == wheel.Y {
			last.DeltaX += wheel.DeltaX
			last.DeltaY += wheel.DeltaY
			browser.inputMu.Unlock()
			return
		}
	}
	browser.inputs = append(browser.inputs, action)
	browser.inputMu.Unlock()
	browser.wakeInput()
}

func (browser *browserPage) wakeInput() {
	// One buffered signal wakes the worker for the whole pending input batch.
	select {
	case browser.inputWake <- struct{}{}:
	default:
	}
}

// inputLoop sends the user's input outside the commands' gate, so a slow
// navigation or script never holds a click back.
func (browser *browserPage) inputLoop() {
	if browser.ctx == nil {
		return
	}
	for {
		select {
		case <-browser.life.Done():
			return
		case <-browser.inputWake:
		}
		for {
			browser.inputMu.Lock()
			var action chromedp.Action
			if len(browser.inputs) > 0 {
				action = browser.inputs[0]
				browser.inputs = browser.inputs[1:]
			} else if browser.move != nil {
				action, browser.move = browser.move, nil
			}
			browser.inputMu.Unlock()
			if action == nil {
				break
			}
			ctx, cancel := context.WithTimeout(browser.ctx, 5*time.Second)
			err := chromedp.Run(ctx, action)
			cancel()
			if err != nil && browser.life.Err() == nil {
				browser.addLog("error", err.Error(), "1loop")
			}
		}
	}
}

func (browser *browserPage) setVisible(visible bool) {
	browser.mu.Lock()
	changed := browser.visible != visible
	browser.visible = visible
	browser.mu.Unlock()
	if changed {
		select {
		case <-browser.ready:
			if browser.ctx != nil {
				go browser.stream()
			}
		default:
		}
	}
}
func (browser *browserPage) isVisible() bool {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	return browser.visible
}

func (browser *browserPage) stream() {
	_ = browser.run(browser.life, chromedp.ActionFunc(func(ctx context.Context) error {
		browser.mu.Lock()
		visible, watched := browser.visible, len(browser.phones) > 0
		browser.mu.Unlock()
		if visible || watched {
			// Chrome paints the tabs behind the front one lazily: a page
			// watched there showed blank bands where it had not painted yet.
			if err := page.BringToFront().Do(ctx); err != nil {
				return err
			}
			return page.StartScreencast().WithFormat(page.ScreencastFormatPng).WithEveryNthFrame(1).Do(ctx)
		}
		return page.StopScreencast().Do(ctx)
	}))
}

func (browser *browserPage) close() {
	browser.closeOnce.Do(func() {
		browser.cancel()
		browser.session.refs--
		if browser.session.refs == 0 {
			browser.session.cancel()
		}
	})
}
func (browser *browserPage) refreshTitle() {
	var title, location string
	if browser.run(browser.life, chromedp.Title(&title), chromedp.Location(&location)) == nil {
		browser.change(func(info *browserInfo) { info.Title = title; info.URL = location })
	}
}

func browserURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "about:blank", nil
	}
	if filepath.IsAbs(raw) {
		return (&url.URL{Scheme: "file", Path: raw}).String(), nil
	}
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "about:") && !strings.HasPrefix(raw, "data:") {
		scheme := "https://"
		if strings.HasPrefix(raw, "localhost") || strings.HasPrefix(raw, "127.") || strings.HasPrefix(raw, "[::1]") {
			scheme = "http://"
		}
		raw = scheme + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.Host == "" {
			return "", fmt.Errorf("Enter a valid URL")
		}
	case "file", "about", "data":
	default:
		return "", fmt.Errorf("Use an http, https, file, data or about URL")
	}
	return raw, nil
}

func (browser *browserPage) navigate(ctx context.Context, action, raw string) error {
	var job chromedp.Action
	switch action {
	case "navigate":
		u, err := browserURL(raw)
		if err != nil {
			return err
		}
		job = chromedp.Navigate(u)
	case "reload":
		job = page.Reload()
	case "back", "forward":
		job = chromedp.ActionFunc(func(ctx context.Context) error {
			at, entries, err := page.GetNavigationHistory().Do(ctx)
			if err != nil {
				return err
			}
			next := int(at) - 1
			if action == "forward" {
				next = int(at) + 1
			}
			if next < 0 || next >= len(entries) {
				return nil
			}
			return page.NavigateToHistoryEntry(entries[next].ID).Do(ctx)
		})
	default:
		return fmt.Errorf("Unknown navigation action")
	}
	err := browser.run(ctx, job)
	if err == nil {
		browser.refreshTitle()
	}
	return err
}

func (browser *browserPage) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	var value json.RawMessage
	err := browser.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		result, exception, err := runtime.Evaluate(expression).WithAwaitPromise(true).WithReturnByValue(true).WithUserGesture(true).WithReplMode(true).Do(ctx)
		if err != nil {
			return err
		}
		if exception != nil {
			return fmt.Errorf("JavaScript: %s", exception.Error())
		}
		if result.Value == nil {
			value = json.RawMessage(`null`)
		} else {
			value = append(json.RawMessage{}, result.Value...)
		}
		return nil
	}))
	return value, err
}

func browserMetrics(width, height int, ratio float64, mobile bool) *emulation.SetDeviceMetricsOverrideParams {
	// A DPR override alone leaves the screencast surface at CSS resolution.
	// Resize that surface as well, without changing the page's CSS viewport.
	// No Viewport here: it pins the visible area to a place in the
	// document, so a scrolled page showed blank bands and shifted content.
	// Sharp frames come from Chrome's own scale (browserEngine.scale).
	return emulation.SetDeviceMetricsOverride(int64(width), int64(height), ratio, mobile)
}

func (browser *browserPage) resize(ctx context.Context, width, height int, mobile, adaptive bool, pixelRatio ...float64) error {
	if width < 240 || width > 3840 || height < 200 || height > 2160 {
		return fmt.Errorf("Viewport must be 240–3840 × 200–2160 CSS pixels")
	}
	ratio := browser.snapshot().PixelRatio
	if len(pixelRatio) > 0 {
		ratio = pixelRatio[0]
	}
	err := browser.run(ctx, browserMetrics(width, height, ratio, mobile))
	if err == nil {
		browser.change(func(info *browserInfo) {
			info.Width = width
			info.Height = height
			info.Mobile = mobile
			info.Adaptive = adaptive
			info.PixelRatio = ratio
		})
	}
	return err
}

func (browser *browserPage) point(ctx context.Context, selector string, x, y float64) (float64, float64, error) {
	if selector != "" {
		encoded, _ := json.Marshal(selector)
		raw, err := browser.evaluate(ctx, `(()=>{const e=document.querySelector(`+string(encoded)+`);if(!e)throw Error('Element not found');e.scrollIntoView({block:'center',inline:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`)
		if err != nil {
			return 0, 0, err
		}
		var p struct{ X, Y float64 }
		if err = json.Unmarshal(raw, &p); err != nil {
			return 0, 0, err
		}
		x, y = p.X, p.Y
	}
	info := browser.snapshot()
	if x < 0 || y < 0 || x >= float64(info.Width) || y >= float64(info.Height) {
		return 0, 0, fmt.Errorf("Coordinates are outside the viewport")
	}
	return x, y, nil
}
func (browser *browserPage) cursor(x, y float64) {
	browser.mu.Lock()
	browser.cursorX, browser.cursorY, browser.cursorShown = x, y, true
	browser.mu.Unlock()
	browser.toPhones(nil)
	if browser.onCursor != nil {
		browser.onCursor(x, y)
	}
}
func (browser *browserPage) interact(ctx context.Context, action, selector, text string, x, y, dx, dy float64) error {
	if action == "click" || action == "move" || action == "scroll" || (action == "type" && selector != "") {
		var err error
		x, y, err = browser.point(ctx, selector, x, y)
		if err != nil {
			return err
		}
		browser.cursor(x, y)
	}
	switch action {
	case "move":
		return browser.run(ctx, input.DispatchMouseEvent(input.MouseMoved, x, y))
	case "click":
		return browser.run(ctx, chromedp.MouseClickXY(x, y))
	case "scroll":
		return browser.run(ctx, input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(dx).WithDeltaY(dy))
	case "type":
		if selector != "" {
			if err := browser.run(ctx, chromedp.MouseClickXY(x, y)); err != nil {
				return err
			}
		}
		return browser.run(ctx, input.InsertText(text))
	case "key":
		key, modifiers := browserKeyChord(text)
		return browser.run(ctx, chromedp.KeyEvent(key, chromedp.KeyModifiers(modifiers...)))
	default:
		return fmt.Errorf("Unknown browser interaction")
	}
}

func (browser *browserPage) capture(ctx context.Context, full bool) (ImagePart, error) {
	var data []byte
	action := chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		data, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).WithFromSurface(true).WithCaptureBeyondViewport(full).Do(ctx)
		return err
	})
	err := browser.run(ctx, action)
	return ImagePart{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(data)}, err
}

func browserKeyChord(text string) (string, []input.Modifier) {
	parts := strings.Split(text, "+")
	var mods []input.Modifier
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToLower(part) {
		case "ctrl", "control":
			mods = append(mods, input.ModifierCtrl)
		case "shift":
			mods = append(mods, input.ModifierShift)
		case "alt":
			mods = append(mods, input.ModifierAlt)
		case "meta", "super":
			mods = append(mods, input.ModifierMeta)
		}
	}
	key := parts[len(parts)-1]
	if special := browserKey(fyne.KeyName(key)); special != "" {
		return special, mods
	}
	aliases := map[string]string{"enter": kb.Enter, "return": kb.Enter, "tab": kb.Tab, "escape": kb.Escape, "backspace": kb.Backspace, "delete": kb.Delete, "arrowleft": kb.ArrowLeft, "arrowright": kb.ArrowRight, "arrowup": kb.ArrowUp, "arrowdown": kb.ArrowDown, "home": kb.Home, "end": kb.End, "pageup": kb.PageUp, "pagedown": kb.PageDown, "space": " "}
	if special := aliases[strings.ToLower(key)]; special != "" {
		key = special
	} else if len(mods) > 0 {
		key = strings.ToLower(key)
	}
	return key, mods
}

// mainLoading follows the loading of the page itself (not of its frames),
// for the phone's progress bar.
func (browser *browserPage) mainLoading(frame string, loading bool) {
	if browser.ctx == nil || frame != string(chromedp.FromContext(browser.ctx).Target.TargetID) {
		return
	}
	browser.mu.Lock()
	browser.loading = loading
	browser.mu.Unlock()
	browser.toPhones(nil)
}
