package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"net/url"
	"os/exec"
	"path/filepath"
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
}

func newBrowserEngine(parent context.Context) *browserEngine {
	ctx, cancel := context.WithCancel(parent)
	return &browserEngine{ctx: ctx, cancel: cancel}
}

func (e *browserEngine) start() error {
	e.once.Do(func() {
		executable := ""
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome"} {
			if path, err := exec.LookPath(name); err == nil {
				executable = path
				break
			}
		}
		if executable == "" {
			e.err = fmt.Errorf("Install Chromium or Google Chrome to use browser tabs")
			return
		}
		opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(executable), chromedp.Flag("headless", "new"), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("enable-automation", false), chromedp.Flag("disable-blink-features", "AutomationControlled"))
		// Read native client hints before a process-wide UA override suppresses
		// them. The process-wide UA also covers a popup's very first request.
		profileAlloc, stopProfileAlloc := chromedp.NewExecAllocator(e.ctx, opts...)
		profileRoot, stopProfileRoot := chromedp.NewContext(profileAlloc)
		var native struct {
			UserAgent string
			Metadata  *emulation.UserAgentMetadata
		}
		e.err = chromedp.Run(profileRoot, chromedp.Navigate("chrome://version"), chromedp.Evaluate(`(async()=>({userAgent:navigator.userAgent,metadata:navigator.userAgentData?await navigator.userAgentData.getHighEntropyValues(['architecture','bitness','platformVersion','fullVersionList','model','wow64']):null}))()`, &native, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		stopProfileRoot()
		stopProfileAlloc()
		if e.err != nil {
			return
		}
		e.userAgent = strings.ReplaceAll(native.UserAgent, "HeadlessChrome/", "Chrome/")
		e.metadata = native.Metadata
		opts = append(opts, chromedp.UserAgent(e.userAgent))
		alloc, stopAlloc := chromedp.NewExecAllocator(e.ctx, opts...)
		root, stopRoot := chromedp.NewContext(alloc, chromedp.WithErrorf(func(f string, a ...any) { devf("browser", f, a...) }))
		context.AfterFunc(e.ctx, func() { stopRoot(); stopAlloc() })
		e.root = root
		chromedp.ListenBrowser(root, func(event any) {
			switch event := event.(type) {
			case *target.EventTargetCreated:
				if value, ok := e.pages.Load(event.TargetInfo.OpenerID); ok && event.TargetInfo.Type == "page" {
					parent := value.(*browserPage)
					if parent.onPopup != nil {
						parent.onPopup(event.TargetInfo.TargetID, event.TargetInfo.URL)
					}
				}
			case *target.EventTargetInfoChanged:
				if value, ok := e.pages.Load(event.TargetInfo.TargetID); ok {
					b := value.(*browserPage)
					b.change(func(i *browserInfo) { i.Title = event.TargetInfo.Title; i.URL = event.TargetInfo.URL })
				}
			case *target.EventTargetDestroyed:
				if value, ok := e.pages.Load(event.TargetID); ok {
					b := value.(*browserPage)
					if b.life.Err() == nil && b.onClosed != nil {
						b.onClosed()
					}
				}
			}
		})
		e.err = chromedp.Run(root)
	})
	return e.err
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

func newBrowserSession(e *browserEngine) *browserSession {
	ctx, cancel := context.WithCancel(e.ctx)
	return &browserSession{engine: e, ctx: ctx, cancel: cancel}
}

func (s *browserSession) start() error {
	s.once.Do(func() {
		if s.err = s.engine.start(); s.err != nil {
			return
		}
		if s.err = s.ctx.Err(); s.err != nil {
			return
		}
		root, cancel := chromedp.NewContext(s.engine.root, chromedp.WithNewBrowserContext())
		context.AfterFunc(s.ctx, cancel)
		s.root = root
		s.err = chromedp.Run(root)
	})
	return s.err
}

type browserPage struct {
	session      *browserSession
	life         context.Context
	cancel       func()
	ready        chan struct{}
	ctx          context.Context // published when ready closes
	gate         chan struct{}
	jobs         chan func(context.Context) error
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
	attachTarget target.ID
	closeOnce    sync.Once
	onPopup      func(target.ID, string)
	onClosed     func()
	view         *browserView // UI thread only
}

func newBrowserPage(s *browserSession, info browserInfo) *browserPage {
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
	life, cancel := context.WithCancel(s.ctx)
	b := &browserPage{session: s, life: life, cancel: cancel, ready: make(chan struct{}), gate: make(chan struct{}, 1), jobs: make(chan func(context.Context) error, 64), frames: make(chan *page.EventScreencastFrame, 1), info: info}
	s.refs++
	return b
}

func (b *browserPage) snapshot() browserInfo { b.mu.Lock(); defer b.mu.Unlock(); return b.info }
func (b *browserPage) change(f func(*browserInfo)) {
	b.mu.Lock()
	f(&b.info)
	info := b.info
	b.mu.Unlock()
	if b.onChange != nil {
		b.onChange(info)
	}
}
func (b *browserPage) addLog(level, text, source string) {
	b.mu.Lock()
	b.logIndex++
	b.logs = append(b.logs, browserLog{b.logIndex, time.Now().Format("15:04:05.000"), level, truncateText(text, 4000), source})
	if len(b.logs) > 500 {
		b.logs = append([]browserLog{}, b.logs[len(b.logs)-500:]...)
	}
	b.mu.Unlock()
	if b.onLogs != nil {
		b.onLogs()
	}
}
func (b *browserPage) logSnapshot(after int) []browserLog {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []browserLog
	for _, entry := range b.logs {
		if entry.Index > after {
			out = append(out, entry)
		}
	}
	return out
}

func (b *browserPage) start() {
	err := b.session.start()
	if err == nil {
		options := []chromedp.ContextOption{}
		if b.attachTarget != "" {
			options = append(options, chromedp.WithTargetID(b.attachTarget))
		}
		ctx, cancel := chromedp.NewContext(b.session.root, options...)
		context.AfterFunc(b.life, cancel)
		b.ctx = ctx
		chromedp.ListenTarget(ctx, b.event)
		info := b.snapshot()
		err = chromedp.Run(ctx,
			runtime.Enable(), log.Enable(), network.Enable(),
			emulation.SetUserAgentOverride(b.session.engine.userAgent).WithUserAgentMetadata(b.session.engine.metadata),
			browserMetrics(info.Width, info.Height, info.PixelRatio, info.Mobile))
	}
	if err != nil {
		b.change(func(i *browserInfo) { i.Error = err.Error() })
		close(b.ready)
		return
	}
	id := chromedp.FromContext(b.ctx).Target.TargetID
	b.session.engine.pages.Store(id, b)
	context.AfterFunc(b.life, func() { b.session.engine.pages.Delete(id) })
	if b.attachTarget == "" {
		navigation, stop := context.WithTimeout(b.ctx, 30*time.Second)
		err = chromedp.Run(navigation, chromedp.Navigate(b.snapshot().URL))
		stop()
	}
	if err != nil {
		b.change(func(i *browserInfo) { i.Error = err.Error() })
	}
	close(b.ready)
	go b.frameLoop()
	go func() {
		for {
			select {
			case <-b.life.Done():
				return
			case job := <-b.jobs:
				if err := job(b.life); err != nil && b.life.Err() == nil {
					b.addLog("error", err.Error(), "1loop")
				}
			}
		}
	}()
	b.mu.Lock()
	visible := b.visible
	b.mu.Unlock()
	if visible {
		b.stream()
	}
	go b.refreshTitle()
}

func (b *browserPage) event(event any) {
	switch e := event.(type) {
	case *page.EventScreencastFrame:
		// Event handlers must not issue synchronous CDP commands: that would
		// block the reader needed for their replies. Decode only the latest frame.
		go func() { _ = chromedp.Run(b.ctx, page.ScreencastFrameAck(e.SessionID)) }()
		select {
		case b.frames <- e:
		default:
			select {
			case <-b.frames:
			default:
			}
			select {
			case b.frames <- e:
			default:
			}
		}
	case *page.EventFrameNavigated:
		if e.Frame.ParentID == "" {
			b.change(func(i *browserInfo) { i.URL = e.Frame.URL; i.Error = "" })
		}
	case *page.EventNavigatedWithinDocument:
		b.change(func(i *browserInfo) { i.URL = e.URL })
	case *page.EventLoadEventFired:
		go b.refreshTitle()
	case *runtime.EventConsoleAPICalled:
		var parts []string
		for _, arg := range e.Args {
			if len(arg.Value) > 0 {
				parts = append(parts, string(arg.Value))
			} else {
				parts = append(parts, arg.Description)
			}
		}
		b.addLog(string(e.Type), strings.Join(parts, " "), "console")
	case *runtime.EventExceptionThrown:
		text := e.ExceptionDetails.Text
		if e.ExceptionDetails.Exception != nil {
			text += " · " + e.ExceptionDetails.Exception.Description
		}
		b.addLog("error", text, e.ExceptionDetails.URL)
	case *log.EventEntryAdded:
		b.addLog(string(e.Entry.Level), e.Entry.Text, e.Entry.URL)
	case *network.EventLoadingFailed:
		if !e.Canceled {
			b.addLog("error", e.ErrorText, "network")
		}
	case *network.EventResponseReceived:
		if e.Response.Status >= 400 {
			b.addLog("error", fmt.Sprintf("HTTP %d %s", e.Response.Status, e.Response.URL), "network")
		}
	}
}

func (b *browserPage) frameLoop() {
	for {
		select {
		case <-b.life.Done():
			return
		case frame := <-b.frames:
			data, err := base64.StdEncoding.DecodeString(frame.Data)
			if err != nil {
				continue
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err == nil && b.onFrame != nil {
				b.onFrame(img)
			}
		}
	}
}

func (b *browserPage) run(ctx context.Context, actions ...chromedp.Action) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.life.Done():
		return fmt.Errorf("Browser tab is closed")
	case <-b.ready:
	}
	if info := b.snapshot(); info.Error != "" && b.ctx == nil {
		return fmt.Errorf("%s", info.Error)
	}
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-b.life.Done():
		return fmt.Errorf("Browser tab is closed")
	}
	defer func() { <-b.gate }()
	operation, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return chromedp.Run(operation, actions...)
}

func (b *browserPage) async(job func(context.Context) error) {
	select {
	case b.jobs <- job:
	case <-b.life.Done():
	default:
		b.addLog("warn", "Input queue busy; try again.", "1loop")
	}
}
func (b *browserPage) setVisible(visible bool) {
	b.mu.Lock()
	changed := b.visible != visible
	b.visible = visible
	b.mu.Unlock()
	if changed {
		select {
		case <-b.ready:
			if b.ctx != nil {
				go b.stream()
			}
		default:
		}
	}
}
func (b *browserPage) stream() {
	_ = b.run(b.life, chromedp.ActionFunc(func(ctx context.Context) error {
		b.mu.Lock()
		visible := b.visible
		b.mu.Unlock()
		if visible {
			if err := page.BringToFront().Do(ctx); err != nil {
				return err
			}
			return page.StartScreencast().WithFormat(page.ScreencastFormatPng).WithEveryNthFrame(1).Do(ctx)
		}
		return page.StopScreencast().Do(ctx)
	}))
}

func (b *browserPage) close() {
	b.closeOnce.Do(func() {
		b.cancel()
		b.session.refs--
		if b.session.refs == 0 {
			b.session.cancel()
		}
	})
}
func (b *browserPage) refreshTitle() {
	var title, location string
	if b.run(b.life, chromedp.Title(&title), chromedp.Location(&location)) == nil {
		b.change(func(i *browserInfo) { i.Title = title; i.URL = location })
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

func (b *browserPage) navigate(ctx context.Context, action, raw string) error {
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
	err := b.run(ctx, job)
	if err == nil {
		b.refreshTitle()
	}
	return err
}

func (b *browserPage) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	var value json.RawMessage
	err := b.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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
	return emulation.SetDeviceMetricsOverride(int64(width), int64(height), ratio, mobile).WithViewport(&page.Viewport{Width: float64(width), Height: float64(height), Scale: 1})
}

func (b *browserPage) resize(ctx context.Context, width, height int, mobile, adaptive bool, pixelRatio ...float64) error {
	if width < 240 || width > 3840 || height < 200 || height > 2160 {
		return fmt.Errorf("Viewport must be 240–3840 × 200–2160 CSS pixels")
	}
	ratio := b.snapshot().PixelRatio
	if len(pixelRatio) > 0 {
		ratio = pixelRatio[0]
	}
	err := b.run(ctx, browserMetrics(width, height, ratio, mobile))
	if err == nil {
		b.change(func(i *browserInfo) {
			i.Width = width
			i.Height = height
			i.Mobile = mobile
			i.Adaptive = adaptive
			i.PixelRatio = ratio
		})
	}
	return err
}

func (b *browserPage) point(ctx context.Context, selector string, x, y float64) (float64, float64, error) {
	if selector != "" {
		encoded, _ := json.Marshal(selector)
		raw, err := b.evaluate(ctx, `(()=>{const e=document.querySelector(`+string(encoded)+`);if(!e)throw Error('Element not found');e.scrollIntoView({block:'center',inline:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`)
		if err != nil {
			return 0, 0, err
		}
		var p struct{ X, Y float64 }
		if err = json.Unmarshal(raw, &p); err != nil {
			return 0, 0, err
		}
		x, y = p.X, p.Y
	}
	info := b.snapshot()
	if x < 0 || y < 0 || x >= float64(info.Width) || y >= float64(info.Height) {
		return 0, 0, fmt.Errorf("Coordinates are outside the viewport")
	}
	return x, y, nil
}
func (b *browserPage) cursor(x, y float64) {
	if b.onCursor != nil {
		b.onCursor(x, y)
	}
}
func (b *browserPage) interact(ctx context.Context, action, selector, text string, x, y, dx, dy float64) error {
	if action == "click" || action == "move" || action == "scroll" || (action == "type" && selector != "") {
		var err error
		x, y, err = b.point(ctx, selector, x, y)
		if err != nil {
			return err
		}
		b.cursor(x, y)
	}
	switch action {
	case "move":
		return b.run(ctx, input.DispatchMouseEvent(input.MouseMoved, x, y))
	case "click":
		return b.run(ctx, chromedp.MouseClickXY(x, y))
	case "scroll":
		return b.run(ctx, input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(dx).WithDeltaY(dy))
	case "type":
		if selector != "" {
			if err := b.run(ctx, chromedp.MouseClickXY(x, y)); err != nil {
				return err
			}
		}
		return b.run(ctx, input.InsertText(text))
	case "key":
		key, modifiers := browserKeyChord(text)
		return b.run(ctx, chromedp.KeyEvent(key, chromedp.KeyModifiers(modifiers...)))
	default:
		return fmt.Errorf("Unknown browser interaction")
	}
}

func (b *browserPage) capture(ctx context.Context, full bool) (ImagePart, error) {
	var data []byte
	action := chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		data, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).WithFromSurface(true).WithCaptureBeyondViewport(full).Do(ctx)
		return err
	})
	err := b.run(ctx, action)
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
