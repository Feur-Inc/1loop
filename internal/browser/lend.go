// SPDX-License-Identifier: MPL-2.0

package browser

// Lending this computer's browser to a server (issue #406): the desktop app
// opens GET /api/browser/attach on a paired server with no Chromium, and
// relays the DevTools protocol between that server's Browser Use and a
// headless Chromium it starts here, with a profile of its own, never the
// user's. The server drives it as it drives a local Chromium (remote.go).
//
// The socket carries LendFrames as JSON text messages. The server asks for
// a browser with Open on a new connection number; the client starts one and
// answers Ready, or Close with the reason it could not. Then Message
// carries the DevTools messages either way, until Close from either side.
// Each connection is a browser of its own, started for it and stopped with
// it: no browser runs here while the server's agent does not use one.

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"1loop/internal/platform"
)

// LendPath is the server's socket for a lent browser.
const LendPath = "/api/browser/attach"

// LendFrame is one message on the lending socket, either way.
type LendFrame struct {
	Conn    int             `json:"conn"`
	Open    bool            `json:"open,omitempty"`    // server: start a browser for this connection
	Ready   bool            `json:"ready,omitempty"`   // client: its browser answers
	Close   bool            `json:"close,omitempty"`   // either: the connection ended
	Error   string          `json:"error,omitempty"`   // why, with Close
	Message json.RawMessage `json:"message,omitempty"` // a DevTools message
}

// LentProfileDir is the profile of the browser lent to servers: its own,
// apart from the user's browsers and from this computer's Browser Use.
func LentProfileDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "lent-browser")
}

// Lending lends this computer's browser to one server.
type Lending struct {
	URL   string // the server's address, https://host:port
	Token string // this device's pairing token there
	TLS   *tls.Config
	// OnAttach says the server took the offer: it may use the browser.
	OnAttach func()
	// OnUse says whether the server's agent is using the browser now.
	OnUse func(bool)
	// Launch starts a browser and returns its DevTools WebSocket address
	// and how to stop it; nil starts a headless Chromium (LaunchLent).
	Launch func(context.Context) (string, func(), error)
}

// ErrLendUnsupported is a server from before browser lending.
var ErrLendUnsupported = errors.New("This server's version cannot use this computer's browser: update the server")

// Run lends the browser until ctx ends or the connection to the server
// does. It returns why it ended.
func (lending Lending) Run(ctx context.Context) error {
	address := lending.URL
	switch {
	case strings.HasPrefix(address, "https://"):
		address = "wss://" + strings.TrimPrefix(address, "https://")
	case strings.HasPrefix(address, "http://"):
		address = "ws://" + strings.TrimPrefix(address, "http://")
	}
	dialer := ws.Dialer{
		TLSConfig: lending.TLS,
		Header:    ws.HandshakeHeaderHTTP(http.Header{"Authorization": {"Bearer " + lending.Token}}),
		Timeout:   30 * time.Second,
	}
	conn, reader, _, err := dialer.Dial(ctx, strings.TrimRight(address, "/")+LendPath)
	if err != nil {
		var status ws.StatusError
		if errors.As(err, &status) && (int(status) == http.StatusNotFound || int(status) == http.StatusMethodNotAllowed) {
			return ErrLendUnsupported
		}
		return err
	}
	if lending.OnAttach != nil {
		lending.OnAttach()
	}
	relay := &lendRelay{lending: lending, conn: conn, links: map[int]*lendLink{}}
	relay.reader = io.Reader(conn)
	if reader != nil { // what the server sent right after its handshake
		relay.reader = io.MultiReader(reader, conn)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(ctx, func() { conn.Close() })
	defer relay.closeAll()
	go relay.ping(ctx)
	for {
		data, err := wsutil.ReadServerText(lockedConn{relay.reader, relay.conn, &relay.writeMu})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("the server closed the connection: %w", err)
		}
		var frame LendFrame
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch {
		case frame.Open:
			go relay.open(ctx, frame.Conn)
		case frame.Close:
			relay.end(frame.Conn, "")
		case frame.Message != nil:
			relay.forward(frame.Conn, frame.Message)
		}
	}
}

type lendRelay struct {
	lending Lending
	conn    net.Conn
	reader  io.Reader
	writeMu sync.Mutex
	mu      sync.Mutex
	links   map[int]*lendLink
	used    bool
	// The browsers run one after the other, on one profile: a new one
	// waits for the previous one to exit.
	launchMu sync.Mutex
}

// lockedConn reads a socket and writes the control frames its reader
// answers (pongs) under the lock of the socket's other writes.
type lockedConn struct {
	io.Reader
	conn net.Conn
	mu   *sync.Mutex
}

func (locked lockedConn) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.conn.Write(data)
}

// lendLink is one connection: a browser started for it.
type lendLink struct {
	browser net.Conn // its DevTools socket
	writeMu sync.Mutex
	stop    func()
	once    sync.Once
}

func (relay *lendRelay) send(frame LendFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	relay.writeMu.Lock()
	defer relay.writeMu.Unlock()
	relay.conn.SetWriteDeadline(time.Now().Add(time.Minute))
	return wsutil.WriteClientText(relay.conn, data)
}

// ping keeps the socket alive through proxies that close idle ones.
func (relay *lendRelay) ping(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			relay.writeMu.Lock()
			relay.conn.SetWriteDeadline(time.Now().Add(time.Minute))
			err := wsutil.WriteClientMessage(relay.conn, ws.OpPing, nil)
			relay.writeMu.Unlock()
			if err != nil {
				relay.conn.Close()
				return
			}
		}
	}
}

// open starts a browser for a connection the server asks for.
func (relay *lendRelay) open(ctx context.Context, id int) {
	launch := relay.lending.Launch
	if launch == nil {
		launch = LaunchLent
	}
	relay.mu.Lock()
	if relay.links[id] != nil {
		relay.mu.Unlock()
		return
	}
	link := &lendLink{}
	relay.links[id] = link
	relay.mu.Unlock()
	relay.report()
	relay.launchMu.Lock()
	address, stop, err := launch(ctx)
	var browser net.Conn
	if err == nil {
		browser, _, _, err = ws.Dial(ctx, address)
		if err != nil {
			stop()
		}
	}
	if err != nil {
		relay.launchMu.Unlock()
		relay.end(id, err.Error())
		return
	}
	link.browser = browser
	link.stop = func() { browser.Close(); stop(); relay.launchMu.Unlock() }
	relay.mu.Lock()
	gone := relay.links[id] != link // the server closed it meanwhile
	relay.mu.Unlock()
	if gone {
		link.once.Do(link.stop)
		return
	}
	if relay.send(LendFrame{Conn: id, Ready: true}) != nil {
		relay.end(id, "")
		return
	}
	for {
		data, err := wsutil.ReadServerText(lockedConn{browser, browser, &link.writeMu})
		if err != nil {
			relay.end(id, "This computer's browser closed")
			return
		}
		if relay.send(LendFrame{Conn: id, Message: data}) != nil {
			relay.end(id, "")
			return
		}
	}
}

// forward passes a DevTools command to the connection's browser, unless it
// reaches beyond the web (lendRefusal): then the server gets an error
// answer, as from the browser.
func (relay *lendRelay) forward(id int, message json.RawMessage) {
	relay.mu.Lock()
	link := relay.links[id]
	relay.mu.Unlock()
	if link == nil || link.browser == nil {
		relay.send(LendFrame{Conn: id, Close: true, Error: "This connection is closed"})
		return
	}
	if reason := lendRefusal(message); reason != "" {
		var command struct {
			ID        int64  `json:"id"`
			SessionID string `json:"sessionId,omitempty"`
		}
		json.Unmarshal(message, &command)
		answer, _ := json.Marshal(map[string]any{"id": command.ID, "sessionId": command.SessionID, "error": map[string]any{"code": -32000, "message": reason}})
		relay.send(LendFrame{Conn: id, Message: answer})
		return
	}
	link.writeMu.Lock()
	err := wsutil.WriteClientText(link.browser, message)
	link.writeMu.Unlock()
	if err != nil {
		relay.end(id, "This computer's browser closed")
	}
}

// end closes a connection and stops its browser; with a reason, the
// server learns it.
func (relay *lendRelay) end(id int, reason string) {
	relay.mu.Lock()
	link := relay.links[id]
	delete(relay.links, id)
	relay.mu.Unlock()
	if link == nil {
		return
	}
	if reason != "" {
		relay.send(LendFrame{Conn: id, Close: true, Error: reason})
	}
	if link.stop != nil {
		link.once.Do(link.stop)
	}
	relay.report()
}

func (relay *lendRelay) closeAll() {
	relay.mu.Lock()
	ids := make([]int, 0, len(relay.links))
	for id := range relay.links {
		ids = append(ids, id)
	}
	relay.mu.Unlock()
	for _, id := range ids {
		relay.end(id, "")
	}
}

// report tells whether a browser is lent now, when that changed.
func (relay *lendRelay) report() {
	relay.mu.Lock()
	used := len(relay.links) > 0
	changed := used != relay.used
	relay.used = used
	relay.mu.Unlock()
	if changed && relay.lending.OnUse != nil {
		relay.lending.OnUse(used)
	}
}

// lendRefusal is why a DevTools command does not reach the lent browser:
// what would read or write this computer's files rather than browse the
// web. The server's own checks (nativeURL) refuse the same addresses; this
// holds for a server that does not, or was made not to.
func lendRefusal(raw json.RawMessage) string {
	var command struct {
		Method string `json:"method"`
		Params struct {
			URL      string `json:"url"`
			Behavior string `json:"behavior"`
			Data     struct {
				Files []string `json:"files"`
			} `json:"data"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &command) != nil {
		return "This computer's browser refuses a malformed command"
	}
	switch command.Method {
	case "Page.navigate", "Target.createTarget", "Network.loadNetworkResource":
		if command.Params.URL != "" && nativeURL(command.Params.URL) != nil {
			return "This computer's browser opens http, https and about:blank addresses only"
		}
	case "Browser.setDownloadBehavior", "Page.setDownloadBehavior":
		if command.Params.Behavior != "deny" && command.Params.Behavior != "default" {
			return "This computer's browser does not save downloads for the server"
		}
	case "DOM.setFileInputFiles":
		return "This computer's browser does not send its files to the server"
	case "Input.dispatchDragEvent":
		if len(command.Params.Data.Files) > 0 {
			return "This computer's browser does not send its files to the server"
		}
	case "Browser.grantPermissions", "Browser.setPermission":
		return "This computer's browser does not grant permissions to the server"
	}
	return ""
}

var devToolsListening = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// LaunchLent starts a headless Chromium for a server, on the lent
// profile: the computer's Chromium, Chrome or Edge, else 1loop's Chromium,
// downloaded the first time (anyBrowser).
func LaunchLent(ctx context.Context) (string, func(), error) {
	binary, err := anyBrowser(ctx)
	if err != nil {
		return "", nil, err
	}
	dir := LentProfileDir()
	if dir == "" {
		return "", nil, fmt.Errorf("This computer has no folder for the lent browser's profile")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	address, stop, err := launchLent(ctx, binary, dir, false)
	if err != nil && binary.own && strings.Contains(err.Error(), "No usable sandbox") {
		platform.Devf("browser", "1loop's Chromium has no usable sandbox here: lending it without")
		address, stop, err = launchLent(ctx, binary, dir, true)
	}
	if err != nil && binary.own {
		err = chromiumStartError(err)
	}
	return address, stop, err
}

func launchLent(ctx context.Context, binary browserBinary, dir string, noSandbox bool) (string, func(), error) {
	args := []string{
		"--headless=new", "--remote-debugging-port=0", "--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-background-timer-throttling", "--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding", "--disable-breakpad", "--disable-client-side-phishing-detection",
		"--disable-default-apps", "--disable-extensions", "--disable-hang-monitor", "--disable-sync",
		"--disable-popup-blocking", "--disable-prompt-on-repost", "--metrics-recording-only",
		"--password-store=basic", "--use-mock-keychain", "--force-color-profile=srgb",
		"--disable-blink-features=AutomationControlled", "--disable-dev-shm-usage",
	}
	if noSandbox {
		args = append(args, "--no-sandbox")
	}
	command := exec.Command(binary.path, append(args, "about:blank")...)
	command.Env = append(os.Environ(), binary.env...)
	stderr, err := command.StderrPipe()
	if err != nil {
		return "", nil, err
	}
	if err := command.Start(); err != nil {
		return "", nil, err
	}
	exited := make(chan struct{})
	found := make(chan string, 1)
	var output strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if match := devToolsListening.FindStringSubmatch(line); match != nil {
				select {
				case found <- match[1]:
				default:
				}
			} else if output.Len() < 4000 {
				output.WriteString(line + "\n")
			}
		}
		command.Wait()
		close(exited)
	}()
	stop := func() {
		select {
		case <-exited: // the server closed it (Browser.close), its profile saved
		case <-time.After(3 * time.Second):
			command.Process.Kill()
			<-exited
		}
	}
	select {
	case address := <-found:
		return address, stop, nil
	case <-exited:
		return "", nil, fmt.Errorf("this computer's browser did not start: %s", strings.TrimSpace(output.String()))
	case <-time.After(time.Minute):
		command.Process.Kill()
		<-exited
		return "", nil, fmt.Errorf("this computer's browser did not start within a minute")
	case <-ctx.Done():
		command.Process.Kill()
		<-exited
		return "", nil, ctx.Err()
	}
}
