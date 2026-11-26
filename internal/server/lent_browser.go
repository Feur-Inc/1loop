// SPDX-License-Identifier: MPL-2.0

package server

// Browser passthrough (issue #406): on a server with no Chromium, a paired
// desktop app may lend its computer's browser. It opens GET
// /api/browser/attach, and Browser Use reaches its browser through it: the
// engine connects to a loopback WebSocket here whose DevTools messages go
// to the client and back (the protocol is internal/browser/lend.go's). The
// tools' code and results are a local Chromium's.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// lendReady is how long a client may take to start its browser: the first
// time, it downloads one.
var lendReady = 5 * time.Minute

// lendWrite is how long a write on a lending socket may take: every write,
// the pongs that answer the client's pings too, gets its own deadline.
var lendWrite = time.Minute

// browserLenders are the clients lending their browser, the last one to
// come first: the engine uses it (browser.RemoteHost).
type browserLenders struct {
	mu   sync.Mutex
	list []*browserLender
	// changed is told, outside mu, when the lender in use came or went
	// (replaced), or started or stopped being used.
	changed func(replaced bool)
}

// browserLender is a client's socket.
type browserLender struct {
	device  string
	conn    net.Conn
	reader  io.Reader
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int
	links   map[int]*lentLink
	gone    chan struct{}
	once    sync.Once
	used    func() // its connections began or ended
}

// lentLink is one DevTools connection through the client: the engine's
// loopback socket on one side, a browser the client started on the other.
type lentLink struct {
	ready   chan string // "" once the client's browser answers, else why not
	mu      sync.Mutex
	engine  net.Conn // the engine's socket, once it connected
	writeMu sync.Mutex
	closed  bool
}

// current is the lender the engine uses, nil when none lends.
func (lenders *browserLenders) current() *browserLender {
	if lenders == nil { // a server made without its engine (tests)
		return nil
	}
	lenders.mu.Lock()
	defer lenders.mu.Unlock()
	if len(lenders.list) == 0 {
		return nil
	}
	return lenders.list[len(lenders.list)-1]
}

// Connect is browser.RemoteHost's: a connection to the current lender's
// browser, open until ctx ends.
func (lenders *browserLenders) Connect(ctx context.Context) (string, error) {
	lender := lenders.current()
	if lender == nil {
		return "", errors.New(browser.NoBrowserProblem)
	}
	return lender.connect(ctx)
}

func (lenders *browserLenders) add(lender *browserLender) {
	lenders.mu.Lock()
	lenders.list = append(lenders.list, lender)
	lenders.mu.Unlock()
	lender.used = func() {
		if lenders.current() == lender {
			lenders.notify(false)
		}
	}
	lenders.notify(true)
}

func (lenders *browserLenders) remove(lender *browserLender) {
	lenders.mu.Lock()
	wasCurrent := len(lenders.list) > 0 && lenders.list[len(lenders.list)-1] == lender
	for i, each := range lenders.list {
		if each == lender {
			lenders.list = append(lenders.list[:i:i], lenders.list[i+1:]...)
			break
		}
	}
	lenders.mu.Unlock()
	if wasCurrent {
		lenders.notify(true)
	}
}

func (lenders *browserLenders) notify(replaced bool) {
	if lenders.changed != nil {
		lenders.changed(replaced)
	}
}

// inUse says whether the server's agent has a connection to the browser.
func (lender *browserLender) inUse() bool {
	lender.mu.Lock()
	defer lender.mu.Unlock()
	return len(lender.links) > 0
}

// left says whether the client went away.
func (lender *browserLender) left() bool {
	select {
	case <-lender.gone:
		return true
	default:
		return false
	}
}

func (lender *browserLender) send(frame browser.LendFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	lender.writeMu.Lock()
	defer lender.writeMu.Unlock()
	lender.conn.SetWriteDeadline(time.Now().Add(lendWrite))
	return wsutil.WriteServerText(lender.conn, data)
}

// connect asks the client for a browser, and gives the engine a loopback
// socket to it.
func (lender *browserLender) connect(ctx context.Context) (string, error) {
	lender.mu.Lock()
	lender.next++
	id := lender.next
	link := &lentLink{ready: make(chan string, 1)}
	if lender.links == nil {
		lender.links = map[int]*lentLink{}
	}
	lender.links[id] = link
	lender.mu.Unlock()
	fail := func(err error) (string, error) {
		lender.drop(id, true)
		return "", err
	}
	if err := lender.send(browser.LendFrame{Conn: id, Open: true}); err != nil {
		return fail(lender.leftError(err))
	}
	select {
	case problem := <-link.ready:
		if problem != "" {
			return fail(fmt.Errorf("%s could not lend its browser: %s", lender.device, problem))
		}
	case <-lender.gone:
		return fail(lender.leftError(nil))
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-time.After(lendReady):
		return fail(fmt.Errorf("%s did not start its browser in time", lender.device))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	if lender.used != nil {
		lender.used()
	}
	route := "/" + core.Identifier()
	stop := context.AfterFunc(ctx, func() { listener.Close(); lender.drop(id, true) })
	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Only the engine knows the route: another local process's
			// connection is refused and does not take its place.
			upgrader := ws.Upgrader{OnRequest: func(uri []byte) error {
				if subtle.ConstantTimeCompare(uri, []byte(route)) != 1 {
					return ws.RejectConnectionError(ws.RejectionStatus(http.StatusNotFound))
				}
				return nil
			}}
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			if _, err := upgrader.Upgrade(conn); err != nil {
				conn.Close()
				continue
			}
			conn.SetDeadline(time.Time{})
			listener.Close()
			link.mu.Lock()
			if link.closed {
				link.mu.Unlock()
				conn.Close()
				return
			}
			link.engine = conn
			link.mu.Unlock()
			for {
				data, err := wsutil.ReadClientText(lockedConn{conn, conn, &link.writeMu})
				if err != nil {
					stop()
					lender.drop(id, true)
					return
				}
				if lender.send(browser.LendFrame{Conn: id, Message: data}) != nil {
					stop()
					lender.drop(id, false)
					return
				}
			}
		}
	}()
	return "ws://" + listener.Addr().String() + route, nil
}

// lockedConn reads a socket and writes the control frames its reader
// answers under the lock of its other writes.
type lockedConn struct {
	io.Reader
	conn net.Conn
	mu   *sync.Mutex
}

func (locked lockedConn) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	locked.conn.SetWriteDeadline(time.Now().Add(lendWrite)) // not the last write's, long past on an idle socket
	return locked.conn.Write(data)
}

// leftError says the client went away.
func (lender *browserLender) leftError(err error) error {
	if err != nil && !lender.left() {
		return fmt.Errorf("%s's browser is unreachable: %w", lender.device, err)
	}
	return fmt.Errorf("%s stopped lending its browser", lender.device)
}

// drop ends a connection: the engine's socket closes, and the client is
// told to stop the browser unless it ended it.
func (lender *browserLender) drop(id int, tell bool) {
	lender.mu.Lock()
	link := lender.links[id]
	delete(lender.links, id)
	lender.mu.Unlock()
	if link == nil {
		return
	}
	link.mu.Lock()
	link.closed = true
	engine := link.engine
	link.mu.Unlock()
	if engine != nil {
		engine.Close()
	}
	if tell && !lender.left() {
		lender.send(browser.LendFrame{Conn: id, Close: true})
	}
	if lender.used != nil {
		lender.used()
	}
}

// run reads the client's frames until it goes away.
func (lender *browserLender) run() {
	defer lender.leave()
	for {
		data, err := wsutil.ReadClientText(lockedConn{lender.reader, lender.conn, &lender.writeMu})
		if err != nil {
			return
		}
		var frame browser.LendFrame
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		lender.mu.Lock()
		link := lender.links[frame.Conn]
		lender.mu.Unlock()
		if link == nil {
			continue
		}
		switch {
		case frame.Ready:
			select {
			case link.ready <- "":
			default:
			}
		case frame.Close:
			problem := frame.Error
			if problem == "" {
				problem = "it closed the connection"
			}
			select {
			case link.ready <- problem:
			default:
			}
			lender.drop(frame.Conn, false)
		case frame.Message != nil:
			link.mu.Lock()
			engine := link.engine
			link.mu.Unlock()
			if engine == nil {
				continue
			}
			link.writeMu.Lock()
			engine.SetWriteDeadline(time.Now().Add(lendWrite))
			err := wsutil.WriteServerText(engine, frame.Message)
			link.writeMu.Unlock()
			if err != nil {
				lender.drop(frame.Conn, true)
			}
		}
	}
}

// leave ends every connection through a client that went away: the
// engine's sockets close, and the calls on them fail.
func (lender *browserLender) leave() {
	lender.once.Do(func() {
		close(lender.gone)
		lender.conn.Close()
		lender.mu.Lock()
		ids := make([]int, 0, len(lender.links))
		for id := range lender.links {
			ids = append(ids, id)
		}
		lender.mu.Unlock()
		for _, id := range ids {
			lender.drop(id, false)
		}
	})
}

// pairedDeviceName is the name of the paired device a request's token is
// for; ok is false for any other token, the phone app's local one too.
func (server *headlessServer) pairedDeviceName(req *http.Request) (string, bool) {
	hash := remoteapi.RequestTokenHash(req)
	if hash == "" {
		return "", false
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, device := range server.devices {
		if subtle.ConstantTimeCompare([]byte(device.TokenHash), []byte(hash)) == 1 {
			return device.Name, true
		}
	}
	return "", false
}

// attachBrowser is GET /api/browser/attach: a paired device lends its
// browser until it closes the socket, or is unpaired.
func (server *headlessServer) attachBrowser(w http.ResponseWriter, r *http.Request) {
	device, ok := server.pairedDeviceName(r)
	if !ok {
		core.Fail(w, 401, "Only a paired device can lend its browser")
		return
	}
	conn, reader, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return // UpgradeHTTP answered the error
	}
	lender := &browserLender{device: device, conn: conn, reader: conn, links: map[int]*lentLink{}, gone: make(chan struct{})}
	if reader != nil { // it holds what the client sent right after its handshake
		lender.reader = reader.Reader
	}
	revoked, release := server.hub.Stream()
	defer release()
	go func() {
		select {
		case <-revoked: // a device was unpaired: this one comes back if it still may
		case <-server.ctx.Done():
		case <-lender.gone:
		}
		lender.leave()
	}()
	server.lenders.add(lender)
	lender.run()
	server.lenders.remove(lender)
}

// lentBrowserChanged follows the lender in use: the clients learn which
// device lends its browser, and when the lender changes, the tabs that ran
// in the browser gone, or failed for want of one, open again in the next.
func (server *headlessServer) lentBrowserChanged(replaced bool) {
	if replaced && server.engine.Native == nil && browser.Disabled() {
		server.reopenBrowsers()
	}
	server.mu.Lock()
	server.broadcastLocked("browser", server.browserStateLocked())
	server.mu.Unlock()
}

// reopenBrowsers closes every chat's browser tabs, keeping where they
// were, so that they open again, in a new browser, when next used.
func (server *headlessServer) reopenBrowsers() {
	server.mu.Lock()
	var closing []*serverTab
	for _, chat := range server.panelContextsLocked() {
		if chat.session == nil {
			continue
		}
		chat.Browsers = nil
		kept := []*serverTab{}
		for _, tab := range chat.tabs {
			if tab.browser == nil {
				kept = append(kept, tab)
				continue
			}
			info := tab.browser.Snapshot()
			info.Error, info.Status = "", ""
			chat.Browsers = append(chat.Browsers, info)
			closing = append(closing, tab)
		}
		chat.tabs = kept
		chat.session = nil
	}
	server.mu.Unlock()
	for _, tab := range closing {
		tab.close()
	}
}

// browserStateLocked is /api/state's part about the browser: whether
// Browser Use works, whether the server has a browser of its own, and the
// device lending one.
func (server *headlessServer) browserStateLocked() map[string]any {
	state := map[string]any{"browser": server.hasBrowser(), "ownBrowser": server.ownBrowser()}
	if lender := server.lenders.current(); lender != nil {
		state["lentBrowser"] = map[string]any{"device": lender.device, "inUse": lender.inUse(), "used": !server.ownBrowser()}
	}
	return state
}
