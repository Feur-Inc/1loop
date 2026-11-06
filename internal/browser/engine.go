// SPDX-License-Identifier: MPL-2.0

// Package browser drives the agent's Chromium: pages, input, frames and
// the phone's live view.
package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"1loop/internal/core"
	"1loop/internal/keys"
	"1loop/internal/platform"
)

type Info struct {
	ID         string  `json:"id"`
	URL        string  `json:"url"`
	Title      string  `json:"title"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	PixelRatio float64 `json:"pixelRatio"`
	Native     bool    `json:"native,omitempty"`
	Adaptive   bool    `json:"adaptive"`
	Mobile     bool    `json:"mobile"`
	Error      string  `json:"error,omitempty"`
	Status     string  `json:"status,omitempty"` // what the page waits for (Chromium's first download)
}

type browserLog struct {
	Index  int    `json:"index"`
	Time   string `json:"time"`
	Level  string `json:"level"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// One engine serves the live tabs. Its process generation ends when the
// final tab closes; another tab starts a fresh generation using the profile.
type Engine struct {
	Native    NativeHost
	Profile   string // a separate persistent profile, when this is not Browser Use
	Language  string // native pages' requests and navigator.language (WebView follows the phone's)
	ctx       context.Context
	Cancel    func()
	mu        sync.Mutex
	refs      atomic.Int64
	runCtx    context.Context
	runCancel context.CancelFunc
	once      sync.Once
	root      context.Context
	err       error
	Pages     sync.Map
	userAgent string
	metadata  *emulation.UserAgentMetadata
	Scale     float64       // the window's scale when Chrome starts: its pages' native density
	closed    chan struct{} // closed after the current Chrome generation exits
}

func NewBrowserEngine(parent context.Context) *Engine {
	ctx, cancel := context.WithCancel(parent)
	runCtx, runCancel := context.WithCancel(ctx)
	return &Engine{ctx: ctx, Cancel: cancel, runCtx: runCtx, runCancel: runCancel}
}

func (engine *Engine) start() error {
	// The browser is found, and the first time downloaded, outside
	// engine.mu: closing a tab must not wait minutes for a download.
	var binary browserBinary
	var lookup error
	engine.mu.Lock()
	stopped := engine.Native == nil && engine.root == nil && engine.err == nil
	engine.mu.Unlock()
	if stopped {
		binary, lookup = findBrowser(engine.ctx)
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.once.Do(func() {
		if engine.Native != nil {
			address, id, err := engine.Native.Connect(engine.runCtx)
			if err != nil {
				engine.err = err
				return
			}
			alloc, stopAlloc := chromedp.NewRemoteAllocator(engine.runCtx, address, chromedp.NoModifyURL)
			root, stopRoot := chromedp.NewContext(alloc, chromedp.WithTargetID(id))
			context.AfterFunc(engine.runCtx, func() { stopRoot(); stopAlloc() })
			engine.root = root
			engine.err = chromedp.Run(root)
			if engine.err == nil {
				engine.listen(root)
			}
			return
		}
		if !stopped { // stopped since the check above
			binary, lookup = findBrowser(engine.ctx)
		}
		if lookup != nil {
			engine.err = lookup
			return
		}
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(binary.path),
			chromedp.Env(binary.env...),
			chromedp.Flag("headless", "new"),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("enable-automation", false),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
		)
		var contextOptions []chromedp.ContextOption
		// One profile, kept between sessions: the agent's browser stays
		// logged in, with its cookies and history, until the user clears
		// it (Settings → General → Browser).
		dir := engine.Profile
		if dir == "" {
			dir = ProfileDir()
		}
		if dir != "" {
			if _, err := os.Stat(dir + ".clear"); err == nil {
				os.RemoveAll(dir)
				os.Remove(dir + ".clear")
			}
			if os.MkdirAll(dir, 0700) == nil {
				opts = append(opts, chromedp.UserDataDir(dir))
			}
		}
		if engine.Scale > 1 {
			// Chrome draws its pages at the screen's density itself: frames
			// are sharp without pinning the visible area (see browserMetrics).
			opts = append(opts, chromedp.Flag("force-device-scale-factor", strconv.FormatFloat(engine.Scale, 'f', -1, 64)))
		}
		// Read native client hints before a process-wide UA override suppresses
		// them. The process-wide UA also covers a popup's very first request.
		var native struct {
			UserAgent string
			Metadata  *emulation.UserAgentMetadata
		}
		probe := func() error {
			profileAlloc, stopProfileAlloc := chromedp.NewExecAllocator(engine.runCtx, opts...)
			profileRoot, stopProfileRoot := chromedp.NewContext(profileAlloc, contextOptions...)
			defer stopProfileAlloc()
			defer stopProfileRoot()
			return chromedp.Run(profileRoot, chromedp.Navigate("chrome://version"), chromedp.Evaluate(`(async()=>({userAgent:navigator.userAgent,metadata:navigator.userAgentData?await navigator.userAgentData.getHighEntropyValues(['architecture','bitness','platformVersion','fullVersionList','model','wow64']):null}))()`, &native, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		}
		if os.Getenv("ONELOOP_CHROMIUM_NO_SANDBOX") == "1" {
			// A container (the server's Docker image) is the browser's
			// isolation: its default profile grants neither the user
			// namespaces nor the privileges Chromium's sandboxes need.
			opts = append(opts, chromedp.NoSandbox)
		}
		engine.err = probe()
		if engine.err != nil && binary.own && strings.Contains(engine.err.Error(), "No usable sandbox") {
			// Its sandbox needs user namespaces, which some systems (Ubuntu
			// since 23.10, containers) allow only to browsers installed by
			// the administrator, or a setuid helper only root can install.
			platform.Devf("browser", "1loop's Chromium has no usable sandbox here: starting it without")
			opts = append(opts, chromedp.NoSandbox)
			engine.err = probe()
		}
		if engine.err != nil {
			if binary.own {
				engine.err = chromiumStartError(engine.err)
			}
			return
		}
		engine.userAgent = strings.ReplaceAll(native.UserAgent, "HeadlessChrome/", "Chrome/")
		engine.metadata = native.Metadata
		opts = append(opts, chromedp.UserAgent(engine.userAgent))
		// Chrome outlives the engine's context by a moment: it is asked to
		// close, which writes its cookies and history to the profile, and
		// killed only if it does not. Killed at once, it lost the session.
		alloc, stopAlloc := chromedp.NewExecAllocator(context.WithoutCancel(engine.runCtx), opts...)
		contextOptions = append(contextOptions, chromedp.WithErrorf(func(f string, a ...any) { platform.Devf("browser", f, a...) }))
		root, stopRoot := chromedp.NewContext(alloc, contextOptions...)
		closedSignal := make(chan struct{})
		engine.closed = closedSignal
		context.AfterFunc(engine.runCtx, func() {
			defer close(closedSignal)
			closed := make(chan struct{})
			go func() { chromedp.Cancel(root); close(closed) }() // asks Chrome to close, waits for it
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
			}
			stopRoot()
			stopAlloc()
		})
		engine.root = root
		if engine.err = chromedp.Run(root); engine.err != nil { // Chrome starts here, owned by root
			return
		}
		engine.listen(root)
		engine.err = chromedp.Run(root)
	})
	return engine.err
}

func (engine *Engine) stopIfIdle() {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.refs.Load() != 0 {
		return
	}
	if engine.root == nil && engine.err == nil {
		return
	}
	engine.runCancel()
	if engine.Native != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = engine.Native.Stop(ctx)
	}
	waitBrowserClosed(engine.closed)
	engine.once = sync.Once{}
	engine.root, engine.err, engine.closed = nil, nil, nil
	engine.runCtx, engine.runCancel = context.WithCancel(engine.ctx)
}

type Session struct {
	engine    *Engine
	ctx       context.Context
	cancel    func()
	once      sync.Once
	root      context.Context
	err       error
	userAgent string
	metadata  *emulation.UserAgentMetadata
	refs      int // UI thread owns references held by its tabs
}

func NewBrowserSession(engine *Engine) *Session {
	ctx, cancel := context.WithCancel(engine.ctx)
	return &Session{engine: engine, ctx: ctx, cancel: cancel}
}

func (session *Session) start() error {
	session.once.Do(func() {
		if session.err = session.ctx.Err(); session.err != nil {
			return
		}
		if session.err = session.engine.start(); session.err != nil {
			return
		}
		session.engine.mu.Lock()
		if session.err = session.ctx.Err(); session.err != nil {
			session.engine.mu.Unlock()
			return
		}
		engineRoot := session.engine.root
		session.userAgent, session.metadata = session.engine.userAgent, session.engine.metadata
		session.engine.mu.Unlock()
		// The browser's own context, the one its profile keeps: a separate
		// browser context is a private window, whose cookies never reach disk.
		if session.engine.Native != nil {
			session.root = engineRoot
			return
		}
		root, cancel := chromedp.NewContext(engineRoot)
		context.AfterFunc(session.ctx, cancel)
		session.root = root
		session.err = chromedp.Run(root)
	})
	return session.err
}

type Page struct {
	View         any // the desktop's view of the page (*browserView); nil on the server
	Session      *Session
	Life         context.Context
	cancel       func()
	ready        chan struct{}
	Ctx          context.Context // published when ready closes
	gate         chan struct{}
	jobs         chan func(context.Context) error
	inputMu      sync.Mutex
	inputs       []chromedp.Action               // clicks, keys and wheel, in order
	move         *input.DispatchMouseEventParams // the latest pointer position not sent yet
	inputWake    chan struct{}
	frames       chan *page.EventScreencastFrame
	mu           sync.Mutex
	info         Info
	logs         []browserLog
	logIndex     int
	visible      bool
	OnFrame      func(image.Image)
	OnChange     func(Info)
	OnLogs       func()
	OnCursor     func(float64, float64)
	phones       map[*phoneWatcher]bool // phones following the page (phone.go)
	nativeFrame  []byte
	phoneFrame   []byte  // the latest frame made for them
	loading      bool    // the main frame is loading
	cursorX      float64 // the agent's cursor, as last placed
	cursorY      float64
	cursorShown  bool
	AttachTarget target.ID
	closeOnce    sync.Once
	OnPopup      func(target.ID, string)
	OnClosed     func()
}

func NewBrowserPage(session *Session, info Info) *Page {
	if info.ID == "" {
		info.ID = core.Identifier()[:16]
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
	info.Native = session.engine.Native != nil
	if info.PixelRatio <= 0 {
		info.PixelRatio = 1
	}
	life, cancel := context.WithCancel(session.ctx)
	browser := &Page{
		Session:   session,
		Life:      life,
		cancel:    cancel,
		ready:     make(chan struct{}),
		gate:      make(chan struct{}, 1),
		jobs:      make(chan func(context.Context) error, 64),
		frames:    make(chan *page.EventScreencastFrame, 1),
		inputWake: make(chan struct{}, 1),
		info:      info,
	}
	session.refs++
	session.engine.refs.Add(1)
	return browser
}

func (browser *Page) Snapshot() Info {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	return browser.info
}

func (browser *Page) change(update func(*Info)) {
	browser.mu.Lock()
	update(&browser.info)
	info := browser.info
	browser.mu.Unlock()
	// UI callbacks may read the page again; invoke them after releasing its lock.
	if browser.OnChange != nil {
		browser.OnChange(info)
	}
	browser.toPhones(nil)
}

func (browser *Page) AddLog(level, text, source string) {
	browser.mu.Lock()
	browser.logIndex++
	browser.logs = append(browser.logs, browserLog{browser.logIndex, time.Now().Format("15:04:05.000"), level, core.TruncateText(text, 4000), source})
	if len(browser.logs) > 500 {
		browser.logs = append([]browserLog{}, browser.logs[len(browser.logs)-500:]...)
	}
	browser.mu.Unlock()
	if browser.OnLogs != nil {
		browser.OnLogs()
	}
}

func (browser *Page) LogSnapshot(after int) []browserLog {
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

func (browser *Page) Start() {
	var waiting atomic.Bool
	stop := watchChromium(func(text string) {
		waiting.Store(true)
		browser.change(func(info *Info) { info.Status = text })
	})
	err := browser.Session.start()
	stop()
	if waiting.Load() {
		browser.change(func(info *Info) { info.Status = "" })
	}
	native := browser.Session.engine.Native
	if err == nil && native != nil {
		err = nativeURL(browser.Snapshot().URL)
	}
	if err == nil && native != nil {
		browser.AttachTarget, err = native.Open(browser.Life, browser.Snapshot())
	}
	if err == nil {
		options := []chromedp.ContextOption{}
		if browser.AttachTarget != "" {
			options = append(options, chromedp.WithTargetID(browser.AttachTarget))
		}
		ctx, cancel := chromedp.NewContext(browser.Session.root, options...)
		context.AfterFunc(browser.Life, cancel)
		browser.Ctx = ctx
		chromedp.ListenTarget(ctx, browser.event)
		info := browser.Snapshot()
		actions := []chromedp.Action{runtime.Enable(), log.Enable(), network.Enable()}
		if native == nil {
			actions = append(actions, emulation.SetUserAgentOverride(browser.Session.userAgent).WithUserAgentMetadata(browser.Session.metadata), browserMetrics(info.Width, info.Height, info.PixelRatio, info.Mobile))
		} else if language := browser.Session.engine.Language; language != "" {
			actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
				var agent string
				if err := chromedp.Evaluate(`navigator.userAgent`, &agent).Do(ctx); err != nil {
					return err
				}
				return emulation.SetUserAgentOverride(agent).WithAcceptLanguage(language).Do(ctx)
			}))
		}
		err = chromedp.Run(ctx, actions...)
	}
	if err != nil {
		browser.change(func(info *Info) { info.Error = err.Error() })
		close(browser.ready)
		return
	}
	id := chromedp.FromContext(browser.Ctx).Target.TargetID
	browser.Session.engine.Pages.Store(id, browser)
	context.AfterFunc(browser.Life, func() { browser.Session.engine.Pages.Delete(id) })
	if browser.AttachTarget == "" || native != nil {
		navigation, stop := context.WithTimeout(browser.Ctx, 30*time.Second)
		err = chromedp.Run(navigation, chromedp.Navigate(browser.Snapshot().URL))
		stop()
	}
	if err != nil {
		browser.change(func(browserInfoValue *Info) { browserInfoValue.Error = err.Error() })
	} else {
		// The page's own title, before anyone reads the tab: until then
		// Chrome gives its address as its title.
		titled, stop := context.WithTimeout(browser.Ctx, 5*time.Second)
		var title, location string
		if chromedp.Run(titled, chromedp.Title(&title), chromedp.Location(&location)) == nil {
			browser.change(func(info *Info) { info.Title = title; info.URL = location })
		}
		stop()
	}
	close(browser.ready)
	go browser.frameLoop()
	go browser.inputLoop()
	go func() {
		for {
			select {
			case <-browser.Life.Done():
				return
			case job := <-browser.jobs:
				if err := job(browser.Life); err != nil && browser.Life.Err() == nil {
					browser.AddLog("error", err.Error(), "1loop")
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

func (browser *Page) event(event any) {
	switch e := event.(type) {
	case *page.EventScreencastFrame:
		// Event handlers must not issue synchronous CDP commands: that would
		// block the reader needed for their replies. Decode only the latest frame.
		go func() { _ = chromedp.Run(browser.Ctx, page.ScreencastFrameAck(e.SessionID)) }()
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
			browser.change(func(info *Info) { info.URL = e.Frame.URL; info.Error = "" })
		}
	case *page.EventFrameStartedLoading:
		browser.mainLoading(string(e.FrameID), true)
	case *page.EventFrameStoppedLoading:
		browser.mainLoading(string(e.FrameID), false)
	case *page.EventNavigatedWithinDocument:
		browser.change(func(browserInfoValue *Info) { browserInfoValue.URL = e.URL })
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
		browser.AddLog(string(e.Type), strings.Join(parts, " "), "console")
	case *runtime.EventExceptionThrown:
		text := e.ExceptionDetails.Text
		if e.ExceptionDetails.Exception != nil {
			text += " · " + e.ExceptionDetails.Exception.Description
		}
		browser.AddLog("error", text, e.ExceptionDetails.URL)
	case *log.EventEntryAdded:
		browser.AddLog(string(e.Entry.Level), e.Entry.Text, e.Entry.URL)
	case *network.EventLoadingFailed:
		if !e.Canceled {
			browser.AddLog("error", e.ErrorText, "network")
		}
	case *network.EventResponseReceived:
		if e.Response.Status >= 400 {
			browser.AddLog("error", fmt.Sprintf("HTTP %d %s", e.Response.Status, e.Response.URL), "network")
		}
	}
}

func (browser *Page) frameLoop() {
	for {
		select {
		case <-browser.Life.Done():
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
				var mobile []byte
				if browser.needsPhoneJPEG() {
					mobile = phoneJPEG(img)
				}
				browser.toPhones(mobile, data)
			}
			if browser.OnFrame != nil && browser.isVisible() {
				if _, ok := img.(*image.RGBA); !ok {
					// Converted here rather than by the painter, on the UI thread.
					rgba := image.NewRGBA(img.Bounds())
					draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
					img = rgba
				}
				browser.OnFrame(img)
			}
		}
	}
}

func (browser *Page) Run(ctx context.Context, actions ...chromedp.Action) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-browser.Life.Done():
		return fmt.Errorf("Browser tab is closed")
	case <-browser.ready:
	}
	if info := browser.Snapshot(); info.Error != "" && browser.Ctx == nil {
		return fmt.Errorf("%s", info.Error)
	}
	select {
	case browser.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-browser.Life.Done():
		return fmt.Errorf("Browser tab is closed")
	}
	defer func() { <-browser.gate }()
	operation, cancel := context.WithTimeout(browser.Ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return chromedp.Run(operation, actions...)
}

func (browser *Page) Async(job func(context.Context) error) {
	select {
	case browser.jobs <- job:
	case <-browser.Life.Done():
	default:
		browser.AddLog("warn", "Input queue busy; try again.", "1loop")
	}
}

// Pointer sends the pointer's position. Only the latest one waits: each
// takes Chrome a few milliseconds, and queuing every position made clicks
// and hovers wait behind a second of stale moves.
func (browser *Page) Pointer(x, y float64, buttons int64) {
	browser.inputMu.Lock()
	browser.move = input.DispatchMouseEvent(input.MouseMoved, x, y).WithButtons(buttons)
	browser.inputMu.Unlock()
	browser.wakeInput()
}

// Input sends a click, key or text in order, before any pending move: it
// carries its own position, so the move would only come after it, stale.
func (browser *Page) Input(action chromedp.Action) {
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

func (browser *Page) wakeInput() {
	// One buffered signal wakes the worker for the whole pending input batch.
	select {
	case browser.inputWake <- struct{}{}:
	default:
	}
}

// inputLoop sends the user's input outside the commands' gate, so a slow
// navigation or script never holds a click back.
func (browser *Page) inputLoop() {
	if browser.Ctx == nil {
		return
	}
	for {
		select {
		case <-browser.Life.Done():
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
			ctx, cancel := context.WithTimeout(browser.Ctx, 5*time.Second)
			err := chromedp.Run(ctx, action)
			cancel()
			if err != nil && browser.Life.Err() == nil {
				browser.AddLog("error", err.Error(), "1loop")
			}
		}
	}
}

func (browser *Page) SetVisible(visible bool) {
	browser.mu.Lock()
	changed := browser.visible != visible
	browser.visible = visible
	browser.mu.Unlock()
	if changed {
		select {
		case <-browser.ready:
			if browser.Ctx != nil {
				go browser.stream()
			}
		default:
		}
	}
}

func (browser *Page) isVisible() bool {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	return browser.visible
}

func (browser *Page) stream() {
	if browser.Session.engine.Native != nil {
		return
	}

	_ = browser.Run(browser.Life, chromedp.ActionFunc(func(ctx context.Context) error {
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

func (browser *Page) Close() {
	browser.closeOnce.Do(func() {
		browser.cancel()
		if native := browser.Session.engine.Native; native != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = native.Close(ctx, browser.Snapshot().ID)
		}
		browser.Session.refs--
		if browser.Session.refs == 0 {
			browser.Session.cancel()
		}
		if browser.Session.engine.refs.Add(-1) == 0 {
			go browser.Session.engine.stopIfIdle()
		}
	})
}

func (browser *Page) refreshTitle() {
	var title, location string
	if browser.Run(browser.Life, chromedp.Title(&title), chromedp.Location(&location)) == nil {
		browser.change(func(info *Info) { info.Title = title; info.URL = location })
	}
}

// URL is the address to open for what was typed or given: a full URL, an
// absolute path (a local file) or a host with no scheme, which gets https,
// or plain http when it is this machine. A relative path or a file name is
// refused rather than sent out as a web request: README.md is also a host
// name, in Moldova.
func URL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "about:blank", nil
	}
	if filepath.IsAbs(raw) {
		return (&url.URL{Scheme: "file", Path: raw}).String(), nil
	}
	if !hasScheme(raw) {
		if strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "~") || strings.Contains(raw, `\`) {
			return "", fmt.Errorf("%s is a path, not a web address: give a full URL (https://…), or an absolute path for a local file", raw)
		}
		parsed, err := url.Parse("https://" + raw)
		if err != nil {
			return "", err
		}
		host := parsed.Hostname()
		switch {
		case localHost(host):
			raw = "http://" + raw
		case !strings.Contains(host, ".") && parsed.Port() == "":
			return "", fmt.Errorf("%s is not a web address: give a full URL such as https://example.com", raw)
		case parsed.Port() == "" && strings.Trim(parsed.Path, "/") == "" && fileExtensions[strings.ToLower(host[strings.LastIndex(host, ".")+1:])]:
			return "", fmt.Errorf("%s looks like a file name, not a web address: give a full URL (https://%s) to open a site of that name", raw, raw)
		default:
			raw = "https://" + raw
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
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

// hasScheme says whether an address starts with its scheme (https:,
// file:, about:...), and not with a host and its port (localhost:3000).
func hasScheme(raw string) bool {
	colon := strings.Index(raw, ":")
	if colon <= 0 {
		return false
	}
	for i, r := range raw[:colon] {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (i == 0 || !(r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
			return false
		}
	}
	port, _, _ := strings.Cut(raw[colon+1:], "/")
	_, err := strconv.Atoi(port)
	return port == "" && strings.HasPrefix(raw[colon+1:], "/") || err != nil
}

// localHost is this machine: only these get plain http without asking.
func localHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// fileExtensions end file names that are also host names a web request
// would go to (.md, .py, .sh are countries): without a scheme, they are files.
var fileExtensions = map[string]bool{}

func init() {
	for _, extension := range strings.Fields("md markdown txt log json jsonl yaml yml toml ini cfg conf env lock mod sum go py rb rs js mjs cjs ts tsx jsx java kt kts swift c h cc cpp hpp cs php pl sh bash zsh fish ps1 bat sql html htm css scss xml svg png jpg jpeg gif webp ico pdf csv tsv zip tar gz tgz deb rpm apk exe dll so wasm") {
		fileExtensions[extension] = true
	}
}

func (browser *Page) Navigate(ctx context.Context, action, raw string) error {
	var job chromedp.Action
	switch action {
	case "navigate":
		if strings.TrimSpace(raw) == "" {
			// about:blank is for a new tab: here it would wipe the page.
			return fmt.Errorf("Give the address to open")
		}
		u, err := URL(raw)
		if err == nil && browser.Session.engine.Native != nil {
			err = nativeURL(u)
		}
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
	err := browser.Run(ctx, job)
	if err == nil {
		browser.refreshTitle()
	}
	return err
}

func (browser *Page) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	var value json.RawMessage
	err := browser.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) (err error) {
		value, err = evaluate(ctx, expression)
		return err
	}))
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		// Run's own limit, not the caller's: say what it means here.
		err = fmt.Errorf("JavaScript: the expression didn't finish within 30 s (a promise that never settles, or a loop)")
	}
	return value, err
}

// settle awaits what an expression returned and gives it as JSON: REPL mode
// awaits a top-level await, not a promise the expression returns. Numbers
// and BigInts JSON cannot hold are given as text ("NaN", "42n").
// A reference back to an object being given is "[Circular]", a DOM node
// its HTML (cut at 300 characters) instead of {}.
const settle = `async function() {
	const value = await this;
	if (value === undefined) return null;
	const ancestors = [];
	try {
		return JSON.parse(JSON.stringify(value, function (key, item) {
			if (typeof item === "bigint") return item + "n";
			if (typeof item === "number" && !isFinite(item)) return String(item);
			if (typeof item !== "object" || item === null) return item;
			if (typeof Node !== "undefined" && item instanceof Node) {
				const html = item.outerHTML ?? item.textContent ?? item.nodeName;
				return html.length > 300 ? html.slice(0, 300) + "…" : html;
			}
			while (ancestors.length > 0 && ancestors.at(-1) !== this) ancestors.pop();
			if (ancestors.includes(item)) return "[Circular]";
			ancestors.push(item);
			return item;
		}));
	} catch (error) {
		return "[Not serializable: " + error.message + "]";
	}
}`

// evaluate runs expression in the page in REPL mode (top-level await, let
// redeclared from one call to the next), awaits a returned promise and
// returns the result as JSON. A thrown error or a rejected promise is an
// error.
func evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	result, exception, err := runtime.Evaluate(expression).WithAwaitPromise(true).WithUserGesture(true).WithReplMode(true).Do(ctx)
	if err != nil {
		return nil, err
	}
	if exception != nil {
		return nil, javaScriptError(exception)
	}
	if result.ObjectID == "" { // a primitive
		switch {
		case result.UnserializableValue != "":
			return json.Marshal(string(result.UnserializableValue))
		case result.Value == nil:
			return json.RawMessage(`null`), nil
		}
		return append(json.RawMessage{}, result.Value...), nil
	}
	defer runtime.ReleaseObject(result.ObjectID).Do(ctx)
	settled, exception, err := runtime.CallFunctionOn(settle).WithObjectID(result.ObjectID).WithAwaitPromise(true).WithReturnByValue(true).Do(ctx)
	if err != nil {
		return nil, err
	}
	if exception != nil {
		return nil, javaScriptError(exception)
	}
	if settled.Value == nil {
		return json.RawMessage(`null`), nil
	}
	return append(json.RawMessage{}, settled.Value...), nil
}

// javaScriptError is a thrown error or a rejection with its reason, which
// the exception's text leaves out when it isn't an Error object: a
// rejected Promise.reject("why") said only "Uncaught (in promise)".
func javaScriptError(exception *runtime.ExceptionDetails) error {
	reason := ""
	if thrown := exception.Exception; thrown != nil {
		switch {
		case thrown.Description != "":
			reason = thrown.Description
		case len(thrown.Value) > 0:
			reason = string(thrown.Value)
		case thrown.UnserializableValue != "":
			reason = string(thrown.UnserializableValue)
		default:
			reason = string(thrown.Type)
		}
	}
	text := strings.TrimSuffix(strings.TrimSpace(exception.Text), ":")
	first, stack, _ := strings.Cut(reason, "\n")
	switch {
	case reason == "":
		return fmt.Errorf("JavaScript: %s", text)
	case strings.HasSuffix(text, first): // "Uncaught Error: boom", then its stack
		reason = stack
	}
	if reason == "" {
		return fmt.Errorf("JavaScript: %s", text)
	}
	return fmt.Errorf("JavaScript: %s: %s", text, strings.TrimSpace(reason))
}

func browserMetrics(width, height int, ratio float64, mobile bool) *emulation.SetDeviceMetricsOverrideParams {
	// A DPR override alone leaves the screencast surface at CSS resolution.
	// Resize that surface as well, without changing the page's CSS viewport.
	// No Viewport here: it pins the visible area to a place in the
	// document, so a scrolled page showed blank bands and shifted content.
	// Sharp frames come from Chrome's own scale (browserEngine.scale).
	return emulation.SetDeviceMetricsOverride(int64(width), int64(height), ratio, mobile)
}

func (browser *Page) Resize(ctx context.Context, width, height int, mobile, adaptive bool, pixelRatio ...float64) error {
	if width < 240 || width > 3840 || height < 200 || height > 2160 {
		return fmt.Errorf("Viewport must be 240–3840 × 200–2160 CSS pixels")
	}
	ratio := browser.Snapshot().PixelRatio
	if len(pixelRatio) > 0 {
		ratio = pixelRatio[0]
	}
	var err error
	if native := browser.Session.engine.Native; native != nil {
		info := browser.Snapshot()
		info.Width, info.Height, info.Mobile, info.Adaptive = width, height, mobile, adaptive
		err = native.Resize(ctx, info)
	} else {
		err = browser.Run(ctx, browserMetrics(width, height, ratio, mobile))
	}
	if err == nil {
		browser.change(func(info *Info) {
			info.Width = width
			info.Height = height
			info.Mobile = mobile
			info.Adaptive = adaptive
			info.PixelRatio = ratio
		})
	}
	return err
}

func (browser *Page) point(ctx context.Context, selector string, x, y float64) (float64, float64, error) {
	if selector != "" {
		encoded, _ := json.Marshal(selector)
		raw, err := browser.Evaluate(ctx, `(()=>{const e=document.querySelector(`+string(encoded)+`);if(!e)throw Error('Element not found');e.scrollIntoView({block:'center',inline:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`)
		if err != nil {
			return 0, 0, err
		}
		var p struct{ X, Y float64 }
		if err = json.Unmarshal(raw, &p); err != nil {
			return 0, 0, err
		}
		x, y = p.X, p.Y
	}
	info := browser.Snapshot()
	if x < 0 || y < 0 || x >= float64(info.Width) || y >= float64(info.Height) {
		return 0, 0, fmt.Errorf("Coordinates are outside the viewport")
	}
	return x, y, nil
}

func (browser *Page) cursor(x, y float64) {
	browser.mu.Lock()
	browser.cursorX, browser.cursorY, browser.cursorShown = x, y, true
	browser.mu.Unlock()
	browser.toPhones(nil)
	if browser.OnCursor != nil {
		browser.OnCursor(x, y)
	}
}

func (browser *Page) Interact(ctx context.Context, action, selector, text string, x, y, dx, dy float64) error {
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
		return browser.Run(ctx, input.DispatchMouseEvent(input.MouseMoved, x, y))
	case "click":
		return browser.Run(ctx, chromedp.MouseClickXY(x, y))
	case "scroll":
		return browser.Run(ctx, input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(dx).WithDeltaY(dy))
	case "type":
		if selector != "" {
			if err := browser.Run(ctx, chromedp.MouseClickXY(x, y)); err != nil {
				return err
			}
		}
		// Text sent with nothing focused goes nowhere, and would be reported typed.
		focused, err := browser.Evaluate(ctx, `(()=>{const e=document.activeElement;return document.designMode==="on"||!!e&&(e.isContentEditable||e!==document.body&&e!==document.documentElement)})()`)
		if err == nil && string(focused) == "false" {
			return fmt.Errorf("Nothing is focused to type into: click the field first, or give its selector")
		}
		return browser.Run(ctx, input.InsertText(text))
	case "key":
		key, modifiers, err := browserKeyChord(text)
		if err != nil {
			return err
		}
		return browser.Run(ctx, chromedp.KeyEvent(key, chromedp.KeyModifiers(modifiers...)))
	default:
		return fmt.Errorf("Unknown browser interaction")
	}
}

func (browser *Page) Capture(ctx context.Context, full bool) (core.ImagePart, error) {
	if native := browser.Session.engine.Native; native != nil && !full {
		return native.Capture(ctx, browser.Snapshot().ID, full)
	}
	var data []byte
	action := chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		data, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).WithFromSurface(true).WithCaptureBeyondViewport(full).Do(ctx)
		return err
	})
	err := browser.Run(ctx, action)
	return core.ImagePart{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(data)}, err
}

// browserKeyChord is the key event for a key name (see keys.Parse). An
// unknown name is an error, not letters typed into the page.
func browserKeyChord(text string) (string, []input.Modifier, error) {
	chord, err := keys.Parse(text)
	if err != nil {
		return "", nil, err
	}
	var mods []input.Modifier
	for _, held := range []struct {
		on       bool
		modifier input.Modifier
	}{{chord.Ctrl, input.ModifierCtrl}, {chord.Shift, input.ModifierShift}, {chord.Alt, input.ModifierAlt}, {chord.Meta, input.ModifierMeta}} {
		if held.on {
			mods = append(mods, held.modifier)
		}
	}
	named := map[string]string{
		"Enter": kb.Enter, "Tab": kb.Tab, "Escape": kb.Escape, "Backspace": kb.Backspace,
		"Delete": kb.Delete, "Insert": kb.Insert, "Home": kb.Home, "End": kb.End,
		"PageUp": kb.PageUp, "PageDown": kb.PageDown, "ArrowUp": kb.ArrowUp,
		"ArrowDown": kb.ArrowDown, "ArrowLeft": kb.ArrowLeft, "ArrowRight": kb.ArrowRight,
		"Space": " ", "F1": kb.F1, "F2": kb.F2, "F3": kb.F3, "F4": kb.F4, "F5": kb.F5,
		"F6": kb.F6, "F7": kb.F7, "F8": kb.F8, "F9": kb.F9, "F10": kb.F10, "F11": kb.F11, "F12": kb.F12,
	}
	if key, ok := named[chord.Key]; ok {
		return key, mods, nil
	}
	if len(mods) > 0 {
		return strings.ToLower(chord.Key), mods, nil
	}
	return chord.Key, mods, nil
}

// mainLoading follows the loading of the page itself (not of its frames),
// for the phone's progress bar.
func (browser *Page) mainLoading(frame string, loading bool) {
	if browser.Ctx == nil || frame != string(chromedp.FromContext(browser.Ctx).Target.TargetID) {
		return
	}
	browser.mu.Lock()
	browser.loading = loading
	browser.mu.Unlock()
	browser.toPhones(nil)
}

// WaitClosed waits, a few seconds at most, for Chrome to have exited after
// the engine's context ended: 1loop quitting must not cut its last write.
func (engine *Engine) WaitClosed() {
	if engine == nil {
		return
	}
	engine.mu.Lock()
	closed := engine.closed
	engine.mu.Unlock()
	waitBrowserClosed(closed)
}

func waitBrowserClosed(closed <-chan struct{}) {
	if closed == nil {
		return
	}
	select {
	case <-closed:
	case <-time.After(4 * time.Second):
	}
}

func NamedKey(key string) string {
	switch key {
	case "Return", "Enter", "KP_Enter":
		return kb.Enter
	case "Tab":
		return kb.Tab
	case "BackSpace":
		return kb.Backspace
	case "Delete":
		return kb.Delete
	case "Escape":
		return kb.Escape
	case "Left":
		return kb.ArrowLeft
	case "Right":
		return kb.ArrowRight
	case "Up":
		return kb.ArrowUp
	case "Down":
		return kb.ArrowDown
	case "Home":
		return kb.Home
	case "End":
		return kb.End
	case "Prior":
		return kb.PageUp
	case "Next":
		return kb.PageDown
	}
	return ""
}

// browserProfileDir is the browser's profile, beside 1loop's state (so a
// copy of 1loop run with another config folder has a browser of its own).
var BrowserProfileOverride string

func ProfileDir() string {
	if BrowserProfileOverride != "" {
		return BrowserProfileOverride
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "browser")
}

func (engine *Engine) listen(root context.Context) {
	chromedp.ListenBrowser(root, func(event any) {
		switch event := event.(type) {
		case *target.EventTargetCreated:
			if value, ok := engine.Pages.Load(event.TargetInfo.OpenerID); ok && event.TargetInfo.Type == "page" {
				parent := value.(*Page)
				if parent.Life.Err() == nil && parent.OnPopup != nil {
					parent.OnPopup(event.TargetInfo.TargetID, event.TargetInfo.URL)
				}
			}
		case *target.EventTargetInfoChanged:
			if value, ok := engine.Pages.Load(event.TargetInfo.TargetID); ok {
				browser := value.(*Page)
				browser.change(func(info *Info) {
					info.Title = event.TargetInfo.Title
					info.URL = event.TargetInfo.URL
				})
			}
		case *target.EventTargetDestroyed:
			if value, ok := engine.Pages.Load(event.TargetID); ok {
				closedBrowser := value.(*Page)
				if closedBrowser.Life.Err() == nil && closedBrowser.OnClosed != nil {
					closedBrowser.OnClosed()
				}
			}
		}
	})
}
