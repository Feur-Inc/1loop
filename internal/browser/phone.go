package browser

// A browser tab watched from the phone. The phone gets the page as the
// screencast paints it (the frames the window shows, made into JPEG at a
// phone's width), its state (address, title, loading) and the agent's
// cursor, as a stream: no picture is asked for and waited for, each one
// arrives as soon as Chrome draws it.
//
// The stream is a sequence of messages: one byte for the kind, four for the
// length (big endian), then the payload. 'F' is a JPEG frame, 'S' the state
// as JSON (see phoneState).

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"1loop/internal/core"
)

const (
	phoneFrameWidth   = 1080 // wider frames are scaled down to this
	phoneFrameQuality = 72
)

type PhoneState struct {
	Adaptive   bool         `json:"adaptive"`
	Mobile     bool         `json:"mobile"`
	Native     bool         `json:"native,omitempty"`
	PixelRatio float64      `json:"pixelRatio"`
	URL        string       `json:"url"`
	Title      string       `json:"title"`
	Loading    bool         `json:"loading"`
	Width      int          `json:"width"`  // the page's viewport in CSS pixels,
	Height     int          `json:"height"` // what the cursor's position is in
	Cursor     *phoneCursor `json:"cursor,omitempty"`
}

type phoneCursor struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// phoneWatcher keeps only the latest frame and state for a phone: a slow
// network drops pictures, it never queues them.
type phoneWatcher struct {
	lossless bool
	mu       sync.Mutex
	frame    []byte
	state    []byte
	wake     chan struct{}
}

func (watcher *phoneWatcher) put(frame, state []byte) {
	watcher.mu.Lock()
	if frame != nil {
		watcher.frame = frame
	}
	if state != nil {
		watcher.state = state
	}
	watcher.mu.Unlock()
	select {
	case watcher.wake <- struct{}{}:
	default:
	}
}

func (watcher *phoneWatcher) take() (frame, state []byte) {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	frame, state = watcher.frame, watcher.state
	watcher.frame, watcher.state = nil, nil
	return
}

// phonesWatching tells whether any phone follows the page.
func (browser *Page) phonesWatching() bool {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	return len(browser.phones) > 0
}

func (browser *Page) phoneStateJSON() []byte {
	browser.mu.Lock()
	state := PhoneState{
		Adaptive:   browser.info.Adaptive,
		Mobile:     browser.info.Mobile,
		Native:     browser.info.Native,
		PixelRatio: browser.info.PixelRatio,
		URL:        browser.info.URL,
		Title:      browser.info.Title,
		Loading:    browser.loading,
		Width:      browser.info.Width,
		Height:     browser.info.Height,
	}
	if browser.cursorShown {
		state.Cursor = &phoneCursor{browser.cursorX, browser.cursorY}
	}
	browser.mu.Unlock()
	data, _ := json.Marshal(state)
	return data
}

// toPhones sends the page's state, and a frame when there is one, to
// every phone that follows it.
func (browser *Page) toPhones(frame []byte, native ...[]byte) {
	browser.mu.Lock()
	watchers := make([]*phoneWatcher, 0, len(browser.phones))
	for watcher := range browser.phones {
		watchers = append(watchers, watcher)
	}
	if frame != nil {
		browser.phoneFrame = frame
	}
	if len(native) > 0 {
		browser.nativeFrame = native[0]
	}
	browser.mu.Unlock()
	if len(watchers) == 0 {
		return
	}
	state := browser.phoneStateJSON()
	for _, watcher := range watchers {
		if watcher.lossless && len(native) > 0 {
			watcher.put(native[0], state)
		} else {
			watcher.put(frame, state)
		}
	}
}

// phoneJPEG makes a decoded frame into what a phone shows.
func phoneJPEG(img image.Image) []byte {
	bounds := img.Bounds()
	if bounds.Dx() > phoneFrameWidth {
		height := bounds.Dy() * phoneFrameWidth / bounds.Dx()
		scaled := image.NewRGBA(image.Rect(0, 0, phoneFrameWidth, height))
		xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, bounds, xdraw.Src, nil)
		img = scaled
	}
	var out bytes.Buffer
	if jpeg.Encode(&out, img, &jpeg.Options{Quality: phoneFrameQuality}) != nil {
		return nil
	}
	return out.Bytes()
}

// watch adds a phone; the returned function removes it.
func (browser *Page) watch(lossless ...bool) (*phoneWatcher, func()) {
	watcher := &phoneWatcher{lossless: len(lossless) > 0 && lossless[0], wake: make(chan struct{}, 1)}
	browser.mu.Lock()
	if browser.phones == nil {
		browser.phones = map[*phoneWatcher]bool{}
	}
	first := len(browser.phones) == 0
	browser.phones[watcher] = true
	last := browser.phoneFrame
	if watcher.lossless {
		last = browser.nativeFrame
	}
	browser.mu.Unlock()
	watcher.put(last, browser.phoneStateJSON())
	if first {
		browser.restream()
	}
	return watcher, func() {
		browser.mu.Lock()
		delete(browser.phones, watcher)
		none := len(browser.phones) == 0
		browser.mu.Unlock()
		if none {
			browser.restream()
		}
	}
}

// restream starts or stops the screencast for what now watches the page.
func (browser *Page) restream() {
	select {
	case <-browser.ready:
		if browser.Ctx != nil {
			go browser.stream()
		}
	default:
	}
}

// NavigateSoon starts a navigation and returns at once: the phone sees the
// page load through its stream, and its next touches do not wait for it.
func (browser *Page) NavigateSoon(action, address string) error {
	if action == "navigate" {
		if _, err := URL(address); err != nil {
			return err
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(browser.Life, 45*time.Second)
		defer cancel()
		if err := browser.Navigate(ctx, action, address); err != nil && browser.Life.Err() == nil {
			browser.AddLog("error", err.Error(), "1loop")
		}
	}()
	return nil
}

func (browser *Page) needsPhoneJPEG() bool {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	for watcher := range browser.phones {
		if !watcher.lossless {
			return true
		}
	}
	return false
}

func ServeBrowserStream(writer http.ResponseWriter, req *http.Request, browser *Page, revoked <-chan struct{}) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		core.Fail(writer, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	watcher, stop := browser.watch(req.URL.Query().Get("format") == "png")
	defer stop()
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	send := func(kind byte, payload []byte) error {
		var head [5]byte
		head[0] = kind
		binary.BigEndian.PutUint32(head[1:], uint32(len(payload)))
		if _, err := writer.Write(head[:]); err != nil {
			return err
		}
		_, err := writer.Write(payload)
		return err
	}
	ping := time.NewTicker(15 * time.Second) // the state again: keeps the stream alive
	defer ping.Stop()
	ctx := req.Context()
	for {
		select {
		case <-revoked:
			return
		case <-ctx.Done():
			return
		case <-browser.Life.Done():
			return
		case <-ping.C:
			watcher.put(nil, browser.phoneStateJSON())
			continue
		case <-watcher.wake:
		}
		frame, state := watcher.take()
		if state != nil && send('S', state) != nil {
			return
		}
		if frame != nil && send('F', frame) != nil {
			return
		}
		flusher.Flush()
	}
}
